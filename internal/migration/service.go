package migration

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/access"
)

var ErrAnalyzerUnavailable = errors.New("migration: execution plan analyzer is not configured")

type PreviewResult struct {
	Title          string               `json:"title"`
	SQL            string               `json:"sql"`
	Statements     []string             `json:"statements"`
	StatementCount int                  `json:"statement_count"`
	PlanReport     *ExecutionPlanReport `json:"plan_report,omitempty"`
}

type Service struct {
	repo      Repository
	runner    *Runner
	checker   access.DatabaseChecker
	analyzer  PlanAnalyzer
	history   HistoryRepository
	approvals ApprovalRepository
}

func NewService(repo Repository, runner *Runner, checker access.DatabaseChecker) *Service {
	var analyzer PlanAnalyzer
	if runner != nil && runner.executor != nil {
		if pa, ok := runner.executor.(PlanAnalyzer); ok {
			analyzer = pa
		}
	}
	return &Service{
		repo:     repo,
		runner:   runner,
		checker:  checker,
		analyzer: analyzer,
		history:  historyFrom(repo),
	}
}

func historyFrom(repo Repository) HistoryRepository {
	hr, _ := repo.(HistoryRepository)
	return hr
}

// NewServiceWithApprovals enables the review/queue gate while keeping the
// original constructor useful for isolated migration tests and local tools.
func NewServiceWithApprovals(repo Repository, runner *Runner, checker access.DatabaseChecker, approvals ApprovalRepository) *Service {
	s := NewService(repo, runner, checker)
	s.approvals = approvals
	return s
}

func (s *Service) UserLevel(ctx context.Context, userID, databaseID int64) (access.Level, error) {
	if userID <= 0 {
		return access.LevelNone, ErrUnauthenticated
	}
	if s.checker == nil {
		return access.LevelNone, ErrForbidden
	}
	return s.checker.EffectiveLevel(ctx, userID, databaseID)
}

func (s *Service) requireLevel(ctx context.Context, userID, databaseID int64, min access.Level) (access.Level, error) {
	lvl, err := s.UserLevel(ctx, userID, databaseID)
	if err != nil {
		return access.LevelNone, err
	}
	if lvl < min {
		return lvl, ErrForbidden
	}
	return lvl, nil
}

func (s *Service) CreateDraft(ctx context.Context, userID, databaseID int64, title, sql string) (Migration, error) {
	if _, err := s.requireLevel(ctx, userID, databaseID, access.LevelEdit); err != nil {
		return Migration{}, err
	}
	title = strings.TrimSpace(title)
	if err := ValidateTitle(title); err != nil {
		return Migration{}, err
	}
	if err := ValidateSQL(sql); err != nil {
		return Migration{}, err
	}

	return s.repo.CreateDraft(ctx, CreateDraftParams{
		DatabaseID: databaseID,
		Title:      title,
		SQL:        sql,
		CreatedBy:  userID,
	})
}

func (s *Service) UpdateDraft(ctx context.Context, userID, databaseID, migrationID int64, title, sql string) (Migration, error) {
	if _, err := s.requireLevel(ctx, userID, databaseID, access.LevelEdit); err != nil {
		return Migration{}, err
	}
	title = strings.TrimSpace(title)
	if err := ValidateTitle(title); err != nil {
		return Migration{}, err
	}
	if err := ValidateSQL(sql); err != nil {
		return Migration{}, err
	}

	return s.repo.UpdateDraft(ctx, UpdateDraftParams{
		ID:         migrationID,
		DatabaseID: databaseID,
		Title:      title,
		SQL:        sql,
	})
}

func (s *Service) PreviewSQL(ctx context.Context, userID, databaseID int64, title, sql string) (PreviewResult, error) {
	if _, err := s.requireLevel(ctx, userID, databaseID, access.LevelEdit); err != nil {
		return PreviewResult{}, err
	}
	title = strings.TrimSpace(title)
	if err := ValidateTitle(title); err != nil {
		return PreviewResult{}, err
	}
	if err := ValidateSQL(sql); err != nil {
		return PreviewResult{}, err
	}

	stmts := SplitStatements(sql)
	res := PreviewResult{
		Title:          title,
		SQL:            sql,
		Statements:     stmts,
		StatementCount: len(stmts),
	}
	if s.analyzer != nil {
		if report, err := s.analyzer.AnalyzePlan(ctx, databaseID, sql, false); err == nil {
			res.PlanReport = &report
		}
	}
	return res, nil
}

func (s *Service) AnalyzeSQL(ctx context.Context, userID, databaseID int64, title, sql string, analyze bool) (PreviewResult, error) {
	if _, err := s.requireLevel(ctx, userID, databaseID, access.LevelEdit); err != nil {
		return PreviewResult{}, err
	}
	title = strings.TrimSpace(title)
	if err := ValidateTitle(title); err != nil {
		return PreviewResult{}, err
	}
	if err := ValidateSQL(sql); err != nil {
		return PreviewResult{}, err
	}
	if s.analyzer == nil {
		return PreviewResult{}, ErrAnalyzerUnavailable
	}

	report, err := s.analyzer.AnalyzePlan(ctx, databaseID, sql, analyze)
	if err != nil {
		return PreviewResult{}, err
	}

	stmts := SplitStatements(sql)
	return PreviewResult{
		Title:          title,
		SQL:            sql,
		Statements:     stmts,
		StatementCount: len(stmts),
		PlanReport:     &report,
	}, nil
}

func (s *Service) ExplainMigration(ctx context.Context, userID, databaseID, migrationID int64, analyze bool) (ExecutionPlanReport, error) {
	if _, err := s.requireLevel(ctx, userID, databaseID, access.LevelEdit); err != nil {
		return ExecutionPlanReport{}, err
	}
	if s.analyzer == nil {
		return ExecutionPlanReport{}, ErrAnalyzerUnavailable
	}
	m, err := s.repo.GetByID(ctx, databaseID, migrationID)
	if err != nil {
		return ExecutionPlanReport{}, err
	}
	return s.analyzer.AnalyzePlan(ctx, databaseID, m.SQL, analyze)
}

func (s *Service) Execute(ctx context.Context, userID, databaseID, migrationID int64) (Migration, error) {
	return s.ExecuteOperation(ctx, userID, databaseID, migrationID, "apply")
}

func (s *Service) ExecuteOperation(ctx context.Context, userID, databaseID, migrationID int64, operation string) (Migration, error) {
	if _, err := s.requireLevel(ctx, userID, databaseID, access.LevelEdit); err != nil {
		return Migration{}, err
	}
	if s.approvals != nil {
		m, err := s.repo.GetByID(ctx, databaseID, migrationID)
		if err != nil {
			return Migration{}, err
		}
		op, err := normalizeOperation(operation)
		if err != nil {
			return Migration{}, err
		}
		if _, err := s.approvals.ApprovedReview(ctx, migrationID, CurrentRevision(m), op, ""); err != nil {
			return Migration{}, err
		}
	}
	return s.runner.Run(ctx, userID, databaseID, migrationID)
}

func (s *Service) CreateAndExecute(ctx context.Context, userID, databaseID int64, title, sql string) (Migration, error) {
	draft, err := s.CreateDraft(ctx, userID, databaseID, title, sql)
	if err != nil {
		return Migration{}, err
	}
	return s.Execute(ctx, userID, databaseID, draft.ID)
}

func (s *Service) ListHistory(ctx context.Context, userID, databaseID int64) ([]Migration, error) {
	if _, err := s.requireLevel(ctx, userID, databaseID, access.LevelView); err != nil {
		return nil, err
	}
	return s.repo.ListByDatabase(ctx, databaseID)
}

func (s *Service) GetDetail(ctx context.Context, userID, databaseID, migrationID int64) (Migration, error) {
	if _, err := s.requireLevel(ctx, userID, databaseID, access.LevelView); err != nil {
		return Migration{}, err
	}
	return s.repo.GetByID(ctx, databaseID, migrationID)
}

func (s *Service) CreateDraftWithDefinition(ctx context.Context, userID, databaseID int64, title, up string, down, reason *string) (Migration, error) {
	return s.CreateDraftWithBatchConfig(ctx, userID, databaseID, title, up, down, reason, nil)
}

func (s *Service) CreateDraftWithBatchConfig(ctx context.Context, userID, databaseID int64, title, up string, down, reason *string, batch *BatchConfig) (Migration, error) {
	if _, err := s.requireLevel(ctx, userID, databaseID, access.LevelEdit); err != nil {
		return Migration{}, err
	}
	title = strings.TrimSpace(title)
	if err := ValidateTitle(title); err != nil {
		return Migration{}, err
	}
	if err := ValidateDefinition(up, down, reason); err != nil {
		return Migration{}, err
	}
	if err := ValidateBatchConfig(batch, up, down); err != nil {
		return Migration{}, err
	}
	return s.repo.CreateDraft(ctx, CreateDraftParams{
		DatabaseID: databaseID, Title: title, SQL: up,
		DownSQL: down, IrreversibleReason: reason, BatchConfig: batch, CreatedBy: userID,
	})
}

func (s *Service) UpdateDraftWithDefinition(ctx context.Context, userID, databaseID, migrationID int64, title, up string, down, reason *string) (Migration, error) {
	return s.UpdateDraftWithBatchConfig(ctx, userID, databaseID, migrationID, title, up, down, reason, nil, false)
}

func (s *Service) UpdateDraftWithBatchConfig(ctx context.Context, userID, databaseID, migrationID int64, title, up string, down, reason *string, batch *BatchConfig, replaceBatch bool) (Migration, error) {
	if _, err := s.requireLevel(ctx, userID, databaseID, access.LevelEdit); err != nil {
		return Migration{}, err
	}
	title = strings.TrimSpace(title)
	if err := ValidateTitle(title); err != nil {
		return Migration{}, err
	}
	if err := ValidateDefinition(up, down, reason); err != nil {
		return Migration{}, err
	}
	if err := ValidateBatchConfig(batch, up, down); err != nil {
		return Migration{}, err
	}
	return s.repo.UpdateDraft(ctx, UpdateDraftParams{
		ID: migrationID, DatabaseID: databaseID, Title: title, SQL: up,
		DownSQL: down, IrreversibleReason: reason, BatchConfig: batch, ReplaceBatchConfig: replaceBatch,
	})
}

func (s *Service) PlanOperation(ctx context.Context, userID, databaseID, migrationID int64, op Operation, revision int) (Plan, error) {
	if _, err := s.requireLevel(ctx, userID, databaseID, access.LevelEdit); err != nil {
		return Plan{}, err
	}
	if s.history == nil {
		return Plan{}, ErrHistoryUnavailable
	}
	m, err := s.repo.GetByID(ctx, databaseID, migrationID)
	if err != nil {
		return Plan{}, err
	}
	if revision != 0 && revision != m.Revision {
		return Plan{}, ErrRevisionMismatch
	}
	return s.planForDefinition(ctx, m, op)
}

func (s *Service) planForDefinition(ctx context.Context, m Migration, op Operation) (Plan, error) {
	if m.BatchConfig != nil && (op == OpRollback || op == OpForward) {
		return Plan{}, ErrBatchDirection
	}
	entries, err := s.history.ListHistoryEntries(ctx, m.DatabaseID)
	if err != nil {
		return Plan{}, err
	}
	entries = DedupeAttempts(entries)

	var plan Plan
	switch op {
	case OpApply:
		plan, err = PlanApplyFor(m, entries)
	case OpRollback:
		plan, err = PlanRollback(m, entries)
	case OpForward:
		plan, err = PlanForward(m, entries)
	default:
		return Plan{}, errInvalidRequest
	}
	if err != nil {
		return Plan{}, err
	}
	if err := ValidateSQL(plan.SQL); err != nil {
		return Plan{}, err
	}
	return plan, nil
}

func validateAttempt(a AttemptResult) error {
	switch a.Operation {
	case OpApply, OpRollback, OpForward, OpBatchRecovery, OpCompensation:
	default:
		return fmt.Errorf("%w: operation %q", ErrInvalidAttempt, a.Operation)
	}
	switch a.Status {
	case AttemptSucceeded, AttemptFailed:
	default:
		return fmt.Errorf("%w: status %q", ErrInvalidAttempt, a.Status)
	}
	if a.DatabaseID <= 0 || a.MigrationID <= 0 || a.ActorID <= 0 || a.Revision <= 0 ||
		a.ExecutionID == "" || a.AttemptID == "" || strings.TrimSpace(a.ExecutedSQL) == "" {
		return fmt.Errorf("%w: missing required field", ErrInvalidAttempt)
	}
	if a.Operation.IsRecovery() != (a.RecoveryOfExecutionID != nil) {
		return fmt.Errorf("%w: recovery target must be set for batch recovery", ErrInvalidAttempt)
	}
	isCommittedRollback := a.Operation == OpRollback && a.Status == AttemptSucceeded
	if isCommittedRollback != (a.RollbackOfExecutionID != nil) {
		return fmt.Errorf("%w: rollback target must be set only on successful rollback", ErrInvalidAttempt)
	}
	return nil
}

// RecordAttempt dipanggil Idempoten per (execution_id, attempt_id):
// redelivery mengembalikan entri lama dengan created=false.
func (s *Service) RecordAttempt(ctx context.Context, a AttemptResult) (HistoryEntry, bool, error) {
	if s.history == nil {
		return HistoryEntry{}, false, ErrHistoryUnavailable
	}
	if a.Revision == 0 {
		m, err := s.repo.GetByID(ctx, a.DatabaseID, a.MigrationID)
		if err != nil {
			return HistoryEntry{}, false, err
		}
		a.Revision = m.Revision
	}
	if err := validateAttempt(a); err != nil {
		return HistoryEntry{}, false, err
	}
	entries, err := s.history.ListHistoryEntries(ctx, a.DatabaseID)
	if err != nil {
		return HistoryEntry{}, false, err
	}
	for _, e := range entries {
		if e.ExecutionID == a.ExecutionID && e.AttemptID == a.AttemptID {
			return e, false, nil
		}
	}
	if a.Operation == OpRollback && a.Status == AttemptSucceeded {
		chain, err := ActiveChain(entries)
		if err != nil {
			return HistoryEntry{}, false, err
		}
		if len(chain) == 0 || chain[len(chain)-1].ExecutionID != *a.RollbackOfExecutionID {
			return HistoryEntry{}, false, ErrNotLatestActive
		}
	}
	if a.At.IsZero() {
		a.At = time.Now().UTC()
	}
	return s.history.AppendHistory(ctx, a)
}

func (s *Service) ListAttemptHistory(ctx context.Context, userID, databaseID int64, hideRollback bool) ([]HistoryEntry, error) {
	if _, err := s.requireLevel(ctx, userID, databaseID, access.LevelView); err != nil {
		return nil, err
	}
	if s.history == nil {
		return nil, ErrHistoryUnavailable
	}
	entries, err := s.history.ListHistoryEntries(ctx, databaseID)
	if err != nil {
		return nil, err
	}
	return VisibleHistory(DedupeAttempts(entries), hideRollback), nil
}

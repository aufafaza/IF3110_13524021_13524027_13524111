package migration

import (
	"context"
	"errors"
	"strings"
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
	repo     Repository
	runner   *Runner
	checker  AccessChecker
	analyzer PlanAnalyzer
}

func NewService(repo Repository, runner *Runner, checker AccessChecker) *Service {
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
	}
}

func (s *Service) WithPlanAnalyzer(analyzer PlanAnalyzer) *Service {
	s.analyzer = analyzer
	return s
}

func (s *Service) UserLevel(ctx context.Context, userID, databaseID int64) (AccessLevel, error) {
	if userID <= 0 {
		return LevelNone, ErrUnauthenticated
	}
	if s.checker == nil {
		return LevelNone, ErrForbidden
	}
	return s.checker.EffectiveLevel(ctx, userID, databaseID)
}

func (s *Service) requireLevel(ctx context.Context, userID, databaseID int64, min AccessLevel) (AccessLevel, error) {
	lvl, err := s.UserLevel(ctx, userID, databaseID)
	if err != nil {
		return LevelNone, err
	}
	if lvl < min {
		return lvl, ErrForbidden
	}
	return lvl, nil
}

func (s *Service) CreateDraft(ctx context.Context, userID, databaseID int64, title, sql string) (Migration, error) {
	if _, err := s.requireLevel(ctx, userID, databaseID, LevelEdit); err != nil {
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
	if _, err := s.requireLevel(ctx, userID, databaseID, LevelEdit); err != nil {
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
	if _, err := s.requireLevel(ctx, userID, databaseID, LevelEdit); err != nil {
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
	if _, err := s.requireLevel(ctx, userID, databaseID, LevelEdit); err != nil {
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
	if _, err := s.requireLevel(ctx, userID, databaseID, LevelEdit); err != nil {
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
	if _, err := s.requireLevel(ctx, userID, databaseID, LevelEdit); err != nil {
		return Migration{}, err
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
	if _, err := s.requireLevel(ctx, userID, databaseID, LevelView); err != nil {
		return nil, err
	}
	return s.repo.ListByDatabase(ctx, databaseID)
}

func (s *Service) GetDetail(ctx context.Context, userID, databaseID, migrationID int64) (Migration, error) {
	if _, err := s.requireLevel(ctx, userID, databaseID, LevelView); err != nil {
		return Migration{}, err
	}
	return s.repo.GetByID(ctx, databaseID, migrationID)
}

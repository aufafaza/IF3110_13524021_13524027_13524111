package migration

import (
	"context"
	"strings"
)

type PreviewResult struct {
	Title          string   `json:"title"`
	SQL            string   `json:"sql"`
	Statements     []string `json:"statements"`
	StatementCount int      `json:"statement_count"`
}

type Service struct {
	repo    Repository
	runner  *Runner
	checker AccessChecker
}

func NewService(repo Repository, runner *Runner, checker AccessChecker) *Service {
	return &Service{
		repo:    repo,
		runner:  runner,
		checker: checker,
	}
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
	return PreviewResult{
		Title:          title,
		SQL:            sql,
		Statements:     stmts,
		StatementCount: len(stmts),
	}, nil
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

package migration

import (
	"context"
	"fmt"
	"time"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/dbmanager"
)

const (
	DefaultExecutionTimeout = 30 * time.Second
	recordHistoryTimeout    = 10 * time.Second
)

type Executor interface {
	ExecInTx(ctx context.Context, databaseID int64, sql string) error
}

type TargetPostgresExecutor struct {
	provider dbmanager.Provider
}

func NewTargetPostgresExecutor(provider dbmanager.Provider) *TargetPostgresExecutor {
	return &TargetPostgresExecutor{provider: provider}
}

func (e *TargetPostgresExecutor) ExecInTx(ctx context.Context, databaseID int64, sql string) error {
	pool, err := e.provider.Pool(ctx, databaseID)
	if err != nil {
		return fmt.Errorf("migration: target pool: %w", err)
	}

	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("migration: acquire target connection: %w", err)
	}
	defer conn.Release()

	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("migration: begin tx: %w", err)
	}

	committed := false
	defer func() {
		if !committed {
			rbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			_ = tx.Rollback(rbCtx)
		}
	}()

	if _, err := tx.Exec(ctx, sql); err != nil {
		return fmt.Errorf("migration: exec sql: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("migration: commit tx: %w", err)
	}
	committed = true
	return nil
}

type Runner struct {
	repo     Repository
	locker   Locker
	executor Executor
	timeout  time.Duration
	now      func() time.Time
}

type RunnerOption func(*Runner)

func WithExecutionTimeout(d time.Duration) RunnerOption {
	return func(r *Runner) {
		if d > 0 {
			r.timeout = d
		}
	}
}

func WithClock(now func() time.Time) RunnerOption {
	return func(r *Runner) {
		if now != nil {
			r.now = now
		}
	}
}

func NewRunner(repo Repository, locker Locker, executor Executor, opts ...RunnerOption) *Runner {
	r := &Runner{
		repo:     repo,
		locker:   locker,
		executor: executor,
		timeout:  DefaultExecutionTimeout,
		now:      time.Now,
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

func (r *Runner) Run(ctx context.Context, userID, databaseID, migrationID int64) (Migration, error) {
	execCtx := ctx
	var cancel context.CancelFunc
	if r.timeout > 0 {
		execCtx, cancel = context.WithTimeout(ctx, r.timeout)
		defer cancel()
	}

	release, err := r.locker.Acquire(execCtx, databaseID)
	if err != nil {
		return Migration{}, err
	}
	defer func() {
		_ = release()
	}()

	m, err := r.repo.GetByID(execCtx, databaseID, migrationID)
	if err != nil {
		return Migration{}, err
	}
	if m.Status == StatusApplied {
		return Migration{}, ErrAlreadyApplied
	}
	if m.Status != StatusDraft {
		return Migration{}, ErrNotDraft
	}

	if err := ValidateSQL(m.SQL); err != nil {
		return Migration{}, err
	}

	version, err := r.repo.NextVersion(execCtx, databaseID)
	if err != nil {
		return Migration{}, err
	}

	execErr := r.executor.ExecInTx(execCtx, databaseID, m.SQL)

	status := StatusApplied
	var errMsg *string
	if execErr != nil {
		status = StatusFailed
		msg := execErr.Error()
		errMsg = &msg
	}

	recordCtx, recordCancel := context.WithTimeout(context.WithoutCancel(ctx), recordHistoryTimeout)
	defer recordCancel()

	updated, recErr := r.repo.RecordExecution(recordCtx, RecordExecutionParams{
		ID:           migrationID,
		DatabaseID:   databaseID,
		Version:      version,
		Status:       status,
		ExecutedBy:   userID,
		ExecutedAt:   r.now().UTC(),
		ErrorMessage: errMsg,
	})
	if recErr != nil {
		return Migration{}, recErr
	}

	if execErr != nil {
		return updated, execErr
	}
	return updated, nil
}

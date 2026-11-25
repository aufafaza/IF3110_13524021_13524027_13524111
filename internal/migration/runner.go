package migration

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/database"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/observability"
)

const (
	DefaultExecutionTimeout = 30 * time.Second
	recordHistoryTimeout    = 10 * time.Second
)

type Executor interface {
	ExecInTx(ctx context.Context, databaseID int64, sql string) error
}

type TargetPostgresExecutor struct {
	provider database.Provider
}

func NewTargetPostgresExecutor(provider database.Provider) *TargetPostgresExecutor {
	return &TargetPostgresExecutor{provider: provider}
}

func (e *TargetPostgresExecutor) ExecInTx(ctx context.Context, databaseID int64, sql string) (retErr error) {
	ctx, span := observability.Start(ctx, "database.transaction")
	defer func() { span.End(retErr) }()
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

	started := time.Now()
	if _, err := tx.Exec(ctx, sql); err != nil {
		observability.SQL(ctx, sql, nil, started, 0, err, sqlState(err), "database_id", databaseID, "phase", "execute")
		return fmt.Errorf("migration: exec sql: %w", err)
	}
	observability.SQL(ctx, sql, nil, started, 0, nil, "", "database_id", databaseID, "phase", "execute")

	if err := tx.Commit(ctx); err != nil {
		observability.Log(ctx, slog.LevelError, "transaction.commit", "database_id", databaseID, "status", "error", "error", err.Error())
		return fmt.Errorf("migration: commit tx: %w", err)
	}
	observability.Log(ctx, slog.LevelInfo, "transaction.commit", "database_id", databaseID, "status", "success")
	committed = true
	return nil
}

func sqlState(err error) string {
	if err == nil {
		return ""
	}
	if pgErr, ok := err.(interface{ SQLState() string }); ok {
		return pgErr.SQLState()
	}
	return ""
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

func (r *Runner) Run(ctx context.Context, userID, databaseID, migrationID int64) (result Migration, retErr error) {
	ctx, span := observability.Start(ctx, "migration.run")
	defer func() { span.End(retErr) }()
	observability.Log(ctx, slog.LevelInfo, "migration.start", "actor", userID, "database", databaseID, "migration", migrationID, "status", "started")
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
	observability.Log(ctx, slog.LevelInfo, "migration.result", "actor", userID, "database", databaseID, "migration", migrationID, "status", status)

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

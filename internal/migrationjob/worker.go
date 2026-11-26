package migrationjob

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/access"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/database"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/migration"
)

const (
	stepTimeout = migration.DefaultExecutionTimeout
	claimLease  = stepTimeout + 10*time.Second
	retryDelay  = time.Second
	idleDelay   = 250 * time.Millisecond
)

type Worker struct {
	store   *Store
	target  database.Provider
	auth    Authorization
	plans   Plans
	history History

	afterClaim      func()
	afterCommit     func()
	afterHistoryAck func()
}

func NewWorker(store *Store, target database.Provider, auth Authorization, plans Plans, history History) *Worker {
	return &Worker{store: store, target: target, auth: auth, plans: plans, history: history}
}

func (w *Worker) Run(ctx context.Context) {
	for ctx.Err() == nil {
		worked, err := w.Step(ctx)
		if err != nil && ctx.Err() == nil && !errors.Is(err, errClaimLost) {
			slog.WarnContext(ctx, "migration worker step failed")
		}
		if worked && err == nil {
			continue
		}
		timer := time.NewTimer(idleDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (w *Worker) Step(ctx context.Context) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, stepTimeout)
	defer cancel()
	c, err := w.store.claim(ctx, claimLease)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if w.afterClaim != nil {
		w.afterClaim()
	}

	a, err := w.store.currentAttempt(ctx, c)
	if err != nil {
		return true, err
	}
	if a.finishedAt != nil && a.deliveredAt == nil {
		return true, w.report(ctx, c, a)
	}
	return true, w.processTarget(ctx, c, a)
}

func (w *Worker) report(ctx context.Context, c claim, a attempt) error {
	outcome := Outcome{
		Approval: c.approval, JobID: c.ID, ExecutionID: c.ExecutionID, AttemptID: a.id,
		Attempt: c.Attempts, SQL: a.sql, StartedAt: a.startedAt, FinishedAt: *a.finishedAt,
		FailureCode: a.failureCode,
	}
	if err := w.history.Record(ctx, outcome); err != nil {
		return w.deferStep(ctx, c, Reporting, "history_unavailable")
	}
	if w.afterHistoryAck != nil {
		w.afterHistoryAck()
	}
	return w.store.acknowledgeHistory(ctx, c, a)
}

func (w *Worker) processTarget(ctx context.Context, c claim, a attempt) error {
	inFlight := a.id != "" && a.finishedAt == nil
	waitState := Retrying
	if inFlight {
		waitState = Reconciling
	}
	pool, err := w.target.Pool(ctx, c.DatabaseID)
	if err != nil {
		return w.deferStep(ctx, c, waitState, "target_unavailable")
	}
	// The same target transaction holds the lock, reads evidence and (for
	// a new attempt) executes the migration. Read Committed gives the
	// evidence query a fresh snapshot after taking the lock.
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted, AccessMode: pgx.ReadWrite})
	if err != nil {
		return w.deferStep(ctx, c, waitState, "target_unavailable")
	}
	defer func() {
		rbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = tx.Rollback(rbCtx)
	}()

	locked, err := migration.TryTargetTransactionLock(ctx, tx, c.DatabaseID)
	if err != nil {
		return w.deferStep(ctx, c, waitState, "target_unavailable")
	}
	if !locked {
		return w.deferStep(ctx, c, waitState, "target_busy")
	}
	var targetID string
	err = tx.QueryRow(ctx, `SELECT id::text FROM migration_worker.target_identity
		WHERE singleton AND NOT pg_catalog.pg_is_in_recovery()`).Scan(&targetID)
	if err != nil {
		return w.deferStep(ctx, c, waitState, "target_evidence_unavailable")
	}
	// A registry retarget or a different database after failover is not
	// proof that the preceding transaction rolled back.
	if a.id != "" && targetID != a.targetID {
		return w.deferStep(ctx, c, Reconciling, "target_identity_changed")
	}

	var evidence struct {
		attemptID string
		planHash  string
		time      time.Time
	}
	err = tx.QueryRow(ctx, `SELECT attempt_id::text, plan_hash, completed_at
		FROM migration_worker.completions WHERE execution_id = $1::uuid`, c.ExecutionID).
		Scan(&evidence.attemptID, &evidence.planHash, &evidence.time)
	switch {
	case err == nil:
		if !inFlight || evidence.attemptID != a.id || evidence.planHash != a.planHash {
			return w.deferStep(ctx, c, Reconciling, "target_evidence_mismatch")
		}
		return w.store.finishAttempt(ctx, c, evidence.time, "", false)
	case !errors.Is(err, pgx.ErrNoRows):
		return w.deferStep(ctx, c, waitState, "target_evidence_unavailable")
	case inFlight:
		return w.store.finishAttempt(ctx, c, time.Now().UTC(), "reconciled_rollback", true)
	}

	if err := w.auth.Recheck(ctx, c.approval); err != nil {
		return w.dependencyFailure(ctx, c, err)
	}
	sql, err := w.plans.ResolveSQL(ctx, c.approval)
	if err != nil {
		return w.dependencyFailure(ctx, c, err)
	}
	if err := migration.ValidateSQL(sql); err != nil {
		return w.store.reschedule(ctx, c, Failed, "invalid_plan", 0)
	}
	planHash := fmt.Sprintf("%x", sha256.Sum256([]byte(sql)))
	a, err = w.store.startAttempt(ctx, c, targetID, planHash, sql)
	if err != nil {
		return err
	}
	c.Attempts++
	return w.execute(ctx, tx, c, a.id, planHash, sql)
}

func (w *Worker) execute(ctx context.Context, tx pgx.Tx, c claim, attemptID, planHash, sql string) error {
	for _, statement := range migration.SplitStatements(sql) {
		if _, err := tx.Conn().PgConn().ExecParams(ctx, statement, nil, nil, nil, nil).Close(); err != nil {
			if rbErr := tx.Rollback(ctx); rbErr != nil {
				return w.deferStep(ctx, c, Reconciling, "rollback_unverified")
			}
			code, retryable := executionFailure(err)
			return w.store.finishAttempt(ctx, c, time.Now().UTC(), code, retryable)
		}
	}

	if _, err := tx.Exec(ctx, "SET LOCAL synchronous_commit = on"); err != nil {
		return w.deferStep(ctx, c, Reconciling, "target_evidence_unavailable")
	}
	var finishedAt time.Time
	err := tx.QueryRow(ctx, `INSERT INTO migration_worker.completions (execution_id, attempt_id, plan_hash)
		VALUES ($1::uuid, $2::uuid, $3) RETURNING completed_at`,
		c.ExecutionID, attemptID, planHash).Scan(&finishedAt)
	if err != nil {
		return w.deferStep(ctx, c, Reconciling, "target_evidence_unavailable")
	}
	if err := tx.Commit(ctx); err != nil {
		return w.deferStep(ctx, c, Reconciling, "commit_unverified")
	}
	if w.afterCommit != nil {
		w.afterCommit()
	}
	return w.store.finishAttempt(ctx, c, finishedAt, "", false)
}

func (w *Worker) dependencyFailure(ctx context.Context, c claim, err error) error {
	if errors.Is(err, ErrIneligible) || errors.Is(err, access.ErrForbidden) {
		return w.store.reschedule(ctx, c, Failed, "ineligible", 0)
	}
	return w.deferStep(ctx, c, Retrying, "dependency_unavailable")
}

func (w *Worker) deferStep(ctx context.Context, c claim, state State, code string) error {
	return w.store.reschedule(ctx, c, state, code, retryDelay)
}

func executionFailure(err error) (string, bool) {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return "execution_interrupted", true
	}
	switch pgErr.Code {
	case "40001", "40P01", "55P03", "57014":
		return pgErr.Code, true
	default:
		return pgErr.Code, false
	}
}

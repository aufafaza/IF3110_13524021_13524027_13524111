package migrationjob

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/access"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/migration"
)

const maxBatchSize = 10_000

type batchProgress struct {
	scope, batches, rows int64
	lastKey              *int64
}

func (w *Worker) processBatch(ctx context.Context, tx pgx.Tx, c claim, a attempt, targetID string) error {
	if a.id == "" {
		if err := w.auth.Recheck(ctx, c.approval); err != nil {
			return w.dependencyFailure(ctx, c, err)
		}
		plans, ok := w.plans.(BatchPlans)
		if !ok {
			return w.store.reschedule(ctx, c, Failed, "batch_plan_unavailable", 0)
		}
		plan, err := plans.ResolveBatch(ctx, c.approval)
		if err != nil {
			return w.dependencyFailure(ctx, c, err)
		}
		if !normalizeBatchPlan(&plan) {
			return w.store.reschedule(ctx, c, Failed, "invalid_batch_plan", 0)
		}
		snapshot, err := json.Marshal(plan)
		if err != nil {
			return err
		}
		hash := fmt.Sprintf("%x", sha256.Sum256(snapshot))
		a, err = w.store.startAttempt(ctx, c, targetID, hash, plan.SQL, snapshot)
		if err != nil {
			return err
		}
		a.batchPlan = &plan
		c.Attempts++
	}
	if a.batchPlan == nil || a.finishedAt != nil {
		return w.deferStep(ctx, c, Reconciling, "batch_plan_mismatch")
	}

	var p batchProgress
	var attemptID, hash string
	err := tx.QueryRow(ctx, `SELECT attempt_id::text, plan_hash, scope_rows, committed_batches, committed_rows, last_key
		FROM migration_worker.batch_runs WHERE execution_id = $1::uuid`, c.ExecutionID).
		Scan(&attemptID, &hash, &p.scope, &p.batches, &p.rows, &p.lastKey)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		if c.ScopeRows != nil {
			return w.deferStep(ctx, c, Reconciling, "batch_scope_missing")
		}
		if c.batchPending {
			return w.batchFailure(ctx, c, "reconciled_rollback", true)
		}
		if err := w.auth.Recheck(ctx, c.approval); err != nil {
			return w.batchAuthorizationFailure(ctx, c, err)
		}
		return w.materializeScope(ctx, tx, c, a)
	case err != nil:
		return w.deferStep(ctx, c, Reconciling, "batch_evidence_unavailable")
	case attemptID != a.id || hash != a.planHash ||
		(c.ScopeRows != nil && *c.ScopeRows != p.scope) ||
		p.batches < c.CommittedBatches || p.rows < c.CommittedRows:
		return w.deferStep(ctx, c, Reconciling, "batch_evidence_mismatch")
	}

	// A commit may be ahead of the platform projection. Repair it before
	// authorizing or executing additional work.
	if p.rows == p.scope {
		return w.finishBatchRun(ctx, tx, c, a, p)
	}
	if c.ScopeRows == nil || p.batches > c.CommittedBatches {
		return w.store.projectBatch(ctx, c, p, true)
	}
	if c.batchPending {
		return w.batchFailure(ctx, c, "reconciled_rollback", true)
	}
	if err := w.auth.Recheck(ctx, c.approval); err != nil {
		return w.batchAuthorizationFailure(ctx, c, err)
	}
	return w.applyBatchPage(ctx, tx, c, a, p)
}

func normalizeBatchPlan(plan *BatchPlan) bool {
	if plan.Size <= 0 || plan.Size > maxBatchSize {
		return false
	}
	for _, sql := range []*string{&plan.ScopeSQL, &plan.SQL} {
		statements := migration.SplitStatements(*sql)
		if len(statements) != 1 || migration.ValidateSQL(*sql) != nil {
			return false
		}
		*sql = statements[0]
	}
	return true
}

func (w *Worker) batchAuthorizationFailure(ctx context.Context, c claim, err error) error {
	switch {
	case errors.Is(err, errClaimLost):
		return err
	case errors.Is(err, ErrIneligible), errors.Is(err, access.ErrForbidden):
		return w.batchFailure(ctx, c, "ineligible", false)
	default:
		return w.deferStep(ctx, c, Reconciling, "dependency_unavailable")
	}
}

func (w *Worker) materializeScope(ctx context.Context, tx pgx.Tx, c claim, a attempt) error {
	if err := w.store.markBatchPending(ctx, c); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `SET LOCAL statement_timeout = '20s'`); err != nil {
		return w.deferStep(ctx, c, Reconciling, "target_unavailable")
	}
	if _, err := tx.Exec(ctx, `INSERT INTO migration_worker.batch_runs (execution_id, attempt_id, plan_hash)
		VALUES ($1::uuid, $2::uuid, $3)`, c.ExecutionID, a.id, a.planHash); err != nil {
		return w.rollbackBatch(ctx, tx, c, err)
	}
	// ScopeSQL is an approved SELECT. Its outputs are streamed into the
	// target ledger by PostgreSQL. A duplicate or NULL key fails the insert.
	tag, err := tx.Exec(ctx, `INSERT INTO migration_worker.batch_scope (execution_id, key)
		SELECT $1::uuid, scope.key FROM (`+a.batchPlan.ScopeSQL+"\n) AS scope", pgx.QueryExecModeExec, c.ExecutionID)
	if err != nil {
		return w.rollbackBatch(ctx, tx, c, err)
	}
	p := batchProgress{scope: tag.RowsAffected()}
	return w.commitBatch(ctx, tx, c, a, p, false)
}

func (w *Worker) applyBatchPage(ctx context.Context, tx pgx.Tx, c claim, a attempt, p batchProgress) error {
	// Use separate query shapes to keep an index range scan after page one.
	query := `SELECT key FROM migration_worker.batch_scope WHERE execution_id = $1::uuid ORDER BY key LIMIT $2`
	args := []any{c.ExecutionID, a.batchPlan.Size}
	if p.lastKey != nil {
		query = `SELECT key FROM migration_worker.batch_scope
			WHERE execution_id = $1::uuid AND key > $3 ORDER BY key LIMIT $2`
		args = append(args, *p.lastKey)
	}
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return w.deferStep(ctx, c, Reconciling, "batch_scope_unavailable")
	}
	keys, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return w.deferStep(ctx, c, Reconciling, "batch_scope_unavailable")
	}
	if len(keys) == 0 || int64(len(keys)) > p.scope-p.rows {
		return w.deferStep(ctx, c, Reconciling, "batch_scope_mismatch")
	}
	if _, err := tx.Exec(ctx, `SET LOCAL statement_timeout = '20s'`); err != nil {
		return w.deferStep(ctx, c, Reconciling, "target_unavailable")
	}
	if err := w.store.markBatchPending(ctx, c); err != nil {
		return err
	}
	if err := applyBatchUpdate(ctx, tx, a.batchPlan.SQL, keys); err != nil {
		return w.rollbackBatch(ctx, tx, c, err)
	}
	affected := int64(len(keys))
	p.batches++
	p.rows += affected
	p.lastKey = &keys[len(keys)-1]
	if _, err := tx.Exec(ctx, `INSERT INTO migration_worker.batch_checkpoints
		(execution_id, sequence, last_key, affected_rows) VALUES ($1::uuid, $2, $3, $4)`,
		c.ExecutionID, p.batches, *p.lastKey, affected); err != nil {
		return w.rollbackBatch(ctx, tx, c, err)
	}
	return w.commitBatch(ctx, tx, c, a, p, true)
}

var errBatchKeysMismatch = errors.New("batch UPDATE did not return the expected keys")

func applyBatchUpdate(ctx context.Context, tx pgx.Tx, sql string, keys []int64) error {
	remaining := make(map[int64]struct{}, len(keys))
	for _, key := range keys {
		remaining[key] = struct{}{}
	}
	rows, err := tx.Query(ctx, sql, pgx.QueryExecModeExec, keys)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var key int64
		if err := rows.Scan(&key); err != nil {
			return errBatchKeysMismatch
		}
		if _, ok := remaining[key]; !ok {
			return errBatchKeysMismatch
		}
		delete(remaining, key)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(remaining) != 0 || !rows.CommandTag().Update() {
		return errBatchKeysMismatch
	}
	return nil
}

func (w *Worker) commitBatch(ctx context.Context, tx pgx.Tx, c claim, a attempt, p batchProgress, applied bool) error {
	if _, err := tx.Exec(ctx, `UPDATE migration_worker.batch_runs
		SET scope_rows = $2, committed_batches = $3, committed_rows = $4, last_key = $5
		WHERE execution_id = $1::uuid`, c.ExecutionID, p.scope, p.batches, p.rows, p.lastKey); err != nil {
		return w.rollbackBatch(ctx, tx, c, err)
	}
	var completedAt time.Time
	if p.rows == p.scope {
		err := tx.QueryRow(ctx, `INSERT INTO migration_worker.completions (execution_id, attempt_id, plan_hash)
			VALUES ($1::uuid, $2::uuid, $3) RETURNING completed_at`, c.ExecutionID, a.id, a.planHash).Scan(&completedAt)
		if err != nil {
			return w.rollbackBatch(ctx, tx, c, err)
		}
	}
	if _, err := tx.Exec(ctx, "SET LOCAL synchronous_commit = on"); err != nil {
		return w.rollbackBatch(ctx, tx, c, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return w.deferStep(ctx, c, Reconciling, "batch_commit_unverified")
	}
	if applied && w.afterCommit != nil {
		w.afterCommit()
	}
	if !completedAt.IsZero() {
		if err := w.store.projectBatch(ctx, c, p, false); err != nil {
			return err
		}
		return w.store.finishAttempt(ctx, c, completedAt, "", false)
	}
	return w.store.projectBatch(ctx, c, p, true)
}

func (w *Worker) finishBatchRun(ctx context.Context, tx pgx.Tx, c claim, a attempt, p batchProgress) error {
	var id, hash string
	var completedAt time.Time
	err := tx.QueryRow(ctx, `SELECT attempt_id::text, plan_hash, completed_at
		FROM migration_worker.completions WHERE execution_id = $1::uuid`, c.ExecutionID).Scan(&id, &hash, &completedAt)
	if err != nil || id != a.id || hash != a.planHash {
		return w.deferStep(ctx, c, Reconciling, "batch_completion_unverified")
	}
	if err := w.store.projectBatch(ctx, c, p, false); err != nil {
		return err
	}
	return w.store.finishAttempt(ctx, c, completedAt, "", false)
}

func (w *Worker) rollbackBatch(ctx context.Context, tx pgx.Tx, c claim, cause error) error {
	if err := tx.Rollback(ctx); err != nil {
		return w.deferStep(ctx, c, Reconciling, "rollback_unverified")
	}
	if errors.Is(cause, errBatchKeysMismatch) {
		return w.batchFailure(ctx, c, "batch_keys_mismatch", false)
	}
	code, retryable := executionFailure(cause)
	return w.batchFailure(ctx, c, code, retryable)
}

func (w *Worker) batchFailure(ctx context.Context, c claim, code string, retryable bool) error {
	if retryable && c.batchFailures+1 < c.attemptLimit {
		return w.store.retryBatch(ctx, c, code)
	}
	return w.store.finishAttempt(ctx, c, time.Now().UTC(), code, false)
}

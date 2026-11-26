package migrationjob

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/migration"
)

func (c claim) batchRoot() string {
	if migration.Operation(c.Operation).IsRecovery() {
		return c.TargetExecutionID
	}
	return c.ExecutionID
}

type rootBatchAttempt struct {
	id, targetID, planHash string
}

func (s *Store) rootBatchAttempt(ctx context.Context, c claim) (rootBatchAttempt, error) {
	var a rootBatchAttempt
	err := s.pool.QueryRow(ctx, `SELECT a.id::text,a.target_id::text,a.plan_hash
		FROM migration_async_jobs root JOIN migration_async_attempts a
		  ON a.job_id=root.id AND a.number=root.attempt_count
		WHERE root.execution_id::text=$1 AND root.database_id=$2 AND root.migration_id=$3 AND root.revision=$4
		  AND root.owns_database AND root.batch_paused AND root.state='partial'
		  AND root.operation='apply' AND root.mode='batch'`,
		c.TargetExecutionID, c.DatabaseID, c.MigrationID, c.Revision).Scan(&a.id, &a.targetID, &a.planHash)
	return a, err
}

func (w *Worker) processRecovery(ctx context.Context, tx pgx.Tx, c claim, a attempt, targetID string) error {
	root, err := w.store.rootBatchAttempt(ctx, c)
	if errors.Is(err, pgx.ErrNoRows) && a.id == "" {
		return w.store.reschedule(ctx, c, Failed, "recovery_unavailable", 0)
	}
	if err != nil {
		return w.deferStep(ctx, c, Reconciling, "batch_owner_unavailable")
	}
	if root.targetID != targetID {
		return w.deferStep(ctx, c, Reconciling, "target_identity_changed")
	}

	var p batchProgress
	var id, hash string
	err = tx.QueryRow(ctx, `SELECT attempt_id::text,plan_hash,scope_rows,committed_batches,committed_rows,last_key
		FROM migration_worker.batch_runs WHERE execution_id=$1::uuid`, c.TargetExecutionID).
		Scan(&id, &hash, &p.scope, &p.batches, &p.rows, &p.lastKey)
	if err != nil || id != root.id || hash != root.planHash {
		return w.deferStep(ctx, c, Reconciling, "batch_evidence_mismatch")
	}
	err = tx.QueryRow(ctx, `SELECT count(*),coalesce(sum(affected_rows),0)
		FROM migration_worker.compensation_checkpoints WHERE execution_id=$1::uuid`, c.TargetExecutionID).
		Scan(&p.compensatedBatches, &p.compensatedRows)
	if err != nil || p.compensatedBatches > p.batches || p.compensatedRows > p.rows {
		return w.deferStep(ctx, c, Reconciling, "batch_compensation_evidence_unavailable")
	}

	compensating := c.Operation == string(migration.OpCompensation)
	if a.id == "" {
		if p.batches == 0 || p.rows == p.scope || (!compensating && p.compensatedBatches != 0) ||
			(compensating && p.compensatedBatches == p.batches) {
			return w.store.reschedule(ctx, c, Failed, "recovery_unavailable", 0)
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
		if compensating && plan.Recovery == nil {
			return w.store.reschedule(ctx, c, Failed, "compensation_unsafe", 0)
		}
		snapshot, err := json.Marshal(plan)
		if err != nil {
			return err
		}
		hash := fmt.Sprintf("%x", sha256.Sum256(snapshot))
		a, err = w.store.startAttempt(ctx, c, targetID, hash, plan.SQL, snapshot, nil)
		if err != nil {
			return err
		}
		a.batchPlan = &plan
		c.Attempts++
	}
	if a.batchPlan == nil || a.finishedAt != nil ||
		p.batches < c.CommittedBatches || p.compensatedBatches < c.CompensatedBatches {
		return w.deferStep(ctx, c, Reconciling, "batch_evidence_mismatch")
	}
	if (compensating && p.compensatedBatches == p.batches) || (!compensating && p.rows == p.scope) {
		return w.finishBatchRun(ctx, tx, c, a, p)
	}
	if c.ScopeRows == nil || p.batches > c.CommittedBatches || p.compensatedBatches > c.CompensatedBatches {
		return w.store.projectBatch(ctx, c, p, true)
	}
	if c.batchPending {
		return w.batchFailure(ctx, c, "reconciled_rollback", true)
	}
	if err := w.auth.Recheck(ctx, c.approval); err != nil {
		return w.batchAuthorizationFailure(ctx, c, err)
	}
	if err := w.recheckBatchPlan(ctx, c, a); err != nil {
		return w.batchAuthorizationFailure(ctx, c, err)
	}
	if compensating {
		return w.compensatePage(ctx, tx, c, a, p)
	}
	return w.applyBatchPage(ctx, tx, c, a, p)
}

func (w *Worker) recheckBatchPlan(ctx context.Context, c claim, a attempt) error {
	resolver, ok := w.plans.(BatchPlans)
	if !ok {
		return ErrIneligible
	}
	plan, err := resolver.ResolveBatch(ctx, c.approval)
	if err != nil {
		return err
	}
	if !normalizeBatchPlan(&plan) {
		return ErrIneligible
	}
	snapshot, err := json.Marshal(plan)
	if err != nil {
		return err
	}
	if fmt.Sprintf("%x", sha256.Sum256(snapshot)) != a.planHash {
		return ErrIneligible
	}
	return nil
}

type batchExplainNode struct {
	NodeType  string `json:"Node Type"`
	Operation string `json:"Operation"`
	Schema    string `json:"Schema"`
	Relation  string `json:"Relation Name"`
	Plans     []batchExplainNode
}

func (n batchExplainNode) writes() bool {
	if n.NodeType == "ModifyTable" {
		return true
	}
	for _, child := range n.Plans {
		if child.writes() {
			return true
		}
	}
	return false
}

func verifyBatchUpdateTable(ctx context.Context, tx pgx.Tx, sql string, table migration.BatchTable, keys []int64) error {
	relation := pgx.Identifier{table.Schema, table.Table}.Sanitize()
	if _, err := tx.Exec(ctx, "LOCK TABLE "+relation+" IN ROW EXCLUSIVE MODE"); err != nil {
		return err
	}
	var supported bool
	err := tx.QueryRow(ctx, `SELECT c.relkind='r'
		AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_inherits i WHERE i.inhparent=c.oid)
		AND NOT EXISTS (
			SELECT 1 FROM pg_catalog.pg_trigger t WHERE t.tgrelid=c.oid AND NOT t.tgisinternal AND t.tgenabled<>'D'
		)
		AND NOT EXISTS (
			SELECT 1 FROM pg_catalog.pg_constraint k WHERE k.confrelid=c.oid AND k.contype='f'
			  AND k.confupdtype IN ('c','n','d')
		)
		FROM pg_catalog.pg_class c WHERE c.oid=$1::regclass`, relation).Scan(&supported)
	if err != nil {
		return err
	}
	if !supported {
		return migration.ErrCompensationUnsafe
	}
	var raw []byte
	if err := tx.QueryRow(ctx, "EXPLAIN (VERBOSE, FORMAT JSON) "+sql, pgx.QueryExecModeExec, keys).Scan(&raw); err != nil {
		return err
	}
	var report []struct{ Plan batchExplainNode }
	if err := json.Unmarshal(raw, &report); err != nil {
		return err
	}
	if len(report) != 1 || report[0].Plan.NodeType != "ModifyTable" || report[0].Plan.Operation != "Update" ||
		report[0].Plan.Schema != table.Schema || report[0].Plan.Relation != table.Table {
		return migration.ErrCompensationUnsafe
	}
	for _, child := range report[0].Plan.Plans {
		if child.writes() {
			return migration.ErrCompensationUnsafe
		}
	}
	return nil
}

func captureBatchRows(ctx context.Context, tx pgx.Tx, executionID string, table migration.BatchTable, keys []int64, before bool) error {
	relation := pgx.Identifier{table.Schema, table.Table}.Sanitize()
	key := pgx.Identifier{table.Key}.Sanitize()
	var tag pgconn.CommandTag
	var err error
	if before {
		tag, err = tx.Exec(ctx, `INSERT INTO migration_worker.batch_row_snapshots(execution_id,key,before_row)
			SELECT $1::uuid,t.`+key+`,to_jsonb(t) FROM `+relation+` t
			WHERE t.`+key+`=ANY($2::bigint[]) FOR UPDATE OF t`, executionID, keys)
	} else {
		tag, err = tx.Exec(ctx, `UPDATE migration_worker.batch_row_snapshots s SET after_row=to_jsonb(t)
			FROM `+relation+` t WHERE s.execution_id=$1::uuid AND s.key=t.`+key+` AND t.`+key+`=ANY($2::bigint[])`,
			executionID, keys)
	}
	if err == nil && tag.RowsAffected() != int64(len(keys)) {
		return migration.ErrCompensationUnsafe
	}
	return err
}

func verifyBatchRows(ctx context.Context, tx pgx.Tx, executionID string, table migration.BatchTable, keys []int64, before bool) error {
	relation := pgx.Identifier{table.Schema, table.Table}.Sanitize()
	key := pgx.Identifier{table.Key}.Sanitize()
	column := "after_row"
	if before {
		column = "before_row"
	}
	var count int64
	err := tx.QueryRow(ctx, `WITH locked AS MATERIALIZED (
		SELECT t.`+key+` AS key,to_jsonb(t) AS data FROM `+relation+` t
		WHERE t.`+key+`=ANY($2::bigint[]) FOR UPDATE OF t
	)
	SELECT count(*) FROM locked l JOIN migration_worker.batch_row_snapshots s
	  ON s.execution_id=$1::uuid AND s.key=l.key AND s.`+column+`=l.data`, executionID, keys).Scan(&count)
	if err == nil && count != int64(len(keys)) {
		return migration.ErrCompensationUnsafe
	}
	return err
}

func (w *Worker) compensatePage(ctx context.Context, tx pgx.Tx, c claim, a attempt, p batchProgress) error {
	sequence := p.batches - p.compensatedBatches
	rows, err := tx.Query(ctx, `SELECT scope.key FROM migration_worker.batch_checkpoints checkpoint
		JOIN migration_worker.batch_scope scope ON scope.execution_id=checkpoint.execution_id
		LEFT JOIN migration_worker.batch_checkpoints prev
		  ON prev.execution_id=checkpoint.execution_id AND prev.sequence=checkpoint.sequence-1
		WHERE checkpoint.execution_id=$1::uuid AND checkpoint.sequence=$2
		  AND scope.key<=checkpoint.last_key AND (prev.last_key IS NULL OR scope.key>prev.last_key)
		ORDER BY scope.key LIMIT $3`, c.TargetExecutionID, sequence, a.batchPlan.Size+1)
	if err != nil {
		return w.deferStep(ctx, c, Reconciling, "batch_scope_unavailable")
	}
	keys, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return w.deferStep(ctx, c, Reconciling, "batch_scope_unavailable")
	}
	var expected int64
	err = tx.QueryRow(ctx, `SELECT affected_rows FROM migration_worker.batch_checkpoints
		WHERE execution_id=$1::uuid AND sequence=$2`, c.TargetExecutionID, sequence).Scan(&expected)
	if err != nil || len(keys) == 0 || len(keys) > a.batchPlan.Size || int64(len(keys)) != expected ||
		p.compensatedRows+expected > p.rows {
		return w.deferStep(ctx, c, Reconciling, "batch_scope_mismatch")
	}
	if _, err := tx.Exec(ctx, `SET LOCAL statement_timeout='20s'`); err != nil {
		return w.deferStep(ctx, c, Reconciling, "target_unavailable")
	}
	if err := w.store.markBatchPending(ctx, c); err != nil {
		return err
	}
	table := *a.batchPlan.Recovery
	if err := verifyBatchUpdateTable(ctx, tx, a.batchPlan.SQL, table, keys); err != nil {
		return w.rollbackBatch(ctx, tx, c, err)
	}
	if err := verifyBatchRows(ctx, tx, c.TargetExecutionID, table, keys, false); err != nil {
		return w.rollbackBatch(ctx, tx, c, err)
	}
	if err := applyBatchUpdate(ctx, tx, a.batchPlan.SQL, keys); err != nil {
		if errors.Is(err, errBatchKeysMismatch) {
			err = migration.ErrCompensationUnsafe
		}
		return w.rollbackBatch(ctx, tx, c, err)
	}
	if err := verifyBatchRows(ctx, tx, c.TargetExecutionID, table, keys, true); err != nil {
		return w.rollbackBatch(ctx, tx, c, err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO migration_worker.compensation_checkpoints
		(execution_id,sequence,recovery_execution_id,attempt_id,affected_rows)
		VALUES ($1::uuid,$2,$3::uuid,$4::uuid,$5)`, c.TargetExecutionID, sequence, c.ExecutionID, a.id, expected); err != nil {
		return w.rollbackBatch(ctx, tx, c, err)
	}
	p.compensatedBatches++
	p.compensatedRows += expected
	return w.commitBatchStep(ctx, tx, c, a, p, p.compensatedBatches == p.batches, true)
}

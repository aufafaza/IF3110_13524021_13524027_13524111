//go:build integration

package migrationjob

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

const selectBatchKeys = "SELECT id AS key FROM migration_job_batch_fixture WHERE selected"
const updateBatchKeys = "UPDATE migration_job_batch_fixture SET count = count + 1 WHERE id = ANY($1::bigint[]) RETURNING id"

func seedBenchmarkTable(t *testing.T, e workerTest, rows int) {
	t.Helper()
	_, err := e.target.Exec(t.Context(), `DROP TABLE IF EXISTS migration_job_batch_fixture;
		CREATE TABLE migration_job_batch_fixture (id bigint PRIMARY KEY, count int NOT NULL DEFAULT 0, selected bool NOT NULL DEFAULT true)`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.target.Exec(t.Context(), `INSERT INTO migration_job_batch_fixture (id) SELECT generate_series(1, $1)`, rows); err != nil {
		t.Fatal(err)
	}
}

func setupBatch(t *testing.T, e workerTest, rows, size int) Job {
	t.Helper()
	seedBenchmarkTable(t, e, rows)
	plan := BatchPlan{ScopeSQL: selectBatchKeys, SQL: updateBatchKeys, Size: size}
	if _, err := e.platform.Exec(t.Context(), `INSERT INTO fixture_approvals (id, actor_id, database_id, mode, sql, batch_plan)
		VALUES ('batch', $1, $2, 'batch', '', $3)`, e.actorID, e.dbID, plan); err != nil {
		t.Fatal(err)
	}
	requests, err := e.fixtures.PendingDispatch(t.Context())
	if err != nil || len(requests) != 1 {
		t.Fatalf("expected one approval: %v, %v", requests, err)
	}
	job, err := NewDispatcher(e.store, e.fixtures).Dispatch(t.Context(), requests[0])
	if err != nil {
		t.Fatal(err)
	}
	return job
}

func batchCounts(t *testing.T, e workerTest, want ...int) {
	t.Helper()
	rows, err := e.target.Query(t.Context(), "SELECT count FROM migration_job_batch_fixture ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	got, err := pgx.CollectRows(rows, pgx.RowTo[int])
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("batch effects = %v, want %v: %v", got, want, err)
	}
}

func TestBatchRecoversAfterIntermediateAndFinalCommit(t *testing.T) {
	e := setupWorkerTest(t)
	job := setupBatch(t, e, 5, 2)
	step(t, e.worker()) // Commit fixed membership.
	batchCounts(t, e, 0, 0, 0, 0, 0)

	// Change the applicability predicate and insert a new key after the
	// scope commit. Both pages must still follow the original membership.
	if _, err := e.target.Exec(t.Context(), `UPDATE migration_job_batch_fixture SET selected = false WHERE id = 4;
		INSERT INTO migration_job_batch_fixture (id) VALUES (0), (6)`); err != nil {
		t.Fatal(err)
	}
	crashWorker(t, e.dbID, "commit")
	batchCounts(t, e, 0, 1, 1, 0, 0, 0, 0)
	e.expireClaim(t, job.ID)
	step(t, e.worker()) // Repair progress from the target without reapplying.
	checkpointed := e.job(t, job.ID, Partial, 1)
	if checkpointed.CommittedBatches != 1 || checkpointed.CommittedRows != 2 || checkpointed.ScopeRows == nil || *checkpointed.ScopeRows != 5 {
		t.Fatalf("recovered checkpoint: %+v", checkpointed)
	}

	var history int
	if err := e.platform.QueryRow(t.Context(), "SELECT count(*) FROM fixture_history").Scan(&history); err != nil || history != 0 {
		t.Fatalf("history before completion = %d, want 0: %v", history, err)
	}
	step(t, e.worker())
	crashWorker(t, e.dbID, "commit")
	batchCounts(t, e, 0, 1, 1, 1, 1, 1, 0)
	e.expireClaim(t, job.ID)

	step(t, e.worker())
	e.job(t, job.ID, Reporting, 1)
	step(t, e.worker())
	finished := e.job(t, job.ID, Succeeded, 1)
	if finished.CommittedBatches != 3 || finished.CommittedRows != 5 {
		t.Fatalf("final progress: %+v", finished)
	}
	batchCounts(t, e, 0, 1, 1, 1, 1, 1, 0)

	var checkpoints int
	err := e.target.QueryRow(t.Context(), `SELECT count(*) FROM migration_worker.batch_checkpoints
		WHERE execution_id = $1::uuid AND last_key = least(sequence * 2, 5)`, job.ExecutionID).Scan(&checkpoints)
	if err != nil || checkpoints != 3 {
		t.Fatalf("committed checkpoints = %d: %v", checkpoints, err)
	}
	if err := e.platform.QueryRow(t.Context(), `SELECT count(*) FROM fixture_history
		WHERE execution_id = $1 AND payload->>'FailureCode' = ''`, job.ExecutionID).Scan(&history); err != nil || history != 1 {
		t.Fatalf("success history = %d, want 1: %v", history, err)
	}
}

func TestFailedBatchPreservesPreviousCommitAndBlocksSubsequentJobs(t *testing.T) {
	e := setupWorkerTest(t)
	job := setupBatch(t, e, 4, 2)
	step(t, e.worker())
	step(t, e.worker())

	if _, err := e.target.Exec(t.Context(), `ALTER TABLE migration_job_batch_fixture
		ADD CHECK (id <= 2 OR count = 0)`); err != nil {
		t.Fatal(err)
	}
	step(t, e.worker())
	step(t, e.worker())
	stalled := e.job(t, job.ID, Partial, 1)
	if !stalled.BatchPaused || stalled.CommittedBatches != 1 || stalled.CommittedRows != 2 || stalled.FailureCode != "23514" {
		t.Fatalf("failed batch progress: %+v", stalled)
	}
	batchCounts(t, e, 1, 1, 0, 0)

	e.approve(t, "after-partial", incrementSQL)
	if err := NewDispatcher(e.store, e.fixtures).DispatchPending(t.Context()); err != nil {
		t.Fatal(err)
	}
	if worked, err := e.worker().Step(t.Context()); worked || err != nil {
		t.Fatalf("worker overtook paused migration: %v, %v", worked, err)
	}
}

func TestBatchReconcilesRejectedCommitBeforeRetrying(t *testing.T) {
	e := setupWorkerTest(t)
	job := setupBatch(t, e, 4, 2)
	if _, err := e.target.Exec(t.Context(), `UPDATE migration_job_batch_fixture SET count = id * 2;
		INSERT INTO migration_job_batch_fixture (id, count, selected) VALUES (5, 9, false);
		ALTER TABLE migration_job_batch_fixture ADD UNIQUE (count) DEFERRABLE INITIALLY DEFERRED`); err != nil {
		t.Fatal(err)
	}
	allowNextStep := func() {
		t.Helper()
		if _, err := e.platform.Exec(t.Context(), `UPDATE migration_async_jobs SET eligible_at = clock_timestamp()
			WHERE id = $1`, job.ID); err != nil {
			t.Fatal(err)
		}
	}
	w := e.worker()
	step(t, w)
	step(t, w)
	step(t, w)
	reconciling := e.job(t, job.ID, Reconciling, 1)
	if reconciling.FailureCode != "batch_commit_unverified" || reconciling.CommittedBatches != 1 {
		t.Fatalf("unexpected status after rejected commit: %+v", reconciling)
	}
	batchCounts(t, e, 3, 5, 6, 8, 9)

	allowNextStep()
	step(t, e.worker())
	partial := e.job(t, job.ID, Partial, 1)
	if partial.FailureCode != "reconciled_rollback" || partial.CommittedBatches != 1 {
		t.Fatalf("unexpected status after checking target evidence: %+v", partial)
	}

	if _, err := e.target.Exec(t.Context(), "DELETE FROM migration_job_batch_fixture WHERE id = 5"); err != nil {
		t.Fatal(err)
	}
	allowNextStep()
	step(t, e.worker())
	step(t, e.worker())
	e.job(t, job.ID, Succeeded, 1)
	batchCounts(t, e, 3, 5, 7, 9)
}

func TestEmptyBatchCommitsCompletionWithoutCheckpoint(t *testing.T) {
	e := setupWorkerTest(t)
	job := setupBatch(t, e, 0, 2)
	step(t, e.worker())
	step(t, e.worker())
	finished := e.job(t, job.ID, Succeeded, 1)
	if finished.CommittedBatches != 0 || finished.CommittedRows != 0 || finished.ScopeRows == nil || *finished.ScopeRows != 0 {
		t.Fatalf("empty scope progress: %+v", finished)
	}
}

func TestBatchRejectsDifferentKeysWithSameRowCount(t *testing.T) {
	e := setupWorkerTest(t)
	job := setupBatch(t, e, 4, 2)
	plan := BatchPlan{
		ScopeSQL: selectBatchKeys,
		SQL: `UPDATE migration_job_batch_fixture SET count = count + 1
			WHERE id IN (SELECT key + 2 FROM unnest($1::bigint[]) AS keys(key)) RETURNING id`,
		Size: 2,
	}
	payload, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.platform.Exec(t.Context(), "UPDATE fixture_approvals SET batch_plan = $1", payload); err != nil {
		t.Fatal(err)
	}
	w := e.worker()
	step(t, w)
	step(t, w)
	step(t, w)
	failed := e.job(t, job.ID, Failed, 1)
	if failed.FailureCode != "batch_keys_mismatch" || failed.CommittedRows != 0 {
		t.Fatalf("unexpected result after wrong-key UPDATE: %+v", failed)
	}
	batchCounts(t, e, 0, 0, 0, 0)
}

func TestLargeBatchComparison(t *testing.T) {
	raw := os.Getenv("MIGRATION_BATCH_BENCH_ROWS")
	if raw == "" {
		t.Skip("set MIGRATION_BATCH_BENCH_ROWS to run the large-update comparison")
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		t.Fatalf("invalid MIGRATION_BATCH_BENCH_ROWS: %q", raw)
	}
	e := setupWorkerTest(t)
	const size = 10_000
	job := setupBatch(t, e, n, size)
	start := time.Now()
	tag, err := e.target.Exec(t.Context(), "UPDATE migration_job_batch_fixture SET count = count + 1")
	ordinary := time.Since(start)
	if err != nil || tag.RowsAffected() != int64(n) {
		t.Fatalf("ordinary UPDATE: %v; rows=%d", err, tag.RowsAffected())
	}
	if ordinary <= time.Second {
		t.Errorf("ordinary UPDATE took %s, increase MIGRATION_BATCH_BENCH_ROWS until it exceeds 1s", ordinary)
	}

	if _, err := e.target.Exec(t.Context(), "TRUNCATE migration_job_batch_fixture"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.target.Exec(t.Context(), "INSERT INTO migration_job_batch_fixture (id) SELECT generate_series(1, $1)", n); err != nil {
		t.Fatal(err)
	}
	w := e.worker()
	start = time.Now()
	for range (n-1)/size + 3 { // scope, ceil(n/size) pages, history
		step(t, w)
	}
	batched := time.Since(start)
	finished := e.job(t, job.ID, Succeeded, 1)
	var updated int64
	err = e.target.QueryRow(t.Context(), "SELECT count(*) FROM migration_job_batch_fixture WHERE count = 1").Scan(&updated)
	if err != nil || updated != int64(n) || finished.CommittedRows != updated {
		t.Fatalf("batch UPDATE: %v, target rows=%d, progress=%+v", err, updated, finished)
	}
	t.Logf("%d rows, ordinary UPDATE %s, batch size %d, %d batches, batched worker %s",
		n, ordinary, size, finished.CommittedBatches, batched)
}

// TestOrdinarySyncAsyncComparison measures elapsed time under the same workload
// and alternates the order to reduce systematic warm-cache bias. The async
// measurement includes the production worker loop's queue polling interval.
func TestOrdinarySyncAsyncComparison(t *testing.T) {
	raw := os.Getenv("MIGRATION_BATCH_BENCH_ROWS")
	if raw == "" {
		t.Skip("set MIGRATION_BATCH_BENCH_ROWS to run the sync/async comparison")
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		t.Fatalf("invalid MIGRATION_BATCH_BENCH_ROWS: %q", raw)
	}
	e := setupWorkerTest(t)
	workerCtx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		e.worker().Run(workerCtx)
	}()
	defer func() {
		cancel()
		<-done
	}()

	const updateAll = "UPDATE migration_job_batch_fixture SET count = count + 1"
	const pairs = 4
	var syncTimes, asyncTimes, dispatchTimes []time.Duration

	for trial := range pairs {
		requestID := fmt.Sprintf("ordinary-%d", trial)
		e.approve(t, requestID, updateAll)
		requests, err := e.fixtures.PendingDispatch(t.Context())
		if err != nil || len(requests) != 1 {
			t.Fatalf("expected one pending approval: %v (%v)", requests, err)
		}
		for pass := range 2 {
			seedBenchmarkTable(t, e, n)
			if (trial+pass)%2 == 0 {
				start := time.Now()
				tag, err := e.target.Exec(t.Context(), updateAll)
				syncTimes = append(syncTimes, time.Since(start))
				if err != nil || tag.RowsAffected() != int64(n) {
					t.Fatalf("synchronous UPDATE touched %d rows: %v", tag.RowsAffected(), err)
				}
			} else {
				start := time.Now()
				job, err := NewDispatcher(e.store, e.fixtures).Dispatch(t.Context(), requests[0])
				dispatchTimes = append(dispatchTimes, time.Since(start))
				if err != nil {
					t.Fatal(err)
				}
				waitForBenchmarkJob(t, e, job)
				asyncTimes = append(asyncTimes, time.Since(start))
			}
			var updated int
			err := e.target.QueryRow(t.Context(), "SELECT count(*) FROM migration_job_batch_fixture WHERE count = 1").Scan(&updated)
			if err != nil || updated != n {
				t.Fatalf("expected %d rows updated once, got %d: %v", n, updated, err)
			}
		}
		t.Logf("pair %d, synchronous %s, async dispatch %s, async end-to-end %s",
			trial+1, syncTimes[trial], dispatchTimes[trial], asyncTimes[trial])
	}

	var history int
	err = e.platform.QueryRow(t.Context(), `SELECT count(*) FROM fixture_history
		WHERE payload->>'FailureCode' = '' AND payload->>'SQL' = $1`, updateAll).Scan(&history)
	if err != nil || history != pairs {
		t.Fatalf("expected %d successful async history entries, got %d: %v", pairs, history, err)
	}
	median := func(times []time.Duration) time.Duration {
		slices.Sort(times)
		mid := len(times) / 2
		return (times[mid-1] + times[mid]) / 2
	}
	t.Logf("%d rows, %d paired runs, medians: synchronous %s, async dispatch %s, async end-to-end %s",
		n, pairs, median(syncTimes), median(dispatchTimes), median(asyncTimes))
}

func waitForBenchmarkJob(t *testing.T, e workerTest, job Job) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		current, err := e.store.Get(ctx, job.DatabaseID, job.ID)
		if err != nil {
			t.Fatal(err)
		}
		switch current.State {
		case Succeeded:
			if current.Attempts != 1 {
				t.Fatalf("expected one async attempt, got %+v", current)
			}
			return
		case Failed:
			t.Fatalf("async benchmark job failed: %+v", current)
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatalf("timed out waiting for async benchmark job: %+v", current)
		}
	}
}

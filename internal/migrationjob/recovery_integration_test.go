//go:build integration

package migrationjob

import (
	"fmt"
	"net/http"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/migration"
)

const recoveryUpSQL = "UPDATE recovery_fixture SET value=value+1 WHERE id=ANY($1::bigint[]) RETURNING id"
const recoveryDownSQL = "UPDATE recovery_fixture SET value=value-1 WHERE id=ANY($1::bigint[]) RETURNING id"

func (e reviewTest) pauseProductionBatch(t *testing.T, withSnapshots bool) (migration.Migration, Job) {
	t.Helper()
	if _, err := e.target.Exec(t.Context(), `DROP TABLE IF EXISTS recovery_fixture;
		CREATE TABLE recovery_fixture (id bigint PRIMARY KEY, value int NOT NULL DEFAULT 0,
			CONSTRAINT stop_at_page_three CHECK(id<=4 OR value=0));
		INSERT INTO recovery_fixture(id) SELECT generate_series(1,6)`); err != nil {
		t.Fatal(err)
	}
	cfg := migration.BatchConfig{ScopeSQL: "SELECT id AS key FROM recovery_fixture", Size: 2}
	if withSnapshots {
		cfg.Recovery = &migration.BatchTable{Schema: "public", Table: "recovery_fixture", Key: "id"}
	}
	down := recoveryDownSQL
	draft, err := e.service.CreateDraftWithBatchConfig(t.Context(), e.actorID, e.dbID, "Batch plan", recoveryUpSQL, &down, nil, &cfg)
	if err != nil {
		t.Fatal(err)
	}
	review := e.submitOperation(t, draft, migration.OpApply, "", http.StatusCreated)
	if review.Mode != Batch {
		t.Fatalf("review lost batch mode: %+v", review)
	}
	queued := e.queueOperation(t, review)
	w := e.productionWorker()
	for range 5 {
		step(t, w)
	}
	job := e.job(t, queued.ID, Partial, 1)
	if !job.BatchPaused || job.CommittedBatches != 2 || job.CommittedRows != 4 {
		t.Fatalf("expected two committed pages and paused job: %+v", job)
	}
	e.checkRecoveryRows(t, 1, 1, 1, 1, 0, 0)
	return draft, job
}

func (e reviewTest) checkRecoveryRows(t *testing.T, want ...int) {
	t.Helper()
	rows, err := e.target.Query(t.Context(), "SELECT value FROM recovery_fixture ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	values, err := pgx.CollectRows(rows, pgx.RowTo[int])
	if err != nil || !slices.Equal(values, want) {
		t.Fatalf("target rows = %v, want %v: %v", values, want, err)
	}
}

func TestApprovedBatchResumeRetainsScopeAndDatabaseOwnership(t *testing.T) {
	e := setupReviewTest(t)
	draft, original := e.pauseProductionBatch(t, true)
	next, nextReview := e.draftAndReview(t)
	queuedLater := e.queueOperation(t, nextReview)
	if worked, err := e.productionWorker().Step(t.Context()); worked || err != nil {
		t.Fatalf("unrelated job overtook paused batch: %v, %v", worked, err)
	}
	if _, err := e.target.Exec(t.Context(), `ALTER TABLE recovery_fixture DROP CONSTRAINT stop_at_page_three;
		INSERT INTO recovery_fixture VALUES (7, 0)`); err != nil {
		t.Fatal(err)
	}
	review := e.submitOperation(t, draft, migration.OpBatchRecovery, original.ExecutionID, http.StatusCreated)
	resume := e.queueOperation(t, review)
	worker := e.productionWorker()
	for range 3 {
		step(t, worker)
	}
	recovered := e.job(t, resume.ID, Succeeded, 1)
	e.job(t, original.ID, Recovered, 1)
	e.job(t, queuedLater.ID, Queued, 0)
	e.checkRecoveryRows(t, 1, 1, 1, 1, 1, 1, 0)
	if recovered.CommittedBatches != 3 || recovered.CommittedRows != 6 || recovered.TargetExecutionID != original.ExecutionID {
		t.Fatalf("resume did not preserve original progress and identity: %+v", recovered)
	}
	e.runOperation(t, queuedLater)
	entries, err := e.service.ListAttemptHistory(t.Context(), e.actorID, e.dbID, false)
	if err != nil || len(entries) != 3 || entries[0].Status != migration.AttemptFailed ||
		entries[1].Operation != migration.OpBatchRecovery || entries[1].Status != migration.AttemptSucceeded ||
		entries[1].RecoveryOfExecutionID == nil || *entries[1].RecoveryOfExecutionID != original.ExecutionID ||
		entries[2].MigrationID != next.ID {
		t.Fatalf("unexpected history after approved resume: %+v (%v)", entries, err)
	}
	applied, err := e.repo.GetByID(t.Context(), e.dbID, draft.ID)
	if err != nil || applied.Status != migration.StatusApplied {
		t.Fatalf("resume did not project applied migration: %+v (%v)", applied, err)
	}
}

func TestApprovedBatchCompensationRecoversAfterReverseCommit(t *testing.T) {
	e := setupReviewTest(t)
	draft, original := e.pauseProductionBatch(t, true)
	queue := e.queueOperation(t, e.submitOperation(t, draft, migration.OpCompensation, original.ExecutionID, http.StatusCreated))
	step(t, e.productionWorker())

	crashWorkerWithServices(t, e.dbID, "commit", "production")
	e.checkRecoveryRows(t, 1, 1, 0, 0, 0, 0)
	e.expireClaim(t, queue.ID)
	step(t, e.productionWorker())
	recovered := e.job(t, queue.ID, PartiallyReverted, 1)
	if recovered.CompensatedBatches != 1 || recovered.CompensatedRows != 2 {
		t.Fatalf("compensation progress was lost: %+v", recovered)
	}
	e.submitOperation(t, draft, migration.OpBatchRecovery, original.ExecutionID, http.StatusConflict)
	step(t, e.productionWorker())
	step(t, e.productionWorker())
	done := e.job(t, queue.ID, Succeeded, 1)
	e.job(t, original.ID, Compensated, 1)
	e.checkRecoveryRows(t, 0, 0, 0, 0, 0, 0)
	if done.CompensatedBatches != 2 || done.CompensatedRows != 4 {
		t.Fatalf("compensation did not reverse all committed pages: %+v", done)
	}
	rows, err := e.target.Query(t.Context(), `SELECT sequence FROM migration_worker.compensation_checkpoints
		WHERE execution_id=$1::uuid ORDER BY sequence`, original.ExecutionID)
	if err != nil {
		t.Fatal(err)
	}
	sequences, err := pgx.CollectRows(rows, pgx.RowTo[int])
	if err != nil || !slices.Equal(sequences, []int{1, 2}) {
		t.Fatalf("unexpected compensation evidence: %v (%v)", sequences, err)
	}
	entries, err := e.service.ListAttemptHistory(t.Context(), e.actorID, e.dbID, false)
	if err != nil || len(entries) != 2 || entries[1].Operation != migration.OpCompensation ||
		entries[1].Status != migration.AttemptSucceeded || entries[1].RecoveryOfExecutionID == nil ||
		*entries[1].RecoveryOfExecutionID != original.ExecutionID {
		t.Fatalf("unexpected compensation history: %+v (%v)", entries, err)
	}
}

func TestApprovedBatchCompensationRefusesRowsChangedAfterApply(t *testing.T) {
	e := setupReviewTest(t)
	draft, original := e.pauseProductionBatch(t, true)
	if _, err := e.target.Exec(t.Context(), "UPDATE recovery_fixture SET value=9 WHERE id=4"); err != nil {
		t.Fatal(err)
	}
	queue := e.queueOperation(t, e.submitOperation(t, draft, migration.OpCompensation, original.ExecutionID, http.StatusCreated))
	for range 3 {
		step(t, e.productionWorker())
	}
	job := e.job(t, queue.ID, Partial, 1)
	if job.FailureCode != "compensation_unsafe" || !job.BatchPaused || job.CompensatedRows != 0 {
		t.Fatalf("unsafe compensation status: %+v", job)
	}
	e.checkRecoveryRows(t, 1, 1, 1, 9, 0, 0)
	e.job(t, original.ID, Partial, 1)
}

func TestApprovedBatchCompensationRequiresRecoveryMetadata(t *testing.T) {
	e := setupReviewTest(t)
	draft, original := e.pauseProductionBatch(t, false)
	e.submitOperation(t, draft, migration.OpCompensation, original.ExecutionID, http.StatusConflict)
	route := fmt.Sprintf("/api/databases/%d/migrations/%d/review?operation=compensation", e.dbID, draft.ID)
	var availability migration.OperationReview
	e.request(t, e.actorID, "GET", route, "", http.StatusOK, &availability)
	if availability.Plan != nil || availability.UnavailableReason != migration.ErrCompensationUnsafe.Error() {
		t.Fatalf("expected corrective migration guidance: %+v", availability)
	}
}

//go:build integration

package migrationjob

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/migration"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/testutil"
)

func (e reviewTest) reversibleDraft(t *testing.T, title string, change int, lockOnDown bool) migration.Migration {
	t.Helper()
	up := fmt.Sprintf("UPDATE migration_job_test_effect SET count = count + %d", change)
	down := fmt.Sprintf("UPDATE migration_job_test_effect SET count = count - %d", change)
	if lockOnDown {
		down = "LOCK TABLE migration_job_test_effect IN ACCESS EXCLUSIVE MODE NOWAIT; " + down
	}
	draft, err := e.service.CreateDraftWithDefinition(t.Context(), e.actorID, e.dbID, title, up, &down, nil)
	if err != nil {
		t.Fatal(err)
	}
	return draft
}

func (e reviewTest) submitOperation(t *testing.T, m migration.Migration, op migration.Operation, target string, want int) migration.Review {
	t.Helper()
	path := fmt.Sprintf("/api/databases/%d/migrations/%d/review", e.dbID, m.ID)
	var review migration.Review
	var result any
	if want == http.StatusCreated {
		result = &review
	}
	body := fmt.Sprintf(`{"operation":%q,"revision":%d,"target_execution_id":%q}`, op, m.Revision, target)
	e.request(t, e.actorID, "POST", path, body, want, result)
	return review
}

func (e reviewTest) queueOperation(t *testing.T, review migration.Review) migration.QueueJob {
	t.Helper()
	e.decision(t, review.RequestID, e.view, http.StatusOK, nil)
	e.decision(t, review.RequestID, e.edit, http.StatusOK, nil)
	var job migration.QueueJob
	e.request(t, e.actorID, "POST", "/api/migration-reviews/"+review.RequestID+"/enqueue", "", http.StatusAccepted, &job)
	return job
}

func (e reviewTest) productionWorker() *Worker {
	provider := &testutil.StaticProvider{Pools: map[int64]*pgxpool.Pool{e.dbID: e.target}}
	return NewApprovedWorker(e.store, provider, e.service)
}

func (e reviewTest) runOperation(t *testing.T, job migration.QueueJob) Job {
	t.Helper()
	step(t, e.productionWorker())
	step(t, e.productionWorker())
	return e.job(t, job.ID, Succeeded, 1)
}

func TestApprovedRollbackForwardAndHistory(t *testing.T) {
	e := setupReviewTest(t)
	first := e.reversibleDraft(t, "Foundation", 10, false)
	later := e.reversibleDraft(t, "Latest change", 1, true)
	a := e.runOperation(t, e.queueOperation(t, e.submitOperation(t, first, migration.OpApply, "", http.StatusCreated)))
	b := e.runOperation(t, e.queueOperation(t, e.submitOperation(t, later, migration.OpApply, "", http.StatusCreated)))
	e.effect(t, 11)

	e.submitOperation(t, first, migration.OpRollback, a.ExecutionID, http.StatusConflict)
	e.submitOperation(t, later, migration.OpForward, "", http.StatusConflict)
	e.submitOperation(t, later, migration.OpRollback, a.ExecutionID, http.StatusConflict)

	review := e.submitOperation(t, later, migration.OpRollback, b.ExecutionID, http.StatusCreated)
	if review.TargetExecutionID != b.ExecutionID {
		t.Fatalf("rollback review has wrong target: %+v", review)
	}
	rollback := e.queueOperation(t, review)
	firstTry, err := e.target.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer firstTry.Rollback(t.Context())
	if _, err := firstTry.Exec(t.Context(), "LOCK TABLE migration_job_test_effect IN ACCESS SHARE MODE"); err != nil {
		t.Fatal(err)
	}
	step(t, e.productionWorker())
	step(t, e.productionWorker())
	e.job(t, rollback.ID, Retrying, 1)
	e.effect(t, 11)
	if err := firstTry.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := e.platform.Exec(t.Context(), "UPDATE migration_async_jobs SET eligible_at=clock_timestamp() WHERE id=$1", rollback.ID); err != nil {
		t.Fatal(err)
	}
	crashWorkerWithServices(t, e.dbID, "commit", "production")
	e.effect(t, 10)
	e.expireClaim(t, rollback.ID)
	step(t, e.productionWorker())
	step(t, e.productionWorker())
	rolledBack := e.job(t, rollback.ID, Succeeded, 2)
	e.effect(t, 10)
	m, err := e.repo.GetByID(t.Context(), e.dbID, later.ID)
	if err != nil || m.Status != migration.StatusRolledBack || m.Version == nil || *m.Version != 2 {
		t.Fatalf("unexpected rolled-back definition: %+v (%v)", m, err)
	}

	e.submitOperation(t, later, migration.OpForward, b.ExecutionID, http.StatusConflict)
	forward := e.runOperation(t, e.queueOperation(t, e.submitOperation(t, later, migration.OpForward, rolledBack.ExecutionID, http.StatusCreated)))
	e.effect(t, 11)
	m, err = e.repo.GetByID(t.Context(), e.dbID, later.ID)
	if err != nil || m.Status != migration.StatusApplied || m.Version == nil || *m.Version != 2 {
		t.Fatalf("unexpected reapplied definition: %+v (%v)", m, err)
	}
	repeated := e.submitOperation(t, later, migration.OpRollback, forward.ExecutionID, http.StatusCreated)
	if repeated.RequestID == review.RequestID || repeated.TargetExecutionID != forward.ExecutionID {
		t.Fatalf("second rollback reused the earlier review: %+v", repeated)
	}

	historyPath := fmt.Sprintf("/api/databases/%d/migration-history", e.dbID)
	var full, hidden struct {
		Entries []migration.HistoryEntry `json:"entries"`
	}
	e.request(t, e.actorID, "GET", historyPath, "", http.StatusOK, &full)
	e.request(t, e.actorID, "GET", historyPath+"?hide_rollback=true", "", http.StatusOK, &hidden)
	if len(full.Entries) != 5 || len(hidden.Entries) != 3 {
		t.Fatalf("unexpected full/filtered lengths: %d/%d", len(full.Entries), len(hidden.Entries))
	}
	failure, success := full.Entries[2], full.Entries[3]
	if failure.Status != migration.AttemptFailed || failure.ExecutionID != rolledBack.ExecutionID ||
		failure.ErrorMessage == nil || *failure.ErrorMessage != "55P03" || failure.RollbackOfExecutionID != nil ||
		success.ExecutionID != rolledBack.ExecutionID || success.AttemptID == failure.AttemptID ||
		success.RollbackOfExecutionID == nil || *success.RollbackOfExecutionID != b.ExecutionID ||
		success.ExecutedSQL != *later.DownSQL || full.Entries[4].ExecutedSQL != later.SQL {
		t.Fatalf("unexpected rollback history: failure=%+v success=%+v forward=%+v", failure, success, full.Entries[4])
	}
	if hidden.Entries[0].ExecutionID != a.ExecutionID || hidden.Entries[1].AttemptID != failure.AttemptID ||
		hidden.Entries[2].ExecutionID != forward.ExecutionID {
		t.Fatalf("Hide rollback did not preserve the failed attempt: %+v", hidden.Entries)
	}
}

func TestApprovedRollbackRechecksExecutionOrder(t *testing.T) {
	e := setupReviewTest(t)
	first := e.reversibleDraft(t, "First", 10, false)
	later := e.reversibleDraft(t, "Second", 1, false)
	a := e.runOperation(t, e.queueOperation(t, e.submitOperation(t, first, migration.OpApply, "", http.StatusCreated)))
	b := e.queueOperation(t, e.submitOperation(t, later, migration.OpApply, "", http.StatusCreated))
	stale := e.queueOperation(t, e.submitOperation(t, first, migration.OpRollback, a.ExecutionID, http.StatusCreated))

	e.runOperation(t, b)
	step(t, e.productionWorker())
	failed := e.job(t, stale.ID, Failed, 0)
	if failed.FailureCode != "ineligible" {
		t.Fatalf("unexpected job after a later migration applied: %+v", failed)
	}
	e.effect(t, 11)
}

func TestLegacyHistoryUsesExecutionVersionOrder(t *testing.T) {
	e := setupReviewTest(t)
	newer := e.reversibleDraft(t, "Created first, applied second", 1, false)
	older := e.reversibleDraft(t, "Created second, applied first", 1, false)
	for i, draft := range []migration.Migration{newer, older} {
		at := time.Now().UTC()
		if _, err := e.repo.RecordExecution(t.Context(), migration.RecordExecutionParams{
			ID: draft.ID, DatabaseID: e.dbID, Version: 2 - i, Status: migration.StatusApplied,
			ExecutedBy: e.actorID, ExecutedAt: at,
		}); err != nil {
			t.Fatal(err)
		}
		if _, _, err := e.repo.AppendHistory(t.Context(), migration.AttemptResult{
			MigrationID: draft.ID, DatabaseID: e.dbID, ActorID: e.actorID, At: at,
			Revision: draft.Revision, ExecutedSQL: draft.SQL, Operation: migration.OpApply,
			Status: migration.AttemptSucceeded, ExecutionID: fmt.Sprintf("exec-%d", draft.ID), AttemptID: "a1",
		}); err != nil {
			t.Fatal(err)
		}
	}
	plan, err := e.service.PlanOperation(t.Context(), e.actorID, e.dbID, newer.ID, migration.OpRollback, newer.Revision)
	if err != nil || plan.TargetExecutionID != fmt.Sprintf("exec-%d", newer.ID) {
		t.Fatalf("legacy newest migration wasn't the rollback candidate: %+v (%v)", plan, err)
	}
}

func TestApprovedRollbackCanRequestNewReviewAfterTerminalFailure(t *testing.T) {
	e := setupReviewTest(t)
	draft := e.reversibleDraft(t, "Rollback requiring a data fix", 1, false)
	applied := e.runOperation(t, e.queueOperation(t, e.submitOperation(t, draft, migration.OpApply, "", http.StatusCreated)))
	review := e.submitOperation(t, draft, migration.OpRollback, applied.ExecutionID, http.StatusCreated)
	firstJob := e.queueOperation(t, review)

	if _, err := e.target.Exec(t.Context(), "ALTER TABLE migration_job_test_effect ADD CONSTRAINT positive_count CHECK (count > 0)"); err != nil {
		t.Fatal(err)
	}
	step(t, e.productionWorker())
	step(t, e.productionWorker())
	failed := e.job(t, firstJob.ID, Failed, 1)
	e.effect(t, 1)

	if _, err := e.target.Exec(t.Context(), "ALTER TABLE migration_job_test_effect DROP CONSTRAINT positive_count"); err != nil {
		t.Fatal(err)
	}
	newReview := e.submitOperation(t, draft, migration.OpRollback, applied.ExecutionID, http.StatusCreated)
	if newReview.RequestID == review.RequestID || newReview.Status != migration.ReviewPending {
		t.Fatalf("expected a new review after failed rollback: %+v", newReview)
	}
	second := e.runOperation(t, e.queueOperation(t, newReview))
	if second.ExecutionID == failed.ExecutionID {
		t.Fatal("new review reused the failed execution identity")
	}
	e.effect(t, 0)
	entries, err := e.service.ListAttemptHistory(t.Context(), e.actorID, e.dbID, true)
	if err != nil || len(entries) != 1 || entries[0].ExecutionID != failed.ExecutionID || entries[0].Status != migration.AttemptFailed {
		t.Fatalf("failed rollback must remain visible after the new execution: %+v (%v)", entries, err)
	}
}

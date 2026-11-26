//go:build integration

package migrationjob

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/access"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/migration"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/testutil"
)

type reviewTest struct {
	workerTest
	reviews *migration.PostgresApprovalRepository
	repo    *migration.PostgresRepository
	service *migration.Service
	queue   *ReviewQueue
	mux     *http.ServeMux
	leader  int64
	edit    int64
	view    int64
}

func setupReviewTest(t *testing.T) reviewTest {
	t.Helper()
	e := setupWorkerTest(t)
	leader := testutil.SeedUser(t, e.platform, "shared-leader", false, true)
	edit := testutil.SeedGroup(t, e.platform, "editors")
	view := testutil.SeedGroup(t, e.platform, "viewers")
	testutil.SeedUserGroup(t, e.platform, e.actorID, edit)
	testutil.SeedUserGroup(t, e.platform, leader, edit)
	testutil.SeedUserGroup(t, e.platform, leader, view)
	testutil.SeedGroupGrant(t, e.platform, edit, e.dbID, "edit")
	testutil.SeedGroupGrant(t, e.platform, view, e.dbID, "view")
	if _, err := e.platform.Exec(t.Context(), "UPDATE groups SET leader_user_id = $1", leader); err != nil {
		t.Fatal(err)
	}

	reviews := migration.NewPostgresApprovalRepository(e.platform)
	repo := migration.NewPostgresRepository(e.platform)
	checker := access.NewPostgresChecker(e.platform)
	service := migration.NewServiceWithApprovals(repo, nil, checker, reviews)
	queue := NewReviewQueue(e.store, reviews)
	mux := http.NewServeMux()
	migration.NewQueuedHandler(service, queue).RegisterRoutes(mux)
	NewHandler(e.store, checker).RegisterRoutes(mux)
	return reviewTest{workerTest: e, reviews: reviews, repo: repo, service: service, queue: queue,
		mux: mux, leader: leader, edit: edit, view: view}
}

func (e reviewTest) request(t *testing.T, actor int64, method, path, body string, wantStatus int, result any) {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r = r.WithContext(access.ContextWithUser(r.Context(), access.User{ID: actor}))
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	e.mux.ServeHTTP(w, r)
	if w.Code != wantStatus {
		t.Fatalf("%s %s: got %d, want %d: %s", method, path, w.Code, wantStatus, w.Body)
	}
	if result != nil {
		if err := json.Unmarshal(w.Body.Bytes(), result); err != nil {
			t.Fatalf("decode %s: %v", w.Body, err)
		}
	}
}

func (e reviewTest) draftAndReview(t *testing.T) (migration.Migration, migration.Review) {
	t.Helper()
	draft, err := e.service.CreateDraft(t.Context(), e.actorID, e.dbID, "Increment counter", incrementSQL)
	if err != nil {
		t.Fatal(err)
	}
	var review migration.Review
	path := fmt.Sprintf("/api/databases/%d/migrations/%d/review", e.dbID, draft.ID)
	e.request(t, e.actorID, "POST", path, `{"operation":"apply"}`, http.StatusCreated, &review)
	return draft, review
}

func (e reviewTest) decision(t *testing.T, requestID string, groupID int64, status int, result any) {
	t.Helper()
	body := fmt.Sprintf(`{"group_id":%d,"decision":"approved"}`, groupID)
	e.request(t, e.leader, "POST", "/api/migration-reviews/"+requestID+"/decision", body, status, result)
}

func TestApprovedReviewDispatchesToLiveWorker(t *testing.T) {
	e := setupReviewTest(t)
	draft, review := e.draftAndReview(t)
	reviewPath := "/api/migration-reviews/" + review.RequestID
	var duplicate migration.Review
	e.request(t, e.actorID, "POST", fmt.Sprintf("/api/databases/%d/migrations/%d/review", e.dbID, draft.ID),
		`{"operation":"apply"}`, http.StatusCreated, &duplicate)
	if duplicate.RequestID != review.RequestID {
		t.Fatalf("repeat submission returned a different review: %s", duplicate.RequestID)
	}
	e.request(t, e.actorID, "POST", reviewPath+"/enqueue", "", http.StatusConflict, nil)
	e.request(t, e.actorID, "POST", reviewPath+"/decision", `{"decision":"approved"}`, http.StatusForbidden, nil)
	e.decision(t, review.RequestID, 0, http.StatusBadRequest, nil)
	e.decision(t, review.RequestID, e.view, http.StatusOK, &review)
	if review.Status != migration.ReviewPending || review.JobID != nil {
		t.Fatalf("one group must leave the review pending: %+v", review)
	}
	e.decision(t, review.RequestID, e.edit, http.StatusOK, &review)
	if review.Status != migration.ReviewApproved || review.JobID != nil {
		t.Fatalf("expected durable approval before dispatch: %+v", review)
	}

	// Simulate an approval previously enqueued by the legacy API before
	// startup. Its legacy ID isn't a worker job ID.
	_, err := e.platform.Exec(t.Context(), "INSERT INTO migration_queue_jobs (id, request_id) VALUES (1000, $1)", review.RequestID)
	if err == nil {
		_, err = e.platform.Exec(t.Context(), "UPDATE migration_review_requests SET enqueued_at = now() WHERE request_id = $1", review.RequestID)
	}
	if err != nil {
		t.Fatal(err)
	}
	e.effect(t, 0)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	provider := &testutil.StaticProvider{Pools: map[int64]*pgxpool.Pool{e.dbID: e.target}}
	dispatchDone, workerDone := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(dispatchDone)
		NewReviewQueue(e.store, e.reviews).Run(ctx)
	}()
	go func() {
		defer close(workerDone)
		NewApprovedWorker(e.store, provider, e.service).Run(ctx)
	}()
	defer func() {
		cancel()
		<-dispatchDone
		<-workerDone
	}()

	for review.JobID == nil {
		review, err = e.reviews.GetReview(ctx, review.RequestID)
		if err != nil {
			t.Fatal(err)
		}
		select {
		case <-ctx.Done():
			t.Fatal("approved review was not rediscovered")
		case <-time.After(10 * time.Millisecond):
		}
	}
	jobID := *review.JobID
	if jobID == 1000 {
		t.Fatal("approval was linked to a legacy job")
	}
	statusPath := fmt.Sprintf("/api/databases/%d/jobs/%d", e.dbID, jobID)
	for {
		var job Job
		e.request(t, e.actorID, "GET", statusPath, "", http.StatusOK, &job)
		if job.State == Succeeded {
			if job.Attempts != 1 {
				t.Fatalf("expected one attempt, got %+v", job)
			}
			break
		}
		if job.State == Failed {
			t.Fatalf("worker failed: %+v", job)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("worker did not finish: %+v", job)
		case <-time.After(10 * time.Millisecond):
		}
	}
	var queued migration.QueueJob
	e.request(t, e.actorID, "POST", reviewPath+"/enqueue", "", http.StatusAccepted, &queued)
	if queued.ID != jobID || queued.DatabaseID != e.dbID || queued.Status != string(Succeeded) {
		t.Fatalf("repeat enqueue returned a different job: %+v", queued)
	}
	var legacyExecute struct {
		migration.Migration
		JobID int64 `json:"job_id"`
	}
	e.request(t, e.actorID, "POST", fmt.Sprintf("/api/databases/%d/migrations/%d/execute", e.dbID, draft.ID),
		`{"confirm":true}`, http.StatusAccepted, &legacyExecute)
	if legacyExecute.JobID != jobID || legacyExecute.ID != draft.ID {
		t.Fatalf("execute route returned a different job: %+v", legacyExecute)
	}
	e.effect(t, 1)
	history, err := e.service.ListAttemptHistory(t.Context(), e.actorID, e.dbID, false)
	if err != nil || len(history) != 1 || history[0].Status != migration.AttemptSucceeded ||
		history[0].ActorID != e.actorID || history[0].ExecutedSQL != incrementSQL || history[0].Revision != draft.Revision {
		t.Fatalf("unexpected real attempt history: %+v (%v)", history, err)
	}
}

type editBeforeDefinitionLock struct {
	*migration.PostgresRepository
	draft migration.Migration
}

func (r editBeforeDefinitionLock) LockDefinition(ctx context.Context, databaseID, migrationID int64, revision int) error {
	if _, err := r.UpdateDraft(ctx, migration.UpdateDraftParams{
		ID: migrationID, DatabaseID: databaseID, Title: r.draft.Title,
		SQL: "UPDATE migration_job_test_effect SET count = count + 2",
	}); err != nil {
		return err
	}
	return r.PostgresRepository.LockDefinition(ctx, databaseID, migrationID, revision)
}

func TestApprovedJobRedispatchAndRevisionRace(t *testing.T) {
	e := setupReviewTest(t)
	draft, review := e.draftAndReview(t)
	e.decision(t, review.RequestID, e.view, http.StatusOK, nil)
	e.decision(t, review.RequestID, e.edit, http.StatusOK, &review)

	// Persist the worker job without acknowledging dispatch, then retry
	// discovery with a fresh adapter. The ID must survive the gap.
	job, err := e.store.enqueue(t.Context(), approvalFromReview(review))
	if err != nil {
		t.Fatal(err)
	}
	fresh := NewReviewQueue(e.store, e.reviews)
	if err := fresh.dispatcher.DispatchPending(t.Context()); err != nil {
		t.Fatal(err)
	}
	linked, err := e.reviews.GetReview(t.Context(), review.RequestID)
	if err != nil || linked.JobID == nil || *linked.JobID != job.ID {
		t.Fatalf("dispatch acknowledgment was not recovered: %+v (%v)", linked, err)
	}

	repo := editBeforeDefinitionLock{PostgresRepository: e.repo, draft: draft}
	service := migration.NewServiceWithApprovals(repo, nil, access.NewPostgresChecker(e.platform), e.reviews)
	provider := &testutil.StaticProvider{Pools: map[int64]*pgxpool.Pool{e.dbID: e.target}}
	step(t, NewApprovedWorker(e.store, provider, service))
	failed := e.job(t, job.ID, Failed, 0)
	if failed.FailureCode != "ineligible" {
		t.Fatalf("expected stale revision rejection, got %+v", failed)
	}
	e.effect(t, 0)
	current, err := e.repo.GetByID(t.Context(), e.dbID, draft.ID)
	if err != nil || current.Revision != draft.Revision+1 || current.FirstExecutedAt != nil {
		t.Fatalf("the edited revision should remain unlocked: %+v (%v)", current, err)
	}
}

func TestApprovedJobRechecksCurrentGroupGrants(t *testing.T) {
	e := setupReviewTest(t)
	_, review := e.draftAndReview(t)
	e.decision(t, review.RequestID, e.view, http.StatusOK, nil)
	e.decision(t, review.RequestID, e.edit, http.StatusOK, &review)

	var job migration.QueueJob
	enqueuePath := "/api/migration-reviews/" + review.RequestID + "/enqueue"
	e.request(t, e.actorID, "POST", enqueuePath, "", http.StatusAccepted, &job)
	e.job(t, job.ID, Queued, 0)
	e.effect(t, 0)

	extraGroup := testutil.SeedGroup(t, e.platform, "new-viewer-group")
	testutil.SeedGroupGrant(t, e.platform, extraGroup, e.dbID, "view")
	testutil.SeedUserGroup(t, e.platform, e.leader, extraGroup)
	if _, err := e.platform.Exec(t.Context(), "UPDATE groups SET leader_user_id=$1 WHERE id=$2", e.leader, extraGroup); err != nil {
		t.Fatal(err)
	}

	provider := &testutil.StaticProvider{Pools: map[int64]*pgxpool.Pool{e.dbID: e.target}}
	step(t, NewApprovedWorker(e.store, provider, e.service))
	failed := e.job(t, job.ID, Failed, 0)
	if failed.FailureCode != "ineligible" {
		t.Fatalf("expected rejection after the group requirements changed: %+v", failed)
	}
	e.effect(t, 0)
}

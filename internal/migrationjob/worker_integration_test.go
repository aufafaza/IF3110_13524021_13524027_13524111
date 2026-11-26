//go:build integration

package migrationjob

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/access"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/migration"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/platformdb"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/testutil"
)

const incrementSQL = "UPDATE migration_job_test_effect SET count = count + 1"

type fixtureServices struct {
	pool *pgxpool.Pool
}

func (f fixtureServices) PendingDispatch(ctx context.Context) ([]Approval, error) {
	rows, err := f.pool.Query(ctx, `SELECT id, actor_id, database_id, mode FROM fixture_approvals WHERE job_id IS NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var requests []Approval
	for rows.Next() {
		request := Approval{MigrationID: 1, Revision: "revision-1", Operation: Apply, Traceparent: "fixture-trace"}
		if err := rows.Scan(&request.RequestID, &request.ActorID, &request.DatabaseID, &request.Mode); err != nil {
			return nil, err
		}
		requests = append(requests, request)
	}
	return requests, rows.Err()
}

func (f fixtureServices) AcknowledgeDispatch(ctx context.Context, requestID string, jobID int64) error {
	_, err := f.pool.Exec(ctx, `UPDATE fixture_approvals SET job_id = $2 WHERE id = $1`, requestID, jobID)
	return err
}

func (f fixtureServices) Recheck(ctx context.Context, a Approval) error {
	var approved bool
	if err := f.pool.QueryRow(ctx, `SELECT approved FROM fixture_approvals WHERE id = $1`, a.RequestID).Scan(&approved); err != nil {
		return err
	}
	if !approved {
		return ErrIneligible
	}
	return nil
}

func (f fixtureServices) ResolvePlan(ctx context.Context, a Approval) (migration.Plan, error) {
	var sql string
	err := f.pool.QueryRow(ctx, `SELECT sql FROM fixture_approvals WHERE id = $1`, a.RequestID).Scan(&sql)
	return migration.Plan{SQL: sql}, err
}

func (f fixtureServices) ResolveBatch(ctx context.Context, a Approval) (BatchPlan, error) {
	var plan BatchPlan
	err := f.pool.QueryRow(ctx, `SELECT batch_plan FROM fixture_approvals WHERE id = $1`, a.RequestID).Scan(&plan)
	return plan, err
}

func (f fixtureServices) Record(ctx context.Context, outcome Outcome) error {
	payload, err := json.Marshal(outcome)
	if err != nil {
		return err
	}
	tag, err := f.pool.Exec(ctx, `INSERT INTO fixture_history (execution_id, attempt_id, payload)
		VALUES ($1, $2, $3) ON CONFLICT (execution_id, attempt_id)
		DO UPDATE SET deliveries = fixture_history.deliveries + 1 WHERE fixture_history.payload = excluded.payload`,
		outcome.ExecutionID, outcome.AttemptID, payload)
	if err == nil && tag.RowsAffected() != 1 {
		return errors.New("outcome changed on redelivery")
	}
	return err
}

type workerTest struct {
	platform *pgxpool.Pool
	target   *pgxpool.Pool
	fixtures fixtureServices
	store    *Store
	dbID     int64
	actorID  int64
}

func openWorkerTarget(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("MIGRATION_TEST_TARGET_URL")
	if dsn == "" {
		dsn = testutil.TestDatabaseDSN(t)
	}
	pool, err := platformdb.Open(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func setupWorkerTest(t *testing.T) workerTest {
	t.Helper()
	platform := testutil.OpenPlatformTestPool(t)
	target := openWorkerTarget(t)
	setup := []struct {
		pool *pgxpool.Pool
		sql  string
	}{
		{platform, `CREATE TABLE fixture_approvals (
			id text PRIMARY KEY, actor_id bigint, database_id bigint, job_id bigint,
			approved boolean NOT NULL DEFAULT true, sql text NOT NULL,
			mode text NOT NULL DEFAULT 'ordinary', batch_plan jsonb
		);
		CREATE TABLE fixture_history (
			execution_id text, attempt_id text, payload jsonb, deliveries integer NOT NULL DEFAULT 1,
			PRIMARY KEY (execution_id, attempt_id)
		);`},
		{target, `DROP SCHEMA IF EXISTS migration_worker CASCADE;
			DROP TABLE IF EXISTS migration_job_test_effect;
			CREATE TABLE migration_job_test_effect (count integer);
			INSERT INTO migration_job_test_effect VALUES (0)`},
	}
	for _, item := range setup {
		if _, err := item.pool.Exec(t.Context(), item.sql); err != nil {
			t.Fatal(err)
		}
	}
	for _, filename := range []string{"0001_async_migration_evidence.sql", "0002_async_migration_batches.sql"} {
		provision, err := os.ReadFile("../../migrations/target/" + filename)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := target.Exec(t.Context(), string(provision)); err != nil {
			t.Fatal(err)
		}
	}
	return workerTest{
		platform: platform, target: target, fixtures: fixtureServices{platform}, store: NewStore(platform),
		dbID:    testutil.SeedManagedDatabase(t, platform, "orders"),
		actorID: testutil.SeedUser(t, platform, "editor", false, true),
	}
}

func (e workerTest) approve(t *testing.T, requestID, sql string) {
	t.Helper()
	_, err := e.platform.Exec(t.Context(), `INSERT INTO fixture_approvals (id, actor_id, database_id, sql)
		VALUES ($1, $2, $3, $4)`, requestID, e.actorID, e.dbID, sql)
	if err != nil {
		t.Fatal(err)
	}
}

func (e workerTest) worker() *Worker {
	provider := &testutil.StaticProvider{Pools: map[int64]*pgxpool.Pool{e.dbID: e.target}}
	return NewWorker(e.store, provider, e.fixtures, e.fixtures, e.fixtures)
}

func (e workerTest) effect(t *testing.T, want int) {
	t.Helper()
	var got int
	if err := e.target.QueryRow(t.Context(), "SELECT count FROM migration_job_test_effect").Scan(&got); err != nil || got != want {
		t.Fatalf("effect count = %d, want %d (err = %v)", got, want, err)
	}
}

func (e workerTest) job(t *testing.T, id int64, state State, attempts int) Job {
	t.Helper()
	job, err := e.store.Get(t.Context(), e.dbID, id)
	if err != nil || job.State != state || job.Attempts != attempts {
		t.Fatalf("job = %+v, want %s with %d attempts (err = %v)", job, state, attempts, err)
	}
	return job
}

func (e workerTest) expireClaim(t *testing.T, id int64) {
	t.Helper()
	tag, err := e.platform.Exec(t.Context(), `UPDATE migration_async_jobs SET lease_until = clock_timestamp()
		WHERE id = $1 AND lease_until IS NOT NULL`, id)
	if err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("expire abandoned worker's claim: %v", err)
	}
}

func step(t *testing.T, w *Worker) {
	t.Helper()
	if worked, err := w.Step(t.Context()); !worked || err != nil {
		t.Fatalf("worker step = %v, %v", worked, err)
	}
}

func crashWorker(t *testing.T, databaseID int64, at string) {
	t.Helper()
	crashWorkerWithServices(t, databaseID, at, "fixtures")
}

func crashWorkerWithServices(t *testing.T, databaseID int64, at, services string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestWorkerCrashProcess$")
	cmd.Env = append(os.Environ(), "MIGRATION_TEST_CRASH_AT="+at, "MIGRATION_TEST_SERVICES="+services, fmt.Sprintf("MIGRATION_TEST_DB_ID=%d", databaseID))
	output, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 42 {
		t.Fatalf("worker did not crash at %s: %v\n%s", at, err, output)
	}
}

func TestWorkerCrashProcess(t *testing.T) {
	at := os.Getenv("MIGRATION_TEST_CRASH_AT")
	if at == "" {
		t.Skip("worker subprocess helper")
	}
	platform, err := platformdb.Open(t.Context(), testutil.TestDatabaseDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	defer platform.Close()
	e := workerTest{platform: platform, target: openWorkerTarget(t), fixtures: fixtureServices{platform}, store: NewStore(platform)}
	if _, err := fmt.Sscan(os.Getenv("MIGRATION_TEST_DB_ID"), &e.dbID); err != nil {
		t.Fatal(err)
	}
	w := e.worker()
	if os.Getenv("MIGRATION_TEST_SERVICES") == "production" {
		reviews := migration.NewPostgresApprovalRepository(platform)
		repo := migration.NewPostgresRepository(platform)
		service := migration.NewServiceWithApprovals(repo, nil, access.NewPostgresChecker(platform), reviews)
		provider := &testutil.StaticProvider{Pools: map[int64]*pgxpool.Pool{e.dbID: e.target}}
		w = NewApprovedWorker(e.store, provider, service)
	}
	crash := func() { os.Exit(42) }
	switch at {
	case "claim":
		w.afterClaim = crash
	case "commit":
		w.afterCommit = crash
	case "history":
		w.afterHistoryAck = crash
	default:
		t.Fatalf("unknown crash boundary %q", at)
	}
	step(t, w)
	t.Fatal("crash hook was not reached")
}

func TestOrdinaryMigrationRecoversAfterCommit(t *testing.T) {
	e := setupWorkerTest(t)
	e.approve(t, "approval", incrementSQL)
	dispatcher := NewDispatcher(e.store, e.fixtures)
	requests, err := e.fixtures.PendingDispatch(t.Context())
	if err != nil || len(requests) != 1 {
		t.Fatalf("approval was not discoverable before enqueue: %v, %v", requests, err)
	}
	// Simulate restart after approval committed and before initial dispatch.
	if err := dispatcher.DispatchPending(t.Context()); err != nil {
		t.Fatal(err)
	}
	job, err := dispatcher.Dispatch(t.Context(), requests[0])
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := dispatcher.Dispatch(t.Context(), requests[0])
	if err != nil || replayed.ID != job.ID || replayed.ExecutionID != job.ExecutionID {
		t.Fatalf("dispatch changed job identity: %+v, %v", replayed, err)
	}
	conflict := requests[0]
	conflict.Revision = "revision-2"
	if _, err := dispatcher.Dispatch(t.Context(), conflict); !errors.Is(err, ErrApprovalMismatch) {
		t.Fatalf("dispatch with conflicting revision: %v", err)
	}
	e.job(t, job.ID, Queued, 0)
	e.effect(t, 0)

	crashWorker(t, e.dbID, "claim")
	e.job(t, job.ID, Running, 0)
	e.effect(t, 0)
	e.expireClaim(t, job.ID)

	crashWorker(t, e.dbID, "commit")
	e.effect(t, 1)
	e.job(t, job.ID, Running, 1)

	var committedAttempt string
	err = e.target.QueryRow(t.Context(), `SELECT attempt_id::text FROM migration_worker.completions
		WHERE execution_id = $1::uuid`, job.ExecutionID).Scan(&committedAttempt)
	if err != nil {
		t.Fatal(err)
	}
	assertHistory := func(wantRows, wantDeliveries int) {
		t.Helper()
		var count, deliveries int
		err := e.platform.QueryRow(t.Context(), `SELECT count(*), coalesce(sum(deliveries), 0)
			FROM fixture_history WHERE execution_id = $1 AND attempt_id = $2
			AND payload->>'FailureCode' = '' AND payload->>'SQL' = $3`,
			job.ExecutionID, committedAttempt, incrementSQL).Scan(&count, &deliveries)
		if err != nil || count != wantRows || deliveries != wantDeliveries {
			t.Fatalf("history = (%d rows, %d deliveries), want (%d, %d): %v", count, deliveries, wantRows, wantDeliveries, err)
		}
	}
	assertHistory(0, 0)

	// Later jobs on this database cannot overtake the job with an uncertain
	// commit, even though its worker process is already gone.
	e.approve(t, "later", incrementSQL)
	if err := dispatcher.DispatchPending(t.Context()); err != nil {
		t.Fatal(err)
	}
	if worked, err := e.worker().Step(t.Context()); worked || err != nil {
		t.Fatalf("claimed another job while the database was owned: %v, %v", worked, err)
	}
	e.expireClaim(t, job.ID)
	step(t, e.worker())
	e.job(t, job.ID, Reporting, 1)
	e.effect(t, 1)
	assertHistory(0, 0)

	crashWorker(t, e.dbID, "history")
	e.job(t, job.ID, Reporting, 1)
	assertHistory(1, 1)
	e.expireClaim(t, job.ID)
	step(t, e.worker())
	e.job(t, job.ID, Succeeded, 1)
	e.effect(t, 1)
	assertHistory(1, 2)

	var attempts, entries int
	err = e.platform.QueryRow(t.Context(), `SELECT
		(SELECT count(*) FROM migration_async_attempts WHERE job_id = $1),
		(SELECT count(*) FROM fixture_history WHERE execution_id = $2)`, job.ID, job.ExecutionID).Scan(&attempts, &entries)
	if err != nil || attempts != 1 || entries != 1 {
		t.Fatalf("final state = %d attempts and %d history entries, want one of each: %v", attempts, entries, err)
	}
}

func TestOrdinaryMigrationJobStatusChecksDatabaseAccess(t *testing.T) {
	e := setupWorkerTest(t)
	e.approve(t, "status", incrementSQL)
	dispatcher := NewDispatcher(e.store, e.fixtures)
	if err := dispatcher.DispatchPending(t.Context()); err != nil {
		t.Fatal(err)
	}
	var id int64
	if err := e.platform.QueryRow(t.Context(), "SELECT job_id FROM fixture_approvals").Scan(&id); err != nil {
		t.Fatal(err)
	}
	otherDB := testutil.SeedManagedDatabase(t, e.platform, "other")
	viewer := testutil.SeedUser(t, e.platform, "viewer", false, true)
	group := testutil.SeedGroup(t, e.platform, "viewers")
	testutil.SeedUserGroup(t, e.platform, viewer, group)
	testutil.SeedGroupGrant(t, e.platform, group, e.dbID, "view")
	testutil.SeedGroupGrant(t, e.platform, group, otherDB, "view")

	mux := http.NewServeMux()
	NewHandler(e.store, access.NewPostgresChecker(e.platform)).RegisterRoutes(mux)
	for _, tc := range []struct {
		user, db int64
		status   int
	}{
		{0, e.dbID, http.StatusUnauthorized},
		{e.actorID, e.dbID, http.StatusForbidden},
		{viewer, otherDB, http.StatusNotFound},
		{viewer, e.dbID, http.StatusOK},
	} {
		r := httptest.NewRequest("GET", fmt.Sprintf("/api/databases/%d/jobs/%d", tc.db, id), nil)
		if tc.user != 0 {
			r = r.WithContext(access.ContextWithUser(r.Context(), access.User{ID: tc.user}))
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("status for user %d database %d = %d, want %d: %s", tc.user, tc.db, w.Code, tc.status, w.Body)
		}
		if w.Code == http.StatusOK {
			var job Job
			if err := json.Unmarshal(w.Body.Bytes(), &job); err != nil || job.ID != id || job.State != Queued {
				t.Fatalf("unexpected job status response: %s (%v)", w.Body, err)
			}
		}
	}
}

func TestOrdinaryMigrationRetriesRolledBackAttempts(t *testing.T) {
	e := setupWorkerTest(t)
	e.approve(t, "retry", incrementSQL+`; DO $$
		BEGIN RAISE EXCEPTION USING ERRCODE = '40001', MESSAGE = 'force serialization failure';
		END $$;`)
	if err := NewDispatcher(e.store, e.fixtures).DispatchPending(t.Context()); err != nil {
		t.Fatal(err)
	}
	var jobID int64
	if err := e.platform.QueryRow(t.Context(), "SELECT job_id FROM fixture_approvals").Scan(&jobID); err != nil {
		t.Fatal(err)
	}

	w := e.worker()
	for attempt := 1; attempt <= 3; attempt++ {
		step(t, w)
		e.job(t, jobID, Reporting, attempt)
		step(t, w)
		if attempt == 3 {
			break
		}
		e.job(t, jobID, Retrying, attempt)
		var delayed bool
		err := e.platform.QueryRow(t.Context(), `SELECT eligible_at > updated_at
			FROM migration_async_jobs WHERE id = $1`, jobID).Scan(&delayed)
		if err != nil || !delayed {
			t.Fatalf("retry was not delayed: %v", err)
		}
		if _, err := e.platform.Exec(t.Context(), `UPDATE migration_async_jobs SET eligible_at = clock_timestamp()
			WHERE id = $1`, jobID); err != nil {
			t.Fatal(err)
		}
	}

	job := e.job(t, jobID, Failed, 3)
	e.effect(t, 0)
	var history int
	err := e.platform.QueryRow(t.Context(), `SELECT count(*) FROM fixture_history
		WHERE execution_id = $1 AND payload->>'FailureCode' = '40001'`, job.ExecutionID).Scan(&history)
	if err != nil || history != 3 {
		t.Fatalf("history = %d, want one outcome per attempt: %v", history, err)
	}
}

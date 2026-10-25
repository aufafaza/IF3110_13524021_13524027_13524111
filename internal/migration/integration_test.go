//go:build integration

package migration_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/migration"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/testutil"
)

type integrationEnv struct {
	platformPool *pgxpool.Pool
	targetPool   *pgxpool.Pool
	dbID         int64
	noGrantUser  int64
	adminNoGroup int64
	viewUser     int64
	editUser     int64
	multiGrpUser int64
	service      *migration.Service
}

func setupIntegrationEnv(t *testing.T) *integrationEnv {
	t.Helper()
	platformPool := testutil.OpenPlatformTestPool(t)
	targetPool := testutil.OpenIsolatedSchemaPool(t, "target_app_schema")

	dbID := testutil.SeedManagedDatabase(t, platformPool, "orders-prod")

	noGrantUser := testutil.SeedUser(t, platformPool, "outsider", false, true)
	adminNoGroup := testutil.SeedUser(t, platformPool, "platform_admin_only", true, true)
	viewUser := testutil.SeedUser(t, platformPool, "viewer_bob", false, true)
	editUser := testutil.SeedUser(t, platformPool, "editor_alice", false, true)
	multiGrpUser := testutil.SeedUser(t, platformPool, "multi_group_charlie", false, true)

	viewGroup := testutil.SeedGroup(t, platformPool, "analysts_view")
	editGroup := testutil.SeedGroup(t, platformPool, "developers_edit")

	testutil.SeedGroupGrant(t, platformPool, viewGroup, dbID, "view")
	testutil.SeedGroupGrant(t, platformPool, editGroup, dbID, "edit")

	testutil.SeedUserGroup(t, platformPool, viewUser, viewGroup)
	testutil.SeedUserGroup(t, platformPool, editUser, editGroup)
	testutil.SeedUserGroup(t, platformPool, multiGrpUser, viewGroup)
	testutil.SeedUserGroup(t, platformPool, multiGrpUser, editGroup)

	provider := &testutil.StaticProvider{
		Pools: map[int64]*pgxpool.Pool{
			dbID: targetPool,
		},
	}

	repo := migration.NewPostgresRepository(platformPool)
	locker := migration.NewTargetPostgresLocker(provider)
	executor := migration.NewTargetPostgresExecutor(provider)
	checker := migration.NewPostgresAccessChecker(platformPool)
	runner := migration.NewRunner(repo, locker, executor)
	svc := migration.NewService(repo, runner, checker)

	return &integrationEnv{
		platformPool: platformPool,
		targetPool:   targetPool,
		dbID:         dbID,
		noGrantUser:  noGrantUser,
		adminNoGroup: adminNoGroup,
		viewUser:     viewUser,
		editUser:     editUser,
		multiGrpUser: multiGrpUser,
		service:      svc,
	}
}

func TestIntegrationMigrationSuccessAndRollback(t *testing.T) {
	ctx := context.Background()
	env := setupIntegrationEnv(t)

	draft1, err := env.service.CreateDraft(ctx, env.editUser, env.dbID, "Create customers table", `
		CREATE TABLE customers (
			id BIGSERIAL PRIMARY KEY,
			email TEXT NOT NULL UNIQUE
		);
		INSERT INTO customers (email) VALUES ('a@example.com'), ('b@example.com');
	`)
	if err != nil {
		t.Fatalf("CreateDraft 1: %v", err)
	}
	if draft1.Version != nil || draft1.Status != migration.StatusDraft {
		t.Fatalf("expected unversioned draft, got %+v", draft1)
	}

	applied1, err := env.service.Execute(ctx, env.editUser, env.dbID, draft1.ID)
	if err != nil {
		t.Fatalf("Execute 1 failed: %v", err)
	}
	if applied1.Status != migration.StatusApplied || applied1.Version == nil || *applied1.Version != 1 {
		t.Fatalf("applied1 = %+v, want status=applied version=1", applied1)
	}
	if applied1.CreatedByUsername != "editor_alice" {
		t.Errorf("CreatedByUsername = %q, want editor_alice", applied1.CreatedByUsername)
	}
	if applied1.ExecutedByUsername == nil || *applied1.ExecutedByUsername != "editor_alice" {
		t.Errorf("ExecutedByUsername = %v, want editor_alice", applied1.ExecutedByUsername)
	}

	var count int
	if err := env.targetPool.QueryRow(ctx, "SELECT count(*) FROM customers").Scan(&count); err != nil {
		t.Fatalf("query target customers: %v", err)
	}
	if count != 2 {
		t.Fatalf("customers count = %d, want 2", count)
	}

	draft2, err := env.service.CreateDraft(ctx, env.editUser, env.dbID, "Broken migration mid-flight", `
		CREATE TABLE invoices_should_rollback (
			id BIGSERIAL PRIMARY KEY,
			amount INT NOT NULL
		);
		INSERT INTO customers (email) VALUES ('c@example.com');
		INSERT INTO non_existent_table_xyz (id) VALUES (999);
	`)
	if err != nil {
		t.Fatalf("CreateDraft 2: %v", err)
	}

	failed2, execErr := env.service.Execute(ctx, env.editUser, env.dbID, draft2.ID)
	if execErr == nil {
		t.Fatal("expected error when executing broken migration")
	}
	if failed2.Status != migration.StatusFailed || failed2.Version == nil || *failed2.Version != 2 {
		t.Fatalf("failed2 = %+v, want status=failed version=2", failed2)
	}
	if failed2.ErrorMessage == nil || *failed2.ErrorMessage == "" {
		t.Fatalf("expected non-empty error_message on failed migration")
	}

	var invoicesExists bool
	if err := env.targetPool.QueryRow(ctx, "SELECT to_regclass('invoices_should_rollback') IS NOT NULL").Scan(&invoicesExists); err != nil {
		t.Fatalf("check invoices_should_rollback: %v", err)
	}
	if invoicesExists {
		t.Fatal("invoices_should_rollback table exists; failed transaction was not rolled back!")
	}
	if err := env.targetPool.QueryRow(ctx, "SELECT count(*) FROM customers").Scan(&count); err != nil {
		t.Fatalf("re-count customers: %v", err)
	}
	if count != 2 {
		t.Fatalf("customers count after rollback = %d, want 2", count)
	}

	history, err := env.service.ListHistory(ctx, env.viewUser, env.dbID)
	if err != nil {
		t.Fatalf("ListHistory as viewUser: %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("history len = %d, want 2", len(history))
	}
	if *history[0].Version != 1 || history[0].Status != migration.StatusApplied {
		t.Errorf("history[0] = %+v, want v1 applied", history[0])
	}
	if *history[1].Version != 2 || history[1].Status != migration.StatusFailed {
		t.Errorf("history[1] = %+v, want v2 failed", history[1])
	}
}

func TestIntegrationAccessControlEnforcement(t *testing.T) {
	ctx := context.Background()
	env := setupIntegrationEnv(t)

	if _, err := env.service.ListHistory(ctx, env.noGrantUser, env.dbID); !errors.Is(err, migration.ErrForbidden) {
		t.Fatalf("noGrantUser ListHistory: got %v, want ErrForbidden", err)
	}
	if _, err := env.service.CreateDraft(ctx, env.noGrantUser, env.dbID, "T", "SELECT 1;"); !errors.Is(err, migration.ErrForbidden) {
		t.Fatalf("noGrantUser CreateDraft: got %v, want ErrForbidden", err)
	}

	if _, err := env.service.ListHistory(ctx, env.adminNoGroup, env.dbID); !errors.Is(err, migration.ErrForbidden) {
		t.Fatalf("adminNoGroup ListHistory: got %v, want ErrForbidden", err)
	}
	if _, err := env.service.CreateDraft(ctx, env.adminNoGroup, env.dbID, "T", "SELECT 1;"); !errors.Is(err, migration.ErrForbidden) {
		t.Fatalf("adminNoGroup CreateDraft: got %v, want ErrForbidden", err)
	}

	draft, err := env.service.CreateDraft(ctx, env.multiGrpUser, env.dbID, "Add audit table", "CREATE TABLE audit (id int);")
	if err != nil {
		t.Fatalf("multiGrpUser CreateDraft: %v", err)
	}

	if _, err := env.service.GetDetail(ctx, env.viewUser, env.dbID, draft.ID); err != nil {
		t.Fatalf("viewUser GetDetail: %v", err)
	}
	if _, err := env.service.CreateDraft(ctx, env.viewUser, env.dbID, "Denied", "SELECT 1;"); !errors.Is(err, migration.ErrForbidden) {
		t.Fatalf("viewUser CreateDraft: got %v, want ErrForbidden", err)
	}
	if _, err := env.service.UpdateDraft(ctx, env.viewUser, env.dbID, draft.ID, "Denied", "SELECT 1;"); !errors.Is(err, migration.ErrForbidden) {
		t.Fatalf("viewUser UpdateDraft: got %v, want ErrForbidden", err)
	}
	if _, err := env.service.Execute(ctx, env.viewUser, env.dbID, draft.ID); !errors.Is(err, migration.ErrForbidden) {
		t.Fatalf("viewUser Execute: got %v, want ErrForbidden", err)
	}
}

func TestIntegrationParallelExecutionMaintainsLinearHistory(t *testing.T) {
	ctx := context.Background()
	env := setupIntegrationEnv(t)

	const numMigrations = 8
	draftIDs := make([]int64, numMigrations)
	for i := 0; i < numMigrations; i++ {
		sql := fmt.Sprintf("CREATE TABLE parallel_tbl_%d (id INT PRIMARY KEY);", i+1)
		d, err := env.service.CreateDraft(ctx, env.editUser, env.dbID, fmt.Sprintf("Parallel %d", i+1), sql)
		if err != nil {
			t.Fatalf("create draft %d: %v", i+1, err)
		}
		draftIDs[i] = d.ID
	}

	var wg sync.WaitGroup
	errs := make(chan error, numMigrations)
	for _, id := range draftIDs {
		wg.Add(1)
		go func(migID int64) {
			defer wg.Done()
			if _, err := env.service.Execute(ctx, env.editUser, env.dbID, migID); err != nil {
				errs <- err
			}
		}(id)
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Fatalf("parallel execute error: %v", err)
	}

	history, err := env.service.ListHistory(ctx, env.viewUser, env.dbID)
	if err != nil {
		t.Fatalf("ListHistory: %v", err)
	}
	if len(history) != numMigrations {
		t.Fatalf("history len = %d, want %d", len(history), numMigrations)
	}
	for i, m := range history {
		wantVer := i + 1
		if m.Version == nil || *m.Version != wantVer {
			t.Fatalf("history[%d].Version = %v, want %d", i, m.Version, wantVer)
		}
		if m.Status != migration.StatusApplied {
			t.Fatalf("history[%d].Status = %s, want applied", i, m.Status)
		}
	}

	singleDraft, err := env.service.CreateDraft(ctx, env.editUser, env.dbID, "Single race test", "CREATE TABLE single_race (id INT);")
	if err != nil {
		t.Fatalf("create single draft: %v", err)
	}

	const workers = 5
	var successCount atomic.Int32
	var alreadyAppliedCount atomic.Int32
	var wgRace sync.WaitGroup
	for i := 0; i < workers; i++ {
		wgRace.Add(1)
		go func() {
			defer wgRace.Done()
			_, err := env.service.Execute(ctx, env.editUser, env.dbID, singleDraft.ID)
			if err == nil {
				successCount.Add(1)
			} else if errors.Is(err, migration.ErrAlreadyApplied) {
				alreadyAppliedCount.Add(1)
			} else {
				t.Errorf("unexpected error in concurrent same-draft execution: %v", err)
			}
		}()
	}
	wgRace.Wait()

	if successCount.Load() != 1 || alreadyAppliedCount.Load() != workers-1 {
		t.Fatalf("same-draft race: success=%d, alreadyApplied=%d; want 1 and %d",
			successCount.Load(), alreadyAppliedCount.Load(), workers-1)
	}
}

func TestIntegrationExplainAndExplainAnalyzeRollback(t *testing.T) {
	ctx := context.Background()
	env := setupIntegrationEnv(t)

	sql := `
		CREATE TABLE departments (id INT PRIMARY KEY, name TEXT NOT NULL);
		CREATE TABLE employees (id INT PRIMARY KEY, dept_id INT NOT NULL REFERENCES departments(id), salary INT NOT NULL);
		CREATE INDEX idx_employees_dept ON employees (dept_id);
		INSERT INTO departments (id, name) VALUES (1, 'Engineering'), (2, 'Sales');
		INSERT INTO employees (id, dept_id, salary) VALUES (10, 1, 120000), (11, 1, 130000), (12, 2, 95000);
		SELECT e.id, d.name FROM employees e INNER JOIN departments d ON d.id = e.dept_id WHERE e.dept_id = 1;
	`

	preview, err := env.service.AnalyzeSQL(ctx, env.editUser, env.dbID, "Department employee query plan", sql, true)
	if err != nil {
		t.Fatalf("AnalyzeSQL (EXPLAIN ANALYZE): %v", err)
	}
	if preview.PlanReport == nil || !preview.PlanReport.Analyze {
		t.Fatalf("expected non-nil PlanReport with Analyze=true")
	}
	if len(preview.PlanReport.Statements) != 6 {
		t.Fatalf("statements in plan report = %d, want 6", len(preview.PlanReport.Statements))
	}

	selectPlan := preview.PlanReport.Statements[5]
	if !selectPlan.IsExplainable || selectPlan.Mode != "EXPLAIN ANALYZE" {
		t.Fatalf("selectPlan = %+v, want IsExplainable=true Mode=EXPLAIN ANALYZE", selectPlan)
	}
	if len(selectPlan.ScanTypes) == 0 {
		t.Errorf("expected scan types to be extracted, got none")
	}
	if len(selectPlan.JoinStrategies) == 0 {
		t.Errorf("expected join strategy to be extracted, got none")
	}
	if selectPlan.ActualTotalTimeMs == nil || selectPlan.ActualRows == nil || *selectPlan.ActualRows != 2 {
		t.Errorf("expected actual time and actual rows=2, got time=%v rows=%v", selectPlan.ActualTotalTimeMs, selectPlan.ActualRows)
	}

	var deptExists bool
	if err := env.targetPool.QueryRow(ctx, "SELECT to_regclass('departments') IS NOT NULL").Scan(&deptExists); err != nil {
		t.Fatalf("check departments after explain analyze: %v", err)
	}
	if deptExists {
		t.Fatal("departments table exists after EXPLAIN ANALYZE; sandbox transaction was not rolled back!")
	}
}


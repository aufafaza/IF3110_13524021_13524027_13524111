//go:build integration

package migration_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/migration"
)

func newDefinedDraft(t *testing.T, env *integrationEnv, title string, down, reason *string) migration.Migration {
	t.Helper()
	m, err := env.service.CreateDraftWithDefinition(context.Background(), env.editUser, env.dbID, title, "SELECT 1;", down, reason)
	if err != nil {
		t.Fatalf("create draft %q: %v", title, err)
	}
	return m
}

func recordAttempt(t *testing.T, env *integrationEnv, m migration.Migration, exec, att string,
	op migration.Operation, st migration.AttemptStatus, target string) bool {
	t.Helper()
	a := migration.AttemptResult{
		DatabaseID: env.dbID, MigrationID: m.ID, Revision: m.Revision,
		ExecutionID: exec, AttemptID: att, Operation: op, Status: st,
		ActorID: env.editUser, ExecutedSQL: "SELECT 1;",
	}
	if target != "" {
		a.RollbackOfExecutionID = &target
	}
	_, created, err := env.service.RecordAttempt(context.Background(), a)
	if err != nil {
		t.Fatalf("record %s/%s: %v", exec, att, err)
	}
	return created
}

func requireSQLState(t *testing.T, err error, code string) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != code {
		t.Fatalf("got %v, want SQLSTATE %s", err, code)
	}
}

func TestIntegrationDefinitionImmutableAfterFirstAttempt(t *testing.T) {
	ctx := context.Background()
	env := setupIntegrationEnv(t)
	m := newDefinedDraft(t, env, "Add a", sp("DROP TABLE a;"), nil)

	edited, err := env.service.UpdateDraftWithDefinition(ctx, env.editUser, env.dbID, m.ID,
		"Add a", "SELECT 2;", sp("DROP TABLE a;"), nil)
	if err != nil || edited.Revision != m.Revision+1 {
		t.Fatalf("edit before execution: rev=%d err=%v", edited.Revision, err)
	}

	recordAttempt(t, env, edited, "E1", "A1", migration.OpApply, migration.AttemptFailed, "")

	_, err = env.service.UpdateDraftWithDefinition(ctx, env.editUser, env.dbID, m.ID,
		"Add a", "SELECT 3;", sp("DROP TABLE x;"), nil)
	if !errors.Is(err, migration.ErrImmutable) {
		t.Fatalf("update after first attempt: got %v, want ErrImmutable", err)
	}
	_, err = env.platformPool.Exec(ctx, `UPDATE migrations SET down_sql = 'DROP TABLE z;' WHERE id = $1`, m.ID)
	requireSQLState(t, err, "23000")

	_, err = env.platformPool.Exec(ctx, `UPDATE migration_history SET executed_sql = 'x' WHERE migration_id = $1`, m.ID)
	requireSQLState(t, err, "23000")
}

func TestIntegrationRetryRedeliveryAndHideRollback(t *testing.T) {
	ctx := context.Background()
	env := setupIntegrationEnv(t)
	m := newDefinedDraft(t, env, "Add a", sp("DROP TABLE a;"), nil)

	if !recordAttempt(t, env, m, "E1", "A1", migration.OpApply, migration.AttemptFailed, "") {
		t.Fatal("first delivery of (E1,A1) should create a row")
	}
	if recordAttempt(t, env, m, "E1", "A1", migration.OpApply, migration.AttemptFailed, "") {
		t.Fatal("redelivery of (E1,A1) must not create a row")
	}
	recordAttempt(t, env, m, "E1", "A2", migration.OpApply, migration.AttemptSucceeded, "")
	recordAttempt(t, env, m, "E2", "A3", migration.OpRollback, migration.AttemptSucceeded, "E1")

	full, err := env.service.ListAttemptHistory(ctx, env.viewUser, env.dbID, false)
	if err != nil {
		t.Fatalf("full history: %v", err)
	}
	if got, want := execIDs(full), []string{"E1/A1", "E1/A2", "E2/A3"}; !equal(got, want) {
		t.Fatalf("full = %v, want %v", got, want)
	}
	hidden, err := env.service.ListAttemptHistory(ctx, env.viewUser, env.dbID, true)
	if err != nil {
		t.Fatalf("hidden history: %v", err)
	}
	if got, want := execIDs(hidden), []string{"E1/A1"}; !equal(got, want) {
		t.Fatalf("hidden = %v, want %v", got, want)
	}

	if _, err := env.service.ListAttemptHistory(ctx, env.noGrantUser, env.dbID, false); !errors.Is(err, migration.ErrForbidden) {
		t.Fatalf("no-grant user: got %v, want ErrForbidden", err)
	}
}

func TestIntegrationPlanOrderIrreversibleAndRevision(t *testing.T) {
	ctx := context.Background()
	env := setupIntegrationEnv(t)
	m1 := newDefinedDraft(t, env, "Add a", sp("DROP TABLE a;"), nil)
	m2 := newDefinedDraft(t, env, "Drop legacy", nil, sp("data is deleted"))

	recordAttempt(t, env, m1, "E1", "A1", migration.OpApply, migration.AttemptSucceeded, "")
	recordAttempt(t, env, m2, "E2", "A1", migration.OpApply, migration.AttemptSucceeded, "")

	if _, err := env.service.PlanOperation(ctx, env.editUser, env.dbID, m1.ID, migration.OpRollback, 0); !errors.Is(err, migration.ErrNotLatestActive) {
		t.Fatalf("rollback non-latest: got %v", err)
	}
	if _, err := env.service.PlanOperation(ctx, env.editUser, env.dbID, m2.ID, migration.OpRollback, 0); !errors.Is(err, migration.ErrIrreversible) {
		t.Fatalf("rollback irreversible: got %v", err)
	}
	if _, err := env.service.PlanOperation(ctx, env.editUser, env.dbID, m1.ID, migration.OpApply, 99); !errors.Is(err, migration.ErrRevisionMismatch) {
		t.Fatalf("wrong revision: got %v", err)
	}
	if _, err := env.service.PlanOperation(ctx, env.editUser, env.dbID, m1.ID, migration.OpApply, 0); !errors.Is(err, migration.ErrAlreadyApplied) {
		t.Fatalf("apply active migration: got %v", err)
	}
	if _, err := env.service.PlanOperation(ctx, env.viewUser, env.dbID, m1.ID, migration.OpRollback, 0); !errors.Is(err, migration.ErrForbidden) {
		t.Fatalf("view user plan: got %v", err)
	}

	_, _, err := env.service.RecordAttempt(ctx, migration.AttemptResult{
		DatabaseID: env.dbID, MigrationID: m1.ID, Revision: m1.Revision,
		ExecutionID: "E3", AttemptID: "A1", Operation: migration.OpRollback,
		Status: migration.AttemptSucceeded, ActorID: env.editUser,
		ExecutedSQL: "DROP TABLE a;", RollbackOfExecutionID: sp("E1"),
	})
	if !errors.Is(err, migration.ErrNotLatestActive) {
		t.Fatalf("record out-of-order rollback: got %v", err)
	}
}

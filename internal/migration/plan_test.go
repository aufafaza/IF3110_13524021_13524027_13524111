package migration_test

import (
	"errors"
	"testing"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/migration"
)

func sp(s string) *string { return &s }

func TestPlanRollbackRules(t *testing.T) {
	h := []migration.HistoryEntry{
		entry(1, "E1", "a1", migration.OpApply, migration.AttemptSucceeded, ""),
		entry(2, "E2", "a2", migration.OpApply, migration.AttemptSucceeded, ""),
	}
	h[0].Revision, h[1].Revision = 1, 1
	m1 := migration.Migration{ID: 1, Revision: 1, DownSQL: sp("DROP TABLE a;")}
	m2 := migration.Migration{ID: 2, Revision: 1, DownSQL: sp("DROP TABLE b;")}
	m2irr := migration.Migration{ID: 2, Revision: 1, IrreversibleReason: sp("data loss")}

	if _, err := migration.PlanRollback(m1, h); !errors.Is(err, migration.ErrNotLatestActive) {
		t.Fatalf("rollback non-latest: %v", err)
	}
	if _, err := migration.PlanRollback(m2irr, h); !errors.Is(err, migration.ErrIrreversible) {
		t.Fatalf("irreversible: %v", err)
	}
	p, err := migration.PlanRollback(m2, h)
	if err != nil || p.SQL != "DROP TABLE b;" || *p.RollbackOfExecutionID != "E2" {
		t.Fatalf("plan = %+v err=%v", p, err)
	}
}

func TestPlanForwardIsSingleLinearStep(t *testing.T) {
	h := []migration.HistoryEntry{
		entry(1, "E1", "a1", migration.OpApply, migration.AttemptSucceeded, ""),
		entry(2, "E2", "a2", migration.OpApply, migration.AttemptSucceeded, ""),
		entry(2, "E3", "a3", migration.OpRollback, migration.AttemptSucceeded, "E2"),
		entry(1, "E4", "a4", migration.OpRollback, migration.AttemptSucceeded, "E1"),
	}
	m1 := migration.Migration{ID: 1, Revision: 1, SQL: "CREATE TABLE a (id int);"}
	m2 := migration.Migration{ID: 2, Revision: 1, SQL: "CREATE TABLE b (id int);"}

	// Terakhir di-rollback adalah migrasi 1, jadi forward-nya duluan.
	if _, err := migration.PlanForward(m2, h); !errors.Is(err, migration.ErrNotLatestActive) {
		t.Fatalf("forward wrong order: %v", err)
	}
	if p, err := migration.PlanForward(m1, h); err != nil || p.Operation != migration.OpForward {
		t.Fatalf("forward m1: %+v %v", p, err)
	}
	if _, err := migration.PlanForward(m1, h[:2]); !errors.Is(err, migration.ErrNothingToForward) {
		t.Fatalf("nothing to forward: %v", err)
	}
}

func TestValidateDefinition(t *testing.T) {
	if err := migration.ValidateDefinition("SELECT 1;", nil, nil); !errors.Is(err, migration.ErrInvalidDefinition) {
		t.Fatalf("neither: %v", err)
	}
	if err := migration.ValidateDefinition("SELECT 1;", sp("SELECT 2;"), sp("x")); !errors.Is(err, migration.ErrInvalidDefinition) {
		t.Fatalf("both: %v", err)
	}
	if err := migration.ValidateDefinition("SELECT 1;", nil, sp("irreversible")); err != nil {
		t.Fatalf("reason only: %v", err)
	}
}

func TestPlanApplyFor(t *testing.T) {
	m := migration.Migration{ID: 1, Revision: 1, SQL: "CREATE TABLE a (id int);"}
	active := []migration.HistoryEntry{entry(1, "E1", "a1", migration.OpApply, migration.AttemptSucceeded, "")}
	if _, err := migration.PlanApplyFor(m, active); !errors.Is(err, migration.ErrAlreadyApplied) {
		t.Fatalf("active: %v", err)
	}
	failedOnly := []migration.HistoryEntry{entry(1, "E1", "a1", migration.OpApply, migration.AttemptFailed, "")}
	if _, err := migration.PlanApplyFor(m, failedOnly); err != nil {
		t.Fatalf("retry after failure should be allowed: %v", err)
	}
	rolledBack := []migration.HistoryEntry{
		entry(1, "E1", "a1", migration.OpApply, migration.AttemptSucceeded, ""),
		entry(1, "E2", "a2", migration.OpRollback, migration.AttemptSucceeded, "E1"),
	}
	if _, err := migration.PlanApplyFor(m, rolledBack); !errors.Is(err, migration.ErrNotDraft) {
		t.Fatalf("rolled back must use forward: %v", err)
	}
}

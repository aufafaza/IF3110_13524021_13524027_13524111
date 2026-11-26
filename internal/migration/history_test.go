package migration_test

import (
	"testing"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/migration"
)

func entry(mig int64, exec, attempt string, op migration.Operation, st migration.AttemptStatus, target string) migration.HistoryEntry {
	e := migration.HistoryEntry{MigrationID: mig, ExecutionID: exec, AttemptID: attempt, Operation: op, Status: st}
	if target != "" {
		e.RollbackOfExecutionID = &target
	}
	return e
}

func execIDs(es []migration.HistoryEntry) []string {
	var out []string
	for _, e := range es {
		out = append(out, e.ExecutionID+"/"+e.AttemptID)
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestHideRollbackSpecExampleE1toE5(t *testing.T) {
	h := []migration.HistoryEntry{
		entry(1, "E1", "a1", migration.OpApply, migration.AttemptSucceeded, ""),
		entry(2, "E2", "a2", migration.OpApply, migration.AttemptSucceeded, ""),
		entry(2, "E3", "a3", migration.OpRollback, migration.AttemptSucceeded, "E2"),
		entry(2, "E4", "a4", migration.OpForward, migration.AttemptSucceeded, ""),
		entry(3, "E5", "a5", migration.OpApply, migration.AttemptSucceeded, ""),
	}
	if got := execIDs(migration.VisibleHistory(h, false)); len(got) != 5 {
		t.Fatalf("full history = %v", got)
	}
	want := []string{"E1/a1", "E4/a4", "E5/a5"}
	if got := execIDs(migration.VisibleHistory(h, true)); !equal(got, want) {
		t.Fatalf("hidden = %v, want %v", got, want)
	}
	chain, err := migration.ActiveChain(h)
	if err != nil || len(chain) != 3 || chain[2].MigrationID != 3 {
		t.Fatalf("chain = %+v err=%v", chain, err)
	}
}

func TestRetryVsRedeliveryAndFailedAttemptsStayVisible(t *testing.T) {
	h := []migration.HistoryEntry{
		entry(1, "E1", "A1", migration.OpApply, migration.AttemptFailed, ""),
		entry(1, "E1", "A1", migration.OpApply, migration.AttemptFailed, ""), // redelivery
		entry(1, "E1", "A2", migration.OpApply, migration.AttemptSucceeded, ""),
		entry(1, "E2", "A3", migration.OpRollback, migration.AttemptSucceeded, "E1"),
	}
	dedup := migration.DedupeAttempts(h)
	if len(dedup) != 3 {
		t.Fatalf("dedupe len = %d, want 3", len(dedup))
	}
	if got, want := execIDs(migration.VisibleHistory(dedup, true)), []string{"E1/A1"}; !equal(got, want) {
		t.Fatalf("hidden = %v, want %v", got, want)
	}
	if got, want := execIDs(migration.VisibleHistory(dedup, false)), []string{"E1/A1", "E1/A2", "E2/A3"}; !equal(got, want) {
		t.Fatalf("full = %v, want %v", got, want)
	}
	chain, err := migration.ActiveChain(h)
	if err != nil || len(chain) != 0 {
		t.Fatalf("chain = %+v err=%v, want empty", chain, err)
	}
}

func TestFailedRollbackDoesNotChangeActivePosition(t *testing.T) {
	h := []migration.HistoryEntry{
		entry(1, "E1", "a1", migration.OpApply, migration.AttemptSucceeded, ""),
		entry(1, "E2", "a2", migration.OpRollback, migration.AttemptFailed, ""),
	}
	chain, err := migration.ActiveChain(h)
	if err != nil || len(chain) != 1 {
		t.Fatalf("chain = %+v err=%v, want 1 active", chain, err)
	}
}

func TestRollbackOutOfOrderIsInconsistent(t *testing.T) {
	h := []migration.HistoryEntry{
		entry(1, "E1", "a1", migration.OpApply, migration.AttemptSucceeded, ""),
		entry(2, "E2", "a2", migration.OpApply, migration.AttemptSucceeded, ""),
		entry(1, "E3", "a3", migration.OpRollback, migration.AttemptSucceeded, "E1"),
	}
	if _, err := migration.ActiveChain(h); err == nil {
		t.Fatal("expected ErrHistoryInconsistent")
	}
}

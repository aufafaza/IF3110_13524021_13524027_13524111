package migration

import (
	"errors"
	"fmt"
	"time"
)

type Operation string

const (
	OpApply    Operation = "apply"
	OpRollback Operation = "rollback"
	OpForward  Operation = "forward"
)

func (op Operation) IsOrdinary() bool {
	return op == OpApply || op == OpRollback || op == OpForward
}

type AttemptStatus string

const (
	AttemptSucceeded AttemptStatus = "success"
	AttemptFailed    AttemptStatus = "failed"
)

type HistoryEntry struct {
	ID                    int64         `json:"id"`
	DatabaseID            int64         `json:"database_id"`
	MigrationID           int64         `json:"migration_id"`
	Revision              int           `json:"revision"`
	ExecutionID           string        `json:"execution_id"`
	AttemptID             string        `json:"attempt_id"`
	Operation             Operation     `json:"operation"`
	Status                AttemptStatus `json:"status"`
	ActorID               int64         `json:"actor_id"`
	ExecutedSQL           string        `json:"executed_sql"`
	ErrorMessage          *string       `json:"error_message,omitempty"`
	RollbackOfExecutionID *string       `json:"rollback_of_execution_id,omitempty"`
	CreatedAt             time.Time     `json:"created_at"`
}

var ErrHistoryInconsistent = errors.New("migration: history is inconsistent")

func DedupeAttempts(entries []HistoryEntry) []HistoryEntry {
	seen := make(map[[2]string]bool, len(entries))
	out := make([]HistoryEntry, 0, len(entries))
	for _, e := range entries {
		k := [2]string{e.ExecutionID, e.AttemptID}
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, e)
	}
	return out
}

func isSuccessfulRollback(e HistoryEntry) bool {
	return e.Operation == OpRollback && e.Status == AttemptSucceeded && e.RollbackOfExecutionID != nil
}

func VisibleHistory(entries []HistoryEntry, hide bool) []HistoryEntry {
	if !hide {
		return append([]HistoryEntry(nil), entries...)
	}
	cancelled := make(map[string]bool)
	for _, e := range entries {
		if isSuccessfulRollback(e) {
			cancelled[*e.RollbackOfExecutionID] = true
		}
	}
	out := make([]HistoryEntry, 0, len(entries))
	for _, e := range entries {
		if isSuccessfulRollback(e) {
			continue
		}
		if e.Status == AttemptSucceeded && cancelled[e.ExecutionID] {
			continue
		}
		out = append(out, e)
	}
	return out
}

func ActiveChain(entries []HistoryEntry) ([]HistoryEntry, error) {
	var chain []HistoryEntry
	for _, e := range DedupeAttempts(entries) {
		if e.Status != AttemptSucceeded {
			continue
		}
		if e.Operation == OpRollback {
			if e.RollbackOfExecutionID == nil || len(chain) == 0 ||
				chain[len(chain)-1].ExecutionID != *e.RollbackOfExecutionID {
				return nil, fmt.Errorf("%w: rollback %s does not cancel top of chain",
					ErrHistoryInconsistent, e.ExecutionID)
			}
			chain = chain[:len(chain)-1]
			continue
		}
		chain = append(chain, e)
	}
	return chain, nil
}

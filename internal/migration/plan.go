package migration

import "strings"

type Plan struct {
	BatchConfig           *BatchConfig `json:"batch_config,omitempty"`
	Operation             Operation    `json:"operation"`
	MigrationID           int64        `json:"migration_id"`
	Revision              int          `json:"revision"`
	SQL                   string       `json:"sql"`
	TargetExecutionID     string       `json:"target_execution_id,omitempty"`
	RollbackOfExecutionID *string      `json:"rollback_of_execution_id,omitempty"`
}

func (p Plan) Mode() string {
	if p.BatchConfig != nil {
		return "batch"
	}
	return "ordinary"
}

func ValidateDefinition(up string, down, reason *string) error {
	if err := ValidateSQL(up); err != nil {
		return err
	}
	if (down == nil) == (reason == nil) {
		return ErrInvalidDefinition
	}
	if down != nil {
		return ValidateSQL(*down)
	}
	if strings.TrimSpace(*reason) == "" {
		return ErrInvalidDefinition
	}
	return nil
}

func PlanApply(m Migration) Plan {
	return Plan{Operation: OpApply, MigrationID: m.ID, Revision: m.Revision, SQL: m.SQL, BatchConfig: m.BatchConfig}
}

func PlanRollback(m Migration, entries []HistoryEntry) (Plan, error) {
	chain, err := ActiveChain(entries)
	if err != nil {
		return Plan{}, err
	}
	if len(chain) == 0 || chain[len(chain)-1].MigrationID != m.ID {
		return Plan{}, ErrNotLatestActive
	}
	top := chain[len(chain)-1]
	if m.DownSQL == nil {
		return Plan{}, ErrIrreversible
	}
	if top.Revision != m.Revision {
		return Plan{}, ErrImmutable
	}
	target := top.ExecutionID
	return Plan{Operation: OpRollback, MigrationID: m.ID, Revision: m.Revision,
		SQL: *m.DownSQL, TargetExecutionID: target, RollbackOfExecutionID: &target}, nil
}

func nextForward(entries []HistoryEntry) (HistoryEntry, bool) {
	var stack []HistoryEntry
	for _, e := range DedupeAttempts(entries) {
		if e.Status != AttemptSucceeded {
			continue
		}
		switch e.Operation {
		case OpRollback:
			stack = append(stack, e)
		case OpForward:
			if n := len(stack); n > 0 && stack[n-1].MigrationID == e.MigrationID {
				stack = stack[:n-1]
			}
		case OpApply, OpBatchRecovery:
			stack = nil
		}
	}
	if len(stack) == 0 {
		return HistoryEntry{}, false
	}
	return stack[len(stack)-1], true
}

func PlanForward(m Migration, entries []HistoryEntry) (Plan, error) {
	target, ok := nextForward(entries)
	if !ok {
		return Plan{}, ErrNothingToForward
	}
	if target.MigrationID != m.ID {
		return Plan{}, ErrNotLatestActive
	}
	if target.Revision != m.Revision {
		return Plan{}, ErrImmutable
	}
	return Plan{Operation: OpForward, MigrationID: m.ID, Revision: m.Revision, SQL: m.SQL, TargetExecutionID: target.ExecutionID}, nil
}

// PlanApplyFor: apply hanya untuk migrasi yang belum pernah berhasil.
// Retry attempt gagal tetap lewat jalur ini
func PlanApplyFor(m Migration, entries []HistoryEntry) (Plan, error) {
	chain, err := ActiveChain(entries)
	if err != nil {
		return Plan{}, err
	}
	for _, c := range chain {
		if c.MigrationID == m.ID {
			return Plan{}, ErrAlreadyApplied
		}
	}
	for _, e := range DedupeAttempts(entries) {
		if e.MigrationID == m.ID && e.Status == AttemptSucceeded && e.Operation != OpRollback && e.Operation != OpCompensation {
			return Plan{}, ErrNotDraft
		}
	}
	return PlanApply(m), nil
}

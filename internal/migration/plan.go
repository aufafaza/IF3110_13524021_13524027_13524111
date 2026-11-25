package migration

type Plan struct {
	Operation             Operation `json:"operation"`
	MigrationID           int64     `json:"migration_id"`
	Revision              int       `json:"revision"`
	SQL                   string    `json:"sql"`
	RollbackOfExecutionID *string   `json:"rollback_of_execution_id,omitempty"`
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
	return Plan{Operation: OpApply, MigrationID: m.ID, Revision: m.Revision, SQL: m.SQL}
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
		SQL: *m.DownSQL, RollbackOfExecutionID: &target}, nil
}

func NextForward(entries []HistoryEntry) (int64, bool) {
	var stack []int64
	for _, e := range DedupeAttempts(entries) {
		if e.Status != AttemptSucceeded {
			continue
		}
		switch e.Operation {
		case OpRollback:
			stack = append(stack, e.MigrationID)
		case OpForward:
			if n := len(stack); n > 0 && stack[n-1] == e.MigrationID {
				stack = stack[:n-1]
			}
		case OpApply:
			stack = nil
		}
	}
	if len(stack) == 0 {
		return 0, false
	}
	return stack[len(stack)-1], true
}

func PlanForward(m Migration, entries []HistoryEntry) (Plan, error) {
	id, ok := NextForward(entries)
	if !ok {
		return Plan{}, ErrNothingToForward
	}
	if id != m.ID {
		return Plan{}, ErrNotLatestActive
	}
	return Plan{Operation: OpForward, MigrationID: m.ID, Revision: m.Revision, SQL: m.SQL}, nil
}
package migration

import (
	"errors"
	"time"
)

type Status string

const (
	StatusDraft   Status = "draft"
	StatusApplied Status = "applied"
	StatusFailed  Status = "failed"
)

func (s Status) IsValid() bool {
	switch s {
	case StatusDraft, StatusApplied, StatusFailed:
		return true
	default:
		return false
	}
}

func (s Status) CanTransitionTo(next Status) bool {
	if s != StatusDraft {
		return false
	}
	return next == StatusApplied || next == StatusFailed
}

type Migration struct {
	ID                 int64      `json:"id"`
	DatabaseID         int64      `json:"database_id"`
	Version            *int       `json:"version,omitempty"`
	Title              string     `json:"title"`
	SQL                string     `json:"sql"`
	Status             Status     `json:"status"`
	DownSQL            *string    `json:"down_sql,omitempty"`
	IrreversibleReason *string    `json:"irreversible_reason,omitempty"`
	Revision           int        `json:"revision"`
	FirstExecutedAt    *time.Time `json:"first_executed_at,omitempty"`
	CreatedBy          int64      `json:"created_by"`
	CreatedByUsername  string     `json:"created_by_username,omitempty"`
	ExecutedBy         *int64     `json:"executed_by,omitempty"`
	ExecutedByUsername *string    `json:"executed_by_username,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
	ExecutedAt         *time.Time `json:"executed_at,omitempty"`
	ErrorMessage       *string    `json:"error_message,omitempty"`
}

var (
	ErrNotFound            = errors.New("migration: not found")
	ErrForbidden           = errors.New("migration: access denied")
	ErrUnauthenticated     = errors.New("migration: unauthenticated")
	ErrAlreadyApplied      = errors.New("migration: migration has already been applied")
	ErrNotDraft            = errors.New("migration: only draft migrations can be modified or executed")
	ErrEmptyTitle          = errors.New("migration: title must not be empty")
	ErrEmptySQL            = errors.New("migration: SQL must not be empty")
	ErrNonTransactionalSQL = errors.New("migration: statement cannot be executed inside a transaction")
	ErrInvalidTransition   = errors.New("migration: invalid status transition")
	ErrVersionConflict     = errors.New("migration: version conflict")
	ErrConfirmationMissing = errors.New("migration: execution confirmation is required")
	ErrNotLatestActive     = errors.New("migration: only the latest active migration can be rolled back")
	ErrIrreversible        = errors.New("migration: migration is irreversible")
	ErrImmutable           = errors.New("migration: definition is immutable after first execution")
	ErrNothingToForward    = errors.New("migration: nothing to forward in linear order")
	ErrInvalidDefinition   = errors.New("migration: provide either down SQL or an irreversible reason")
	ErrRevisionMismatch    = errors.New("migration: revision does not match the approved revision")
	ErrInvalidAttempt      = errors.New("migration: invalid attempt result")
	ErrHistoryUnavailable  = errors.New("migration: history repository is not configured")
)

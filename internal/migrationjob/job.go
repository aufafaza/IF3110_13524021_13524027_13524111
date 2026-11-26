package migrationjob

import (
	"context"
	"errors"
	"time"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/migration"
)

const (
	Apply    = "apply"
	Ordinary = "ordinary"
	Batch    = "batch"
)

type State string

const (
	Queued      State = "queued"
	Running     State = "running"
	Reconciling State = "reconciling"
	Reporting   State = "reporting"
	Retrying    State = "retrying"
	Partial     State = "partial"
	Succeeded   State = "succeeded"
	Failed      State = "failed"
)

var (
	ErrNotFound         = errors.New("migration job not found")
	ErrApprovalMismatch = errors.New("approved request ID has different execution identity")
	ErrIneligible       = errors.New("migration is no longer eligible")
	errClaimLost        = errors.New("migration job claim expired or was replaced")
)

type Approval struct {
	RequestID         string
	ActorID           int64
	DatabaseID        int64
	MigrationID       int64
	Revision          string
	Operation         string
	TargetExecutionID string
	Mode              string
	Traceparent       string
	Tracestate        string
}

type ApprovalSource interface {
	PendingDispatch(context.Context) ([]Approval, error)
	AcknowledgeDispatch(ctx context.Context, requestID string, jobID int64) error
}

type Authorization interface {
	Recheck(context.Context, Approval) error
}

type Plans interface {
	ResolvePlan(context.Context, Approval) (migration.Plan, error)
}

type BatchPlans interface {
	ResolveBatch(context.Context, Approval) (BatchPlan, error)
}

type BatchPlan struct {
	ScopeSQL string
	SQL      string
	Size     int
}

type Outcome struct {
	Approval
	JobID                 int64
	ExecutionID           string
	AttemptID             string
	Attempt               int
	SQL                   string
	StartedAt             time.Time
	FinishedAt            time.Time
	FailureCode           string     // empty on success
	RollbackOfExecutionID *string    `json:",omitempty"`
	BatchPlan             *BatchPlan `json:",omitempty"`
}

type History interface {
	Record(context.Context, Outcome) error
}

type Job struct {
	ID                int64     `json:"id"`
	ExecutionID       string    `json:"execution_id"`
	DatabaseID        int64     `json:"database_id"`
	MigrationID       int64     `json:"migration_id"`
	Revision          string    `json:"revision"`
	Operation         string    `json:"operation"`
	TargetExecutionID string    `json:"target_execution_id,omitempty"`
	Mode              string    `json:"mode"`
	ScopeRows         *int64    `json:"scope_rows,omitempty"`
	CommittedBatches  int64     `json:"committed_batches"`
	CommittedRows     int64     `json:"committed_rows"`
	BatchPaused       bool      `json:"batch_paused,omitempty"`
	State             State     `json:"state"`
	Attempts          int       `json:"attempts"`
	FailureCode       string    `json:"failure_code,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type Dispatcher struct {
	store  *Store
	source ApprovalSource
}

func NewDispatcher(store *Store, source ApprovalSource) *Dispatcher {
	return &Dispatcher{store: store, source: source}
}

func (d *Dispatcher) Dispatch(ctx context.Context, approved Approval) (Job, error) {
	job, err := d.store.enqueue(ctx, approved)
	if err != nil {
		return Job{}, err
	}
	return job, d.source.AcknowledgeDispatch(ctx, approved.RequestID, job.ID)
}

// DispatchPending repairs missed dispatches on startup and during polling.
func (d *Dispatcher) DispatchPending(ctx context.Context) error {
	requests, err := d.source.PendingDispatch(ctx)
	if err != nil {
		return err
	}
	var dispatchErr error
	for _, request := range requests {
		_, err := d.Dispatch(ctx, request)
		dispatchErr = errors.Join(dispatchErr, err)
	}
	return dispatchErr
}

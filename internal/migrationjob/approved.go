package migrationjob

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/access"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/database"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/migration"
)

type reviewSource struct {
	repo *migration.PostgresApprovalRepository
}

func approvalFromReview(review migration.Review) Approval {
	return Approval{
		RequestID: review.RequestID, ActorID: review.RequestedBy, DatabaseID: review.DatabaseID,
		MigrationID: review.MigrationID, Revision: review.Revision, Operation: review.Operation, Mode: Ordinary,
	}
}

func (s reviewSource) PendingDispatch(ctx context.Context) ([]Approval, error) {
	reviews, err := s.repo.PendingApprovedApply(ctx)
	if err != nil {
		return nil, err
	}
	approvals := make([]Approval, len(reviews))
	for i, review := range reviews {
		approvals[i] = approvalFromReview(review)
	}
	return approvals, nil
}

func (s reviewSource) AcknowledgeDispatch(ctx context.Context, requestID string, jobID int64) error {
	return s.repo.AcknowledgeDispatch(ctx, requestID, jobID)
}

type ReviewQueue struct {
	dispatcher *Dispatcher
}

func NewReviewQueue(store *Store, reviews *migration.PostgresApprovalRepository) *ReviewQueue {
	return &ReviewQueue{dispatcher: NewDispatcher(store, reviewSource{repo: reviews})}
}

func (q *ReviewQueue) DispatchReview(ctx context.Context, review migration.Review) (migration.QueueJob, error) {
	if review.Status != migration.ReviewApproved {
		return migration.QueueJob{}, migration.ErrQueueNotApproved
	}
	if review.Operation != Apply {
		return migration.QueueJob{}, migration.ErrInvalidOperation
	}
	job, err := q.dispatcher.Dispatch(ctx, approvalFromReview(review))
	return migration.QueueJob{
		ID: job.ID, RequestID: review.RequestID, DatabaseID: job.DatabaseID,
		Status: string(job.State), Attempts: job.Attempts, CreatedAt: job.CreatedAt,
	}, err
}

// Run starts with a discovery pass so an approval committed before a server
// stop can still reach the worker's queue after restart.
func (q *ReviewQueue) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for ctx.Err() == nil {
		dispatchCtx, cancel := context.WithTimeout(ctx, stepTimeout)
		err := q.dispatcher.DispatchPending(dispatchCtx)
		cancel()
		if err != nil && ctx.Err() == nil {
			slog.WarnContext(ctx, "approved migration discovery failed")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

type approvedWorkerServices struct {
	service *migration.Service
}

func NewApprovedWorker(store *Store, target database.Provider, service *migration.Service) *Worker {
	deps := approvedWorkerServices{service: service}
	return NewWorker(store, target, deps, deps, deps)
}

func (s approvedWorkerServices) Recheck(ctx context.Context, approved Approval) error {
	level, err := s.service.UserLevel(ctx, approved.ActorID, approved.DatabaseID)
	if err != nil {
		return err
	}
	if level < access.LevelEdit {
		return ErrIneligible
	}
	return nil
}

func (s approvedWorkerServices) ResolveSQL(ctx context.Context, approved Approval) (string, error) {
	plan, err := s.service.PlanApproved(ctx, migration.ApprovedOperation{
		RequestID: approved.RequestID, ActorID: approved.ActorID, DatabaseID: approved.DatabaseID,
		MigrationID: approved.MigrationID, Revision: approved.Revision, Operation: migration.Operation(approved.Operation),
	}, true)
	switch {
	case errors.Is(err, migration.ErrApprovalRequired), errors.Is(err, migration.ErrRevisionChanged),
		errors.Is(err, migration.ErrForbidden), errors.Is(err, migration.ErrNotFound),
		errors.Is(err, migration.ErrAlreadyApplied), errors.Is(err, migration.ErrNotDraft),
		errors.Is(err, migration.ErrNonTransactionalSQL), errors.Is(err, migration.ErrEmptySQL):
		return "", errors.Join(ErrIneligible, err)
	default:
		return plan.SQL, err
	}
}

func (s approvedWorkerServices) Record(ctx context.Context, outcome Outcome) error {
	result := migration.AttemptResult{
		DatabaseID: outcome.DatabaseID, MigrationID: outcome.MigrationID,
		ExecutionID: outcome.ExecutionID, AttemptID: outcome.AttemptID,
		Operation: migration.Operation(outcome.Operation), Status: migration.AttemptSucceeded,
		ActorID: outcome.ActorID, ExecutedSQL: outcome.SQL, At: outcome.FinishedAt,
	}
	if outcome.FailureCode != "" {
		result.Status = migration.AttemptFailed
		result.ErrorMessage = &outcome.FailureCode
	}
	_, _, err := s.service.RecordAttempt(ctx, result)
	return err
}

package migration

import (
	"context"
	"errors"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/access"
)

type ApprovedOperation struct {
	RequestID   string
	ActorID     int64
	DatabaseID  int64
	MigrationID int64
	Operation   Operation
	Revision    string
}

func (s *Service) ApprovedApply(ctx context.Context, actorID, databaseID, migrationID int64) (Migration, Review, error) {
	if s.approvals == nil {
		return Migration{}, Review{}, ErrApprovalsUnavailable
	}
	if _, err := s.requireLevel(ctx, actorID, databaseID, access.LevelEdit); err != nil {
		return Migration{}, Review{}, err
	}
	m, err := s.repo.GetByID(ctx, databaseID, migrationID)
	if err != nil {
		return Migration{}, Review{}, err
	}
	review, err := s.approvals.ApprovedReview(ctx, m.ID, CurrentRevision(m), string(OpApply))
	if err != nil {
		return Migration{}, Review{}, err
	}
	if review.RequestedBy != actorID {
		return Migration{}, Review{}, ErrApprovalRequired
	}
	return m, review, nil
}

func (s *Service) PlanApproved(ctx context.Context, in ApprovedOperation, lock bool) (Plan, error) {
	if s.approvals == nil {
		return Plan{}, ErrApprovalsUnavailable
	}
	if s.history == nil {
		return Plan{}, ErrHistoryUnavailable
	}
	if _, err := s.requireLevel(ctx, in.ActorID, in.DatabaseID, access.LevelEdit); err != nil {
		return Plan{}, err
	}
	m, err := s.repo.GetByID(ctx, in.DatabaseID, in.MigrationID)
	if err != nil {
		return Plan{}, err
	}
	if CurrentRevision(m) != in.Revision {
		return Plan{}, ErrRevisionChanged
	}
	review, err := s.approvals.ApprovedReview(ctx, m.ID, in.Revision, string(in.Operation))
	if err != nil {
		return Plan{}, err
	}
	if review.RequestID != in.RequestID || review.RequestedBy != in.ActorID {
		return Plan{}, ErrApprovalRequired
	}
	if in.Operation == OpApply && m.Status != StatusDraft {
		return Plan{}, ErrNotDraft
	}
	plan, err := s.planForDefinition(ctx, m, in.Operation)
	if err != nil || !lock {
		return plan, err
	}
	locker, ok := s.repo.(interface {
		LockDefinition(context.Context, int64, int64, int) error
	})
	if !ok {
		return Plan{}, errors.New("migration: definition locking is unavailable")
	}
	if err := locker.LockDefinition(ctx, m.DatabaseID, m.ID, m.Revision); err != nil {
		return Plan{}, err
	}
	after, err := s.repo.GetByID(ctx, m.DatabaseID, m.ID)
	if err != nil {
		return Plan{}, err
	}
	if after.FirstExecutedAt == nil || after.Revision != m.Revision || CurrentRevision(after) != in.Revision {
		return Plan{}, ErrRevisionChanged
	}
	return plan, nil
}

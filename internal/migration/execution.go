package migration

import "context"

// ApprovedOperation: identitas operasi yang disetujui (dari job queue).
type ApprovedOperation struct {
	RequestID   string
	ActorID     int64
	DatabaseID  int64
	MigrationID int64
	Operation   Operation
	Revision    string // hash revisi dari approval
}

// PlanApproved memverifikasi ulang izin, revisi, dan approval, lalu mengembalikan
// rencana SQL. Dipanggil worker di bawah lock. Dengan lock=true, definisi
// dikunci (immutable) sebelum eksekusi, jadi tidak bisa diedit sebelum history tercatat.
func (s *Service) PlanApproved(ctx context.Context, in ApprovedOperation, lock bool) (Plan, error) {
	if s.approvals == nil {
		return Plan{}, ErrApprovalsUnavailable
	}
	m, err := s.repo.GetByID(ctx, in.DatabaseID, in.MigrationID)
	if err != nil {
		return Plan{}, err
	}
	if CurrentRevision(m) != in.Revision {
		return Plan{}, ErrRevisionChanged
	}
	review, err := s.approvals.ApprovedReview(ctx, in.MigrationID, in.Revision, string(in.Operation))
	if err != nil {
		return Plan{}, err
	}
	if review.RequestID != in.RequestID {
		return Plan{}, ErrApprovalRequired
	}
	plan, err := s.PlanOperation(ctx, in.ActorID, in.DatabaseID, in.MigrationID, in.Operation, 0)
	if err != nil || !lock {
		return plan, err
	}
	if l, ok := s.repo.(interface {
		LockDefinition(ctx context.Context, databaseID, migrationID int64) error
	}); ok {
		if err := l.LockDefinition(ctx, in.DatabaseID, in.MigrationID); err != nil {
			return Plan{}, err
		}
	}
	// Setelah dikunci, baca ulang: kalau draft sempat diedit di sela-sela, batalkan.
	after, err := s.repo.GetByID(ctx, in.DatabaseID, in.MigrationID)
	if err != nil {
		return Plan{}, err
	}
	if after.Revision != plan.Revision {
		return Plan{}, ErrRevisionChanged
	}
	return plan, nil
}
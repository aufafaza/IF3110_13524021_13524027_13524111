package migration

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/access"
)

type ReviewStatus string

const (
	ReviewPending  ReviewStatus = "pending"
	ReviewApproved ReviewStatus = "approved"
	ReviewRejected ReviewStatus = "rejected"
)

type ReviewDecision struct {
	GroupID      int64      `json:"group_id"`
	LeaderUserID int64      `json:"leader_user_id"`
	Decision     string     `json:"decision,omitempty"`
	Comment      string     `json:"comment,omitempty"`
	DecidedAt    *time.Time `json:"decided_at,omitempty"`
}

type Review struct {
	RequestID   string           `json:"request_id"`
	MigrationID int64            `json:"migration_id"`
	DatabaseID  int64            `json:"database_id"`
	Revision    string           `json:"revision"`
	Operation   string           `json:"operation"`
	RequestedBy int64            `json:"requested_by"`
	Status      ReviewStatus     `json:"status"`
	CreatedAt   time.Time        `json:"created_at"`
	DecidedAt   *time.Time       `json:"decided_at,omitempty"`
	EnqueuedAt  *time.Time       `json:"enqueued_at,omitempty"`
	Decisions   []ReviewDecision `json:"decisions"`
}

type QueueJob struct {
	ID        int64      `json:"id"`
	RequestID string     `json:"request_id"`
	Status    string     `json:"status"`
	Attempts  int        `json:"attempts"`
	CreatedAt time.Time  `json:"created_at"`
	StartedAt *time.Time `json:"started_at,omitempty"`
}

type SubmitReviewParams struct {
	MigrationID int64
	DatabaseID  int64
	Revision    string
	Operation   string
	RequestedBy int64
}

type ApprovalRepository interface {
	SubmitReview(ctx context.Context, p SubmitReviewParams) (Review, error)
	GetReview(ctx context.Context, requestID string) (Review, error)
	ListReviews(ctx context.Context, databaseID int64) ([]Review, error)
	DecideReview(ctx context.Context, requestID string, actorID int64, revision, decision, comment string) (Review, error)
	EnqueueApproved(ctx context.Context, requestID string) (QueueJob, error)
	ApprovedReview(ctx context.Context, migrationID int64, revision, operation string) (Review, error)
}

var (
	ErrApprovalsUnavailable = errors.New("migration: approvals are not configured")
	ErrLeaderMissing        = errors.New("migration: every database access group must have a leader")
	ErrReviewNotFound       = errors.New("migration: review request not found")
	ErrReviewClosed         = errors.New("migration: review is already closed")
	ErrNotGroupLeader       = errors.New("migration: only the group leader may decide")
	ErrRevisionChanged      = errors.New("migration: migration revision changed after review submission")
	ErrApprovalRequired     = errors.New("migration: an approved review is required")
	ErrInvalidOperation     = errors.New("migration: invalid review operation")
	ErrQueueNotApproved     = errors.New("migration: only an approved review can be queued")
)

func Revision(title, sql string) string {
	sum := sha256.Sum256([]byte(title + "\x00" + sql))
	return hex.EncodeToString(sum[:])
}

func newRequestID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "review-" + hex.EncodeToString([]byte(time.Now().UTC().String()))
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%s-%s-%s-%s-%s", hex.EncodeToString(b[0:4]), hex.EncodeToString(b[4:6]), hex.EncodeToString(b[6:8]), hex.EncodeToString(b[8:10]), hex.EncodeToString(b[10:]))
}

func normalizeOperation(operation string) (string, error) {
	operation = strings.ToLower(strings.TrimSpace(operation))
	if operation == "" {
		operation = "apply"
	}
	switch operation {
	case "apply", "rollback", "forward", "batch_recovery", "compensation":
		return operation, nil
	default:
		return "", ErrInvalidOperation
	}
}

type PostgresApprovalRepository struct{ pool *pgxpool.Pool }

func NewPostgresApprovalRepository(pool *pgxpool.Pool) *PostgresApprovalRepository {
	return &PostgresApprovalRepository{pool: pool}
}

func (r *PostgresApprovalRepository) SubmitReview(ctx context.Context, p SubmitReviewParams) (Review, error) {
	op, err := normalizeOperation(p.Operation)
	if err != nil {
		return Review{}, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Review{}, fmt.Errorf("migration: begin review: %w", err)
	}
	defer tx.Rollback(ctx)

	var status string
	if err := tx.QueryRow(ctx, `SELECT status FROM migrations WHERE id=$1 AND database_id=$2 FOR SHARE`, p.MigrationID, p.DatabaseID).Scan(&status); errors.Is(err, pgx.ErrNoRows) {
		return Review{}, ErrNotFound
	} else if err != nil {
		return Review{}, fmt.Errorf("migration: review migration: %w", err)
	} else if status != string(StatusDraft) {
		return Review{}, ErrNotDraft
	}

	type requirement struct {
		groupID  int64
		leaderID int64
	}
	var requirements []requirement
	rows, err := tx.Query(ctx, `SELECT g.id, g.leader_user_id
		FROM groups g JOIN group_db_access a ON a.group_id=g.id
		WHERE a.database_id=$1 ORDER BY g.id`, p.DatabaseID)
	if err != nil {
		return Review{}, fmt.Errorf("migration: review groups: %w", err)
	}
	for rows.Next() {
		var groupID int64
		var leaderID *int64
		if err := rows.Scan(&groupID, &leaderID); err != nil {
			rows.Close()
			return Review{}, fmt.Errorf("migration: review group: %w", err)
		}
		if leaderID == nil {
			rows.Close()
			return Review{}, ErrLeaderMissing
		}
		requirements = append(requirements, requirement{groupID, *leaderID})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return Review{}, fmt.Errorf("migration: review groups: %w", err)
	}
	rows.Close()
	if len(requirements) == 0 {
		return Review{}, ErrForbidden
	}

	requestID := newRequestID()
	_, err = tx.Exec(ctx, `INSERT INTO migration_review_requests
		(request_id,migration_id,revision,operation,requested_by)
		VALUES ($1,$2,$3,$4,$5)`, requestID, p.MigrationID, p.Revision, op, p.RequestedBy)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		if err := tx.Commit(ctx); err != nil {
			return Review{}, err
		}
		return r.findByRevision(ctx, p.MigrationID, p.Revision, op)
	}
	if err != nil {
		return Review{}, fmt.Errorf("migration: create review: %w", err)
	}
	for _, req := range requirements {
		if _, err := tx.Exec(ctx, `INSERT INTO migration_review_decisions(request_id,group_id,leader_user_id) VALUES($1,$2,$3)`, requestID, req.groupID, req.leaderID); err != nil {
			return Review{}, fmt.Errorf("migration: create review requirement: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Review{}, fmt.Errorf("migration: commit review: %w", err)
	}
	return r.GetReview(ctx, requestID)
}

func (r *PostgresApprovalRepository) findByRevision(ctx context.Context, migrationID int64, revision, operation string) (Review, error) {
	var requestID string
	err := r.pool.QueryRow(ctx, `SELECT request_id FROM migration_review_requests WHERE migration_id=$1 AND revision=$2 AND operation=$3`, migrationID, revision, operation).Scan(&requestID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Review{}, ErrReviewNotFound
	}
	if err != nil {
		return Review{}, err
	}
	return r.GetReview(ctx, requestID)
}

const reviewColumns = `request_id,migration_id,m.database_id,revision,operation,requested_by,status,created_at,decided_at,enqueued_at`

func (r *PostgresApprovalRepository) GetReview(ctx context.Context, requestID string) (Review, error) {
	var out Review
	var status string
	err := r.pool.QueryRow(ctx, `SELECT `+reviewColumns+` FROM migration_review_requests q JOIN migrations m ON m.id=q.migration_id WHERE q.request_id=$1`, requestID).Scan(&out.RequestID, &out.MigrationID, &out.DatabaseID, &out.Revision, &out.Operation, &out.RequestedBy, &status, &out.CreatedAt, &out.DecidedAt, &out.EnqueuedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Review{}, ErrReviewNotFound
	}
	if err != nil {
		return Review{}, fmt.Errorf("migration: get review: %w", err)
	}
	out.Status = ReviewStatus(status)
	out.Decisions, err = r.decisions(ctx, requestID)
	if err != nil {
		return Review{}, err
	}
	return out, nil
}

func (r *PostgresApprovalRepository) decisions(ctx context.Context, requestID string) ([]ReviewDecision, error) {
	rows, err := r.pool.Query(ctx, `SELECT group_id,leader_user_id,COALESCE(decision,''),comment,decided_at FROM migration_review_decisions WHERE request_id=$1 ORDER BY group_id`, requestID)
	if err != nil {
		return nil, fmt.Errorf("migration: list review decisions: %w", err)
	}
	defer rows.Close()
	var out []ReviewDecision
	for rows.Next() {
		var d ReviewDecision
		if err := rows.Scan(&d.GroupID, &d.LeaderUserID, &d.Decision, &d.Comment, &d.DecidedAt); err != nil {
			return nil, fmt.Errorf("migration: scan review decision: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (r *PostgresApprovalRepository) ListReviews(ctx context.Context, databaseID int64) ([]Review, error) {
	rows, err := r.pool.Query(ctx, `SELECT request_id FROM migration_review_requests q JOIN migrations m ON m.id=q.migration_id WHERE m.database_id=$1 ORDER BY q.created_at DESC`, databaseID)
	if err != nil {
		return nil, fmt.Errorf("migration: list reviews: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]Review, 0, len(ids))
	for _, id := range ids {
		v, err := r.GetReview(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

func (r *PostgresApprovalRepository) DecideReview(ctx context.Context, requestID string, actorID int64, revision, decision, comment string) (Review, error) {
	decision = strings.ToLower(strings.TrimSpace(decision))
	if decision != "approved" && decision != "rejected" {
		return Review{}, errors.New("migration: decision must be approved or rejected")
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Review{}, err
	}
	defer tx.Rollback(ctx)
	var currentRevision, status string
	if err := tx.QueryRow(ctx, `SELECT revision,status FROM migration_review_requests WHERE request_id=$1 FOR UPDATE`, requestID).Scan(&currentRevision, &status); errors.Is(err, pgx.ErrNoRows) {
		return Review{}, ErrReviewNotFound
	} else if err != nil {
		return Review{}, err
	}
	if currentRevision != revision {
		return Review{}, ErrRevisionChanged
	}
	if status != string(ReviewPending) {
		return Review{}, ErrReviewClosed
	}
	var groupID int64
	err = tx.QueryRow(ctx, `SELECT d.group_id
		FROM migration_review_decisions d
		JOIN migration_review_requests q ON q.request_id=d.request_id
		JOIN migrations m ON m.id=q.migration_id
		JOIN groups g ON g.id=d.group_id
		WHERE d.request_id=$1 AND g.leader_user_id=$2
		  AND EXISTS (SELECT 1 FROM group_db_access a WHERE a.group_id=d.group_id AND a.database_id=m.database_id)`, requestID, actorID).Scan(&groupID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Review{}, ErrNotGroupLeader
	}
	if err != nil {
		return Review{}, err
	}
	_, err = tx.Exec(ctx, `UPDATE migration_review_decisions SET decision=$3,comment=$4,decided_at=now() WHERE request_id=$1 AND group_id=$2`, requestID, groupID, decision, strings.TrimSpace(comment))
	if err != nil {
		return Review{}, fmt.Errorf("migration: save review decision: %w", err)
	}
	if decision == "rejected" {
		_, err = tx.Exec(ctx, `UPDATE migration_review_requests SET status='rejected',decided_at=now() WHERE request_id=$1`, requestID)
	} else {
		_, err = tx.Exec(ctx, `UPDATE migration_review_requests q SET status='approved',decided_at=now()
			WHERE q.request_id=$1 AND NOT EXISTS (SELECT 1 FROM migration_review_decisions d WHERE d.request_id=q.request_id AND d.decision IS DISTINCT FROM 'approved')`, requestID)
	}
	if err != nil {
		return Review{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Review{}, err
	}
	return r.GetReview(ctx, requestID)
}

func (r *PostgresApprovalRepository) EnqueueApproved(ctx context.Context, requestID string) (QueueJob, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return QueueJob{}, err
	}
	defer tx.Rollback(ctx)
	var status string
	if err := tx.QueryRow(ctx, `SELECT status FROM migration_review_requests WHERE request_id=$1 FOR UPDATE`, requestID).Scan(&status); errors.Is(err, pgx.ErrNoRows) {
		return QueueJob{}, ErrReviewNotFound
	} else if err != nil {
		return QueueJob{}, err
	}
	if status != string(ReviewApproved) {
		return QueueJob{}, ErrQueueNotApproved
	}
	if err := r.verifyCurrentAuthorization(ctx, requestID); err != nil {
		return QueueJob{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO migration_queue_jobs(request_id) VALUES($1) ON CONFLICT(request_id) DO NOTHING`, requestID)
	if err != nil {
		return QueueJob{}, fmt.Errorf("migration: enqueue review: %w", err)
	}
	_, err = tx.Exec(ctx, `UPDATE migration_review_requests SET enqueued_at=COALESCE(enqueued_at,now()) WHERE request_id=$1`, requestID)
	if err != nil {
		return QueueJob{}, err
	}
	var job QueueJob
	err = tx.QueryRow(ctx, `SELECT id,request_id,status,attempts,created_at,started_at FROM migration_queue_jobs WHERE request_id=$1`, requestID).Scan(&job.ID, &job.RequestID, &job.Status, &job.Attempts, &job.CreatedAt, &job.StartedAt)
	if err != nil {
		return QueueJob{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return QueueJob{}, err
	}
	return job, nil
}

func (r *PostgresApprovalRepository) ApprovedReview(ctx context.Context, migrationID int64, revision, operation string) (Review, error) {
	return r.findByRevisionStatus(ctx, migrationID, revision, operation, string(ReviewApproved))
}

func (r *PostgresApprovalRepository) findByRevisionStatus(ctx context.Context, migrationID int64, revision, operation, status string) (Review, error) {
	var id string
	err := r.pool.QueryRow(ctx, `SELECT request_id FROM migration_review_requests q
		JOIN migrations m ON m.id=q.migration_id
		WHERE q.migration_id=$1 AND q.revision=$2 AND q.operation=$3 AND q.status=$4
		  AND NOT EXISTS (SELECT 1 FROM group_db_access a
			WHERE a.database_id=m.database_id AND NOT EXISTS (SELECT 1 FROM migration_review_decisions d WHERE d.request_id=q.request_id AND d.group_id=a.group_id))
		  AND NOT EXISTS (SELECT 1 FROM migration_review_decisions d
			JOIN groups g ON g.id=d.group_id
			WHERE d.request_id=q.request_id
			  AND (g.leader_user_id IS DISTINCT FROM d.leader_user_id
				OR NOT EXISTS (SELECT 1 FROM group_db_access a WHERE a.group_id=d.group_id AND a.database_id=m.database_id)))`, migrationID, revision, operation, status).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Review{}, ErrApprovalRequired
	}
	if err != nil {
		return Review{}, err
	}
	return r.GetReview(ctx, id)
}

func (r *PostgresApprovalRepository) verifyCurrentAuthorization(ctx context.Context, requestID string) error {
	var ok bool
	err := r.pool.QueryRow(ctx, `SELECT NOT EXISTS (
		SELECT 1 FROM migration_review_requests q
		JOIN migrations m ON m.id=q.migration_id
		WHERE q.request_id=$1 AND (
			EXISTS (SELECT 1 FROM group_db_access a WHERE a.database_id=m.database_id AND NOT EXISTS (SELECT 1 FROM migration_review_decisions d WHERE d.request_id=q.request_id AND d.group_id=a.group_id))
			OR EXISTS (SELECT 1 FROM migration_review_decisions d JOIN groups g ON g.id=d.group_id
				WHERE d.request_id=q.request_id AND (g.leader_user_id IS DISTINCT FROM d.leader_user_id OR NOT EXISTS (SELECT 1 FROM group_db_access a WHERE a.group_id=d.group_id AND a.database_id=m.database_id)))
		)`, requestID).Scan(&ok)
	if err != nil {
		return fmt.Errorf("migration: recheck review authorization: %w", err)
	}
	if !ok {
		return ErrRevisionChanged
	}
	return nil
}

func CurrentRevision(m Migration) string { return Revision(m.Title, m.SQL) }

type ApprovalService interface {
	SubmitReview(ctx context.Context, userID, databaseID, migrationID int64, operation string) (Review, error)
	GetReview(ctx context.Context, userID int64, requestID string) (Review, error)
	ListReviews(ctx context.Context, userID, databaseID int64) ([]Review, error)
	DecideReview(ctx context.Context, userID int64, requestID, decision, comment string) (Review, error)
	EnqueueApproved(ctx context.Context, userID int64, requestID string) (QueueJob, error)
}

func (s *Service) SubmitReview(ctx context.Context, userID, databaseID, migrationID int64, operation string) (Review, error) {
	if s.approvals == nil {
		return Review{}, ErrApprovalsUnavailable
	}
	if _, err := s.requireLevel(ctx, userID, databaseID, access.LevelEdit); err != nil {
		return Review{}, err
	}
	op, err := normalizeOperation(operation)
	if err != nil {
		return Review{}, err
	}
	m, err := s.repo.GetByID(ctx, databaseID, migrationID)
	if err != nil {
		return Review{}, err
	}
	if m.Status != StatusDraft {
		return Review{}, ErrNotDraft
	}
	return s.approvals.SubmitReview(ctx, SubmitReviewParams{MigrationID: migrationID, DatabaseID: databaseID, Revision: CurrentRevision(m), Operation: op, RequestedBy: userID})
}

func (s *Service) GetReview(ctx context.Context, userID int64, requestID string) (Review, error) {
	if s.approvals == nil {
		return Review{}, ErrApprovalsUnavailable
	}
	review, err := s.approvals.GetReview(ctx, requestID)
	if err != nil {
		return Review{}, err
	}
	if _, err := s.requireLevel(ctx, userID, review.DatabaseID, access.LevelView); err != nil {
		return Review{}, err
	}
	return review, nil
}

func (s *Service) ListReviews(ctx context.Context, userID, databaseID int64) ([]Review, error) {
	if s.approvals == nil {
		return nil, ErrApprovalsUnavailable
	}
	if _, err := s.requireLevel(ctx, userID, databaseID, access.LevelView); err != nil {
		return nil, err
	}
	return s.approvals.ListReviews(ctx, databaseID)
}

func (s *Service) DecideReview(ctx context.Context, userID int64, requestID, decision, comment string) (Review, error) {
	if s.approvals == nil {
		return Review{}, ErrApprovalsUnavailable
	}
	review, err := s.approvals.GetReview(ctx, requestID)
	if err != nil {
		return Review{}, err
	}
	if _, err := s.requireLevel(ctx, userID, review.DatabaseID, access.LevelView); err != nil {
		return Review{}, err
	}
	m, err := s.repo.GetByID(ctx, review.DatabaseID, review.MigrationID)
	if err != nil {
		return Review{}, err
	}
	if CurrentRevision(m) != review.Revision {
		return Review{}, ErrRevisionChanged
	}
	return s.approvals.DecideReview(ctx, requestID, userID, review.Revision, decision, comment)
}

func (s *Service) EnqueueApproved(ctx context.Context, userID int64, requestID string) (QueueJob, error) {
	if s.approvals == nil {
		return QueueJob{}, ErrApprovalsUnavailable
	}
	review, err := s.GetReview(ctx, userID, requestID)
	if err != nil {
		return QueueJob{}, err
	}
	if review.Status != ReviewApproved {
		return QueueJob{}, ErrQueueNotApproved
	}
	return s.approvals.EnqueueApproved(ctx, requestID)
}

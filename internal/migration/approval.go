package migration

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/access"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/observability"
)

type ReviewStatus string

const (
	ReviewPending  ReviewStatus = "pending"
	ReviewApproved ReviewStatus = "approved"
	ReviewRejected ReviewStatus = "rejected"
)

type ReviewDecision struct {
	GroupID      int64      `json:"group_id"`
	GroupName    string     `json:"group_name"`
	LeaderUserID int64      `json:"leader_user_id"`
	Decision     string     `json:"decision,omitempty"`
	Comment      string     `json:"comment,omitempty"`
	DecidedAt    *time.Time `json:"decided_at,omitempty"`
}

type Review struct {
	Trace             observability.Propagation `json:"-"`
	RequestID         string                    `json:"request_id"`
	MigrationID       int64                     `json:"migration_id"`
	DatabaseID        int64                     `json:"database_id"`
	Revision          string                    `json:"revision"`
	Operation         string                    `json:"operation"`
	Mode              string                    `json:"mode"`
	TargetExecutionID string                    `json:"target_execution_id,omitempty"`
	RequestedBy       int64                     `json:"requested_by"`
	Status            ReviewStatus              `json:"status"`
	CreatedAt         time.Time                 `json:"created_at"`
	DecidedAt         *time.Time                `json:"decided_at,omitempty"`
	EnqueuedAt        *time.Time                `json:"enqueued_at,omitempty"`
	JobID             *int64                    `json:"job_id,omitempty"`
	Decisions         []ReviewDecision          `json:"decisions"`
}

type QueueJob struct {
	ID         int64     `json:"id"`
	RequestID  string    `json:"request_id"`
	Status     string    `json:"status"`
	Attempts   int       `json:"attempts"`
	CreatedAt  time.Time `json:"created_at"`
	DatabaseID int64     `json:"database_id"`
}

type SubmitReviewParams struct {
	MigrationID        int64
	DatabaseID         int64
	Revision           string
	Operation          string
	Mode               string
	RequestedBy        int64
	DefinitionRevision int
	TargetExecutionID  string
}

type ApprovalRepository interface {
	SubmitReview(ctx context.Context, p SubmitReviewParams) (Review, error)
	GetReview(ctx context.Context, requestID string) (Review, error)
	ListReviews(ctx context.Context, databaseID int64) ([]Review, error)
	FindByRevision(ctx context.Context, migrationID int64, revision, operation string, targetExecutionID *string) (Review, error)
	DecideReview(ctx context.Context, requestID string, actorID, groupID int64, revision, decision, comment string) (Review, error)
	ApprovedReview(ctx context.Context, migrationID int64, revision, operation, targetExecutionID string) (Review, error)
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
	ErrReviewGroupRequired  = errors.New("migration: group_id is required when deciding for multiple groups")
	ErrInvalidDecision      = errors.New("migration: decision must be approved or rejected")
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
	var definitionRevision int
	if err := tx.QueryRow(ctx, `SELECT status, revision FROM migrations WHERE id=$1 AND database_id=$2 FOR NO KEY UPDATE`, p.MigrationID, p.DatabaseID).Scan(&status, &definitionRevision); errors.Is(err, pgx.ErrNoRows) {
		return Review{}, ErrNotFound
	} else if err != nil {
		return Review{}, fmt.Errorf("migration: review migration: %w", err)
	} else if op == string(OpApply) && status != string(StatusDraft) {
		return Review{}, ErrNotDraft
	} else if definitionRevision != p.DefinitionRevision {
		return Review{}, ErrRevisionChanged
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

	if op != string(OpApply) {
		_, err := tx.Exec(ctx, `UPDATE migration_review_requests q SET superseded_at=clock_timestamp()
			FROM migration_async_jobs j
			WHERE q.migration_id=$1 AND q.revision=$2 AND q.operation=$3 AND q.target_execution_id=$4
			  AND q.superseded_at IS NULL AND q.async_job_id=j.id AND (j.state='failed' OR j.batch_paused)`,
			p.MigrationID, p.Revision, op, p.TargetExecutionID)
		if err != nil {
			return Review{}, fmt.Errorf("migration: retire failed operation review: %w", err)
		}
	}

	requestID := newRequestID()
	trace := observability.Capture(ctx)
	tag, err := tx.Exec(ctx, `INSERT INTO migration_review_requests
		(request_id,migration_id,revision,operation,requested_by,target_execution_id,mode,traceparent,tracestate,correlation_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT (migration_id,revision,operation,target_execution_id) WHERE superseded_at IS NULL DO NOTHING`,
		requestID, p.MigrationID, p.Revision, op, p.RequestedBy, p.TargetExecutionID, p.Mode, trace.Traceparent, trace.Tracestate, trace.CorrelationID)
	if err != nil {
		return Review{}, fmt.Errorf("migration: create review: %w", err)
	}
	if tag.RowsAffected() == 0 {
		if err := tx.Commit(ctx); err != nil {
			return Review{}, err
		}
		return r.FindByRevision(ctx, p.MigrationID, p.Revision, op, &p.TargetExecutionID)
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

func (r *PostgresApprovalRepository) FindByRevision(ctx context.Context, migrationID int64, revision, operation string, targetExecutionID *string) (Review, error) {
	var requestID string
	err := r.pool.QueryRow(ctx, `SELECT request_id FROM migration_review_requests
		WHERE migration_id=$1 AND revision=$2 AND operation=$3 AND superseded_at IS NULL
		  AND ($4::text IS NULL OR target_execution_id=$4)
		  AND (operation='apply' OR target_execution_id<>'')
		ORDER BY created_at DESC, request_id DESC LIMIT 1`, migrationID, revision, operation, targetExecutionID).Scan(&requestID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Review{}, ErrReviewNotFound
	}
	if err != nil {
		return Review{}, err
	}
	return r.GetReview(ctx, requestID)
}

const reviewColumns = `q.request_id,q.migration_id,m.database_id,q.revision,q.operation,q.requested_by,
	q.target_execution_id,q.mode,q.status,q.created_at,q.decided_at,CASE WHEN q.async_job_id IS NOT NULL THEN q.enqueued_at END,q.async_job_id,q.traceparent,q.tracestate,q.correlation_id`

func (r *PostgresApprovalRepository) GetReview(ctx context.Context, requestID string) (Review, error) {
	var out Review
	var status string
	err := r.pool.QueryRow(ctx, `SELECT `+reviewColumns+` FROM migration_review_requests q JOIN migrations m ON m.id=q.migration_id WHERE q.request_id=$1`, requestID).Scan(&out.RequestID, &out.MigrationID, &out.DatabaseID, &out.Revision, &out.Operation, &out.RequestedBy, &out.TargetExecutionID, &out.Mode, &status, &out.CreatedAt, &out.DecidedAt, &out.EnqueuedAt, &out.JobID, &out.Trace.Traceparent, &out.Trace.Tracestate, &out.Trace.CorrelationID)
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
	rows, err := r.pool.Query(ctx, `SELECT d.group_id,g.name,d.leader_user_id,COALESCE(d.decision,''),d.comment,d.decided_at
		FROM migration_review_decisions d JOIN groups g ON g.id=d.group_id
		WHERE d.request_id=$1 ORDER BY g.name,d.group_id`, requestID)
	if err != nil {
		return nil, fmt.Errorf("migration: list review decisions: %w", err)
	}
	defer rows.Close()
	var out []ReviewDecision
	for rows.Next() {
		var d ReviewDecision
		if err := rows.Scan(&d.GroupID, &d.GroupName, &d.LeaderUserID, &d.Decision, &d.Comment, &d.DecidedAt); err != nil {
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

func (r *PostgresApprovalRepository) DecideReview(ctx context.Context, requestID string, actorID, groupID int64, revision, decision, comment string) (Review, error) {
	decision = strings.ToLower(strings.TrimSpace(decision))
	if decision != "approved" && decision != "rejected" {
		return Review{}, ErrInvalidDecision
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
	// Lock the reviewed definition until the decision commits. A concurrent
	// edit can revoke the approval afterward, but cannot race this check.
	var definition Migration
	err = tx.QueryRow(ctx, `SELECT m.title,m.sql,m.down_sql,m.irreversible_reason,m.batch_config
		FROM migrations m JOIN migration_review_requests q ON q.migration_id=m.id
		WHERE q.request_id=$1 FOR SHARE OF m`, requestID).
		Scan(&definition.Title, &definition.SQL, &definition.DownSQL, &definition.IrreversibleReason, &definition.BatchConfig)
	if err != nil {
		return Review{}, err
	}
	if CurrentRevision(definition) != revision {
		return Review{}, ErrRevisionChanged
	}
	rows, err := tx.Query(ctx, `SELECT d.group_id
		FROM migration_review_decisions d
		JOIN migration_review_requests q ON q.request_id=d.request_id
		JOIN migrations m ON m.id=q.migration_id
		JOIN groups g ON g.id=d.group_id
		JOIN group_db_access a ON a.group_id=g.id AND a.database_id=m.database_id
		JOIN user_groups ug ON ug.group_id=g.id AND ug.user_id=g.leader_user_id
		JOIN users u ON u.id=ug.user_id AND u.is_active
		WHERE d.request_id=$1 AND g.leader_user_id=$2 AND d.leader_user_id=$2
		  AND ($3::bigint=0 OR d.group_id=$3)
		ORDER BY d.group_id LIMIT 2`, requestID, actorID, groupID)
	if err != nil {
		return Review{}, err
	}
	groups, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return Review{}, err
	}
	if len(groups) == 0 {
		return Review{}, ErrNotGroupLeader
	}
	if len(groups) > 1 {
		return Review{}, ErrReviewGroupRequired
	}
	_, err = tx.Exec(ctx, `UPDATE migration_review_decisions SET decision=$3,comment=$4,decided_at=now()
		WHERE request_id=$1 AND group_id=$2`, requestID, groups[0], decision, strings.TrimSpace(comment))
	if err != nil {
		return Review{}, fmt.Errorf("migration: save review decision: %w", err)
	}
	if decision == "rejected" {
		_, err = tx.Exec(ctx, `UPDATE migration_review_requests SET status='rejected',decided_at=now() WHERE request_id=$1`, requestID)
	} else {
		trace := observability.Capture(ctx)
		_, err = tx.Exec(ctx, `UPDATE migration_review_requests q SET status='approved',decided_at=now(),
			traceparent=$2,tracestate=$3,correlation_id=$4
			FROM migrations m WHERE q.migration_id=m.id AND q.request_id=$1 AND `+allCurrentGroupsApproved,
			requestID, trace.Traceparent, trace.Tracestate, trace.CorrelationID)
	}
	if err != nil {
		return Review{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Review{}, err
	}
	return r.GetReview(ctx, requestID)
}

func (r *PostgresApprovalRepository) PendingApprovedOperations(ctx context.Context) ([]Review, error) {
	rows, err := r.pool.Query(ctx, `SELECT q.request_id,q.migration_id,m.database_id,q.revision,q.operation,q.requested_by,q.target_execution_id,q.mode,
		q.traceparent,q.tracestate,q.correlation_id
		FROM migration_review_requests q JOIN migrations m ON m.id=q.migration_id
		WHERE q.status='approved' AND q.async_job_id IS NULL AND q.superseded_at IS NULL
		  AND (q.operation='apply' OR (q.operation IN ('rollback','forward','batch_recovery','compensation') AND q.target_execution_id<>''))
		ORDER BY q.decided_at, q.request_id`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Review, error) {
		var review Review
		err := row.Scan(&review.RequestID, &review.MigrationID, &review.DatabaseID, &review.Revision,
			&review.Operation, &review.RequestedBy, &review.TargetExecutionID, &review.Mode,
			&review.Trace.Traceparent, &review.Trace.Tracestate, &review.Trace.CorrelationID)
		return review, err
	})
}

func (r *PostgresApprovalRepository) AcknowledgeDispatch(ctx context.Context, requestID string, jobID int64) error {
	tag, err := r.pool.Exec(ctx, `UPDATE migration_review_requests q
		SET async_job_id=j.id, enqueued_at=CASE WHEN q.async_job_id IS NULL THEN clock_timestamp() ELSE q.enqueued_at END
		FROM migration_async_jobs j
		WHERE q.request_id=$1 AND q.status='approved' AND j.id=$2 AND j.request_id=q.request_id
		  AND (q.async_job_id IS NULL OR q.async_job_id=j.id)`, requestID, jobID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrQueueNotApproved
	}
	return nil
}

// Used both when a review becomes approved and when the worker rechecks it.
const allCurrentGroupsApproved = `EXISTS (SELECT 1 FROM group_db_access a WHERE a.database_id=m.database_id)
		  AND NOT EXISTS (
			SELECT 1 FROM group_db_access a
			JOIN groups g ON g.id=a.group_id
			LEFT JOIN migration_review_decisions d ON d.request_id=q.request_id AND d.group_id=a.group_id
			WHERE a.database_id=m.database_id
			  AND (d.decision IS DISTINCT FROM 'approved'
				OR g.leader_user_id IS DISTINCT FROM d.leader_user_id
				OR NOT EXISTS (
					SELECT 1 FROM user_groups ug JOIN users u ON u.id=ug.user_id AND u.is_active
					WHERE ug.group_id=g.id AND ug.user_id=g.leader_user_id)))
		  AND NOT EXISTS (
			SELECT 1 FROM migration_review_decisions d WHERE d.request_id=q.request_id
			AND NOT EXISTS (SELECT 1 FROM group_db_access a WHERE a.group_id=d.group_id AND a.database_id=m.database_id))`

func (r *PostgresApprovalRepository) ApprovedReview(ctx context.Context, migrationID int64, revision, operation, targetExecutionID string) (Review, error) {
	var id string
	err := r.pool.QueryRow(ctx, `SELECT q.request_id FROM migration_review_requests q
		JOIN migrations m ON m.id=q.migration_id
		WHERE q.migration_id=$1 AND q.revision=$2 AND q.operation=$3 AND q.target_execution_id=$4 AND q.status='approved'
		  AND q.superseded_at IS NULL AND `+allCurrentGroupsApproved,
		migrationID, revision, operation, targetExecutionID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Review{}, ErrApprovalRequired
	}
	if err != nil {
		return Review{}, err
	}
	return r.GetReview(ctx, id)
}

func CurrentRevision(m Migration) string {
	def := m.SQL + "\x00"
	switch {
	case m.DownSQL != nil:
		def += "down:" + *m.DownSQL
	case m.IrreversibleReason != nil:
		def += "irreversible:" + *m.IrreversibleReason
	}
	if m.BatchConfig != nil {
		config, _ := json.Marshal(m.BatchConfig)
		def += "\x00batch:" + string(config)
	}
	return Revision(m.Title, def)
}

func (s *Service) SubmitReview(ctx context.Context, userID, databaseID, migrationID int64, operation string, revision int, targetExecutionID string) (Review, error) {
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
	if revision != 0 && revision != m.Revision {
		return Review{}, ErrRevisionMismatch
	}
	plan, err := s.planReview(ctx, m, Operation(op), targetExecutionID)
	if err != nil {
		return Review{}, err
	}
	if targetExecutionID != "" && targetExecutionID != plan.TargetExecutionID {
		return Review{}, ErrOperationTargetChanged
	}
	return s.approvals.SubmitReview(ctx, SubmitReviewParams{
		MigrationID: migrationID, DatabaseID: databaseID, Revision: CurrentRevision(m), Operation: op,
		RequestedBy: userID, DefinitionRevision: m.Revision, TargetExecutionID: plan.TargetExecutionID, Mode: plan.Mode(),
	})
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

type OperationReview struct {
	Review            *Review `json:"review"`
	Plan              *Plan   `json:"plan"`
	UnavailableReason string  `json:"unavailable_reason,omitempty"`
}

func (s *Service) planReview(ctx context.Context, m Migration, op Operation, executionID string) (Plan, error) {
	if op.IsRecovery() {
		return s.planRecovery(ctx, m, op, executionID)
	}
	if !op.IsOrdinary() {
		return Plan{}, ErrInvalidOperation
	}
	if op == OpApply && m.Status != StatusDraft {
		return Plan{}, ErrNotDraft
	}
	if s.history == nil {
		return Plan{}, ErrHistoryUnavailable
	}
	return s.planForDefinition(ctx, m, op)
}

func (s *Service) CurrentOperationReview(ctx context.Context, userID, databaseID, migrationID int64, revision int, operation string) (OperationReview, error) {
	if s.approvals == nil {
		return OperationReview{}, ErrApprovalsUnavailable
	}
	m, err := s.GetDetail(ctx, userID, databaseID, migrationID)
	if err != nil {
		return OperationReview{}, err
	}
	if revision != 0 && revision != m.Revision {
		return OperationReview{}, ErrRevisionMismatch
	}
	op, err := normalizeOperation(operation)
	if err != nil || (!Operation(op).IsOrdinary() && !Operation(op).IsRecovery()) {
		return OperationReview{}, ErrInvalidOperation
	}

	var result OperationReview
	var target *string
	plan, err := s.planReview(ctx, m, Operation(op), "")
	switch {
	case err == nil:
		result.Plan = &plan
		target = &plan.TargetExecutionID
	case errors.Is(err, ErrNotDraft), errors.Is(err, ErrAlreadyApplied), errors.Is(err, ErrIrreversible),
		errors.Is(err, ErrNothingToForward), errors.Is(err, ErrNotLatestActive), errors.Is(err, ErrImmutable),
		errors.Is(err, ErrRecoveryUnavailable), errors.Is(err, ErrCompensationUnsafe), errors.Is(err, ErrBatchDirection):
		result.UnavailableReason = err.Error()
	default:
		return OperationReview{}, err
	}
	review, err := s.approvals.FindByRevision(ctx, m.ID, CurrentRevision(m), op, target)
	if errors.Is(err, ErrReviewNotFound) {
		return result, nil
	}
	if err != nil {
		return OperationReview{}, err
	}
	result.Review = &review
	return result, nil
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

func (s *Service) DecideReview(ctx context.Context, userID int64, requestID string, groupID int64, decision, comment string) (Review, error) {
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
	if review.Operation != string(OpApply) {
		m, err := s.repo.GetByID(ctx, review.DatabaseID, review.MigrationID)
		if err != nil {
			return Review{}, err
		}
		plan, err := s.planReview(ctx, m, Operation(review.Operation), review.TargetExecutionID)
		if err != nil {
			return Review{}, err
		}
		if review.TargetExecutionID != plan.TargetExecutionID {
			return Review{}, ErrOperationTargetChanged
		}
	}
	return s.approvals.DecideReview(ctx, requestID, userID, groupID, review.Revision, decision, comment)
}

func (s *Service) ReviewForDispatch(ctx context.Context, userID int64, requestID string) (Review, error) {
	review, err := s.GetReview(ctx, userID, requestID)
	if err != nil {
		return Review{}, err
	}
	if _, err := s.requireLevel(ctx, userID, review.DatabaseID, access.LevelEdit); err != nil {
		return Review{}, err
	}
	if review.Status != ReviewApproved {
		return Review{}, ErrQueueNotApproved
	}
	return review, nil
}

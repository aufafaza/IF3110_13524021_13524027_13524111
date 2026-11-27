package migrationjob

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/migration"
)

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

const jobColumns = `id, execution_id::text, database_id, migration_id, revision, operation, target_execution_id,
	state, attempt_count, coalesce(failure_code, ''), created_at, updated_at,
	mode, scope_rows, committed_batches, committed_rows, batch_paused, compensated_batches, compensated_rows`

func jobFields(j *Job) []any {
	return []any{&j.ID, &j.ExecutionID, &j.DatabaseID, &j.MigrationID, &j.Revision, &j.Operation, &j.TargetExecutionID,
		&j.State, &j.Attempts, &j.FailureCode, &j.CreatedAt, &j.UpdatedAt,
		&j.Mode, &j.ScopeRows, &j.CommittedBatches, &j.CommittedRows, &j.BatchPaused, &j.CompensatedBatches, &j.CompensatedRows}
}

func (s *Store) Get(ctx context.Context, databaseID, jobID int64) (Job, error) {
	var j Job
	err := s.pool.QueryRow(ctx, `SELECT `+jobColumns+`
		FROM migration_async_jobs WHERE database_id = $1 AND id = $2`, databaseID, jobID).Scan(jobFields(&j)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return Job{}, ErrNotFound
	}
	return j, err
}

func (s *Store) enqueue(ctx context.Context, a Approval) (Job, error) {
	if a.Mode == "" {
		a.Mode = Ordinary
	}
	op := migration.Operation(a.Operation)
	validMode := op.IsOrdinary() && (a.Mode == Ordinary || (op == migration.OpApply && a.Mode == Batch)) ||
		op.IsRecovery() && a.Mode == Batch
	if strings.TrimSpace(a.RequestID) == "" || strings.TrimSpace(a.Revision) == "" ||
		a.ActorID <= 0 || a.DatabaseID <= 0 || a.MigrationID <= 0 || !validMode ||
		((a.Operation == Apply) != (a.TargetExecutionID == "")) {
		return Job{}, errors.New("invalid approval for migration job")
	}

	_, err := s.pool.Exec(ctx, `INSERT INTO migration_async_jobs
		(request_id, actor_id, database_id, migration_id, revision, operation, target_execution_id, traceparent, tracestate, mode, correlation_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11) ON CONFLICT (request_id) DO NOTHING`,
		a.RequestID, a.ActorID, a.DatabaseID, a.MigrationID, a.Revision, a.Operation, a.TargetExecutionID, a.Traceparent, a.Tracestate, a.Mode, a.CorrelationID)
	if err != nil {
		return Job{}, err
	}

	var j Job
	var actorID int64
	err = s.pool.QueryRow(ctx, `SELECT `+jobColumns+`, actor_id FROM migration_async_jobs WHERE request_id = $1`,
		a.RequestID).Scan(append(jobFields(&j), &actorID)...)
	if err != nil {
		return Job{}, err
	}
	if actorID != a.ActorID || j.DatabaseID != a.DatabaseID || j.MigrationID != a.MigrationID ||
		j.Revision != a.Revision || j.Operation != a.Operation || j.Mode != a.Mode || j.TargetExecutionID != a.TargetExecutionID {
		return Job{}, ErrApprovalMismatch
	}
	return j, nil
}

type claim struct {
	Job
	approval      Approval
	token         string
	attemptLimit  int
	batchFailures int
	batchPending  bool
}

const terminalStates = `('succeeded', 'failed', 'recovered', 'compensated')`

const ownsRecoveryRoot = `j.operation IN ('batch_recovery', 'compensation') AND EXISTS (
	SELECT 1 FROM migration_async_jobs root
	WHERE root.execution_id::text=j.target_execution_id AND root.database_id=j.database_id
	  AND root.migration_id=j.migration_id AND root.owns_database AND root.batch_paused
	  AND root.operation='apply' AND root.mode='batch'
)`

const holdsExecutionGate = `(j.owns_database OR (` + ownsRecoveryRoot + `))`

func (s *Store) claim(ctx context.Context, lease time.Duration) (claim, error) {
	var c claim
	err := s.pool.QueryRow(ctx, `UPDATE migration_async_jobs j SET
		claim_token = gen_random_uuid(), lease_until = clock_timestamp() + $1::bigint * interval '1 microsecond',
		state = CASE WHEN state IN ('queued', 'retrying') THEN 'running' ELSE state END,
		owns_database = NOT (`+ownsRecoveryRoot+`), updated_at = clock_timestamp()
		WHERE id = (
			SELECT j.id FROM migration_async_jobs j
			WHERE j.state NOT IN `+terminalStates+` AND NOT j.batch_paused
			  AND j.eligible_at <= clock_timestamp()
			  AND (j.lease_until IS NULL OR j.lease_until <= clock_timestamp())
			  AND (
				  (j.operation IN ('batch_recovery', 'compensation') AND NOT EXISTS (
					  SELECT 1 FROM migration_async_jobs sibling
					  WHERE sibling.database_id=j.database_id AND sibling.target_execution_id=j.target_execution_id
					    AND sibling.operation IN ('batch_recovery', 'compensation') AND sibling.id<j.id
					    AND sibling.state NOT IN `+terminalStates+` AND NOT sibling.batch_paused
				  ))
				  OR (j.operation NOT IN ('batch_recovery', 'compensation') AND (j.owns_database OR NOT EXISTS (
					  SELECT 1 FROM migration_async_jobs earlier
					  WHERE earlier.database_id=j.database_id AND earlier.id<j.id
					    AND earlier.state NOT IN `+terminalStates+`
				  )))
			  )
			  AND NOT EXISTS (
				  SELECT 1 FROM migration_async_jobs owner
				  WHERE owner.database_id=j.database_id AND owner.owns_database AND owner.id<>j.id
				    AND (j.operation NOT IN ('batch_recovery', 'compensation')
				      OR owner.execution_id::text<>j.target_execution_id
				      OR owner.migration_id<>j.migration_id OR NOT owner.batch_paused)
			  )
			ORDER BY j.eligible_at, j.id
			LIMIT 1 FOR UPDATE OF j SKIP LOCKED
		)
		RETURNING `+jobColumns+`, request_id, actor_id, traceparent, tracestate, correlation_id, claim_token::text, attempt_limit, batch_failures, batch_pending`,
		lease.Microseconds()).Scan(append(jobFields(&c.Job), &c.approval.RequestID, &c.approval.ActorID,
		&c.approval.Traceparent, &c.approval.Tracestate, &c.approval.CorrelationID, &c.token, &c.attemptLimit, &c.batchFailures, &c.batchPending)...)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "migration_async_database_owner" {
		return claim{}, pgx.ErrNoRows
	}
	c.approval.DatabaseID, c.approval.MigrationID = c.DatabaseID, c.MigrationID
	c.approval.Revision, c.approval.Operation = c.Revision, c.Operation
	c.approval.TargetExecutionID = c.TargetExecutionID
	if c.Mode == Batch {
		c.approval.Mode = c.Mode
	}
	return c, err
}

type attempt struct {
	id          string
	targetID    string
	planHash    string
	sql         string
	startedAt   time.Time
	finishedAt  *time.Time
	failureCode string
	retryable   bool
	deliveredAt *time.Time
	batchPlan   *BatchPlan
	rollbackOf  *string
}

func (s *Store) currentAttempt(ctx context.Context, c claim) (attempt, error) {
	var a attempt
	if c.Attempts == 0 {
		return a, nil
	}
	err := s.pool.QueryRow(ctx, `SELECT id::text, target_id::text, plan_hash, sql_snapshot, started_at,
		finished_at, coalesce(failure_code, ''), retryable, delivered_at, batch_plan, rollback_of_execution_id
		FROM migration_async_attempts WHERE job_id = $1 AND number = $2`, c.ID, c.Attempts).
		Scan(&a.id, &a.targetID, &a.planHash, &a.sql, &a.startedAt, &a.finishedAt, &a.failureCode, &a.retryable, &a.deliveredAt, &a.batchPlan, &a.rollbackOf)
	return a, err
}

func (s *Store) checkClaim(ctx context.Context, tx pgx.Tx, c claim) error {
	var id int64
	err := tx.QueryRow(ctx, `SELECT id FROM migration_async_jobs j
		WHERE id = $1 AND claim_token = $2::uuid AND lease_until > clock_timestamp() AND `+holdsExecutionGate+`
		FOR UPDATE`, c.ID, c.token).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return errClaimLost
	}
	return err
}

func (s *Store) startAttempt(ctx context.Context, c claim, targetID, planHash, sql string, batchPlan []byte, rollbackOf *string) (attempt, error) {
	var a attempt
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := s.checkClaim(ctx, tx, c); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `INSERT INTO migration_async_attempts
			(job_id, number, target_id, plan_hash, sql_snapshot, batch_plan, rollback_of_execution_id)
			VALUES ($1, $2, $3::uuid, $4, $5, $6, $7) RETURNING id::text, started_at`,
			c.ID, c.Attempts+1, targetID, planHash, sql, batchPlan, rollbackOf).Scan(&a.id, &a.startedAt); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE migration_async_jobs SET
			attempt_count = attempt_count + 1, failure_code = NULL, updated_at = clock_timestamp()
			WHERE id = $1`, c.ID)
		return err
	})
	a.planHash = planHash
	return a, err
}

func (s *Store) finishAttempt(ctx context.Context, c claim, finishedAt time.Time, code string, retryable bool) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := s.checkClaim(ctx, tx, c); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `UPDATE migration_async_attempts
			SET finished_at = $3, failure_code = nullif($4, ''), retryable = $5
			WHERE job_id = $1 AND number = $2 AND finished_at IS NULL`, c.ID, c.Attempts, finishedAt, code, retryable)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return errors.New("expected unfinished migration attempt")
		}
		return releaseClaim(ctx, tx, c, Reporting, code, 0)
	})
}

func (s *Store) acknowledgeHistory(ctx context.Context, c claim, a attempt) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := s.checkClaim(ctx, tx, c); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `UPDATE migration_async_attempts SET delivered_at = clock_timestamp()
			WHERE job_id = $1 AND number = $2 AND finished_at IS NOT NULL AND delivered_at IS NULL`, c.ID, c.Attempts)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return errors.New("expected undelivered migration outcome")
		}

		switch {
		case a.failureCode == "" && migration.Operation(c.Operation).IsRecovery():
			return s.settleRecovery(ctx, tx, c)
		case a.failureCode == "":
			return releaseClaim(ctx, tx, c, Succeeded, "", 0)
		case c.Mode == Batch && c.CommittedBatches > 0:
			if _, err := tx.Exec(ctx, `UPDATE migration_async_jobs SET batch_paused = true WHERE id = $1`, c.ID); err != nil {
				return err
			}
			return releaseClaim(ctx, tx, c, batchState(c.CommittedBatches, c.CompensatedBatches, Partial), a.failureCode, 0)
		case a.retryable && c.Attempts < c.attemptLimit:
			return releaseClaim(ctx, tx, c, Retrying, a.failureCode, retryDelay*time.Duration(1<<(c.Attempts-1)))
		default:
			return releaseClaim(ctx, tx, c, Failed, a.failureCode, 0)
		}
	})
}

func (s *Store) reschedule(ctx context.Context, c claim, state State, code string, delay time.Duration) error {
	return releaseClaim(ctx, s.pool, c, state, code, delay)
}

func releaseClaim(ctx context.Context, db interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}, c claim, state State, code string, delay time.Duration) error {
	tag, err := db.Exec(ctx, `UPDATE migration_async_jobs j SET
		state = $3, failure_code = nullif($4, ''), claim_token = NULL, lease_until = NULL,
		owns_database = owns_database AND $3 NOT IN `+terminalStates+`,
		eligible_at = clock_timestamp() + $5::bigint * interval '1 microsecond', updated_at = clock_timestamp()
		WHERE id = $1 AND claim_token = $2::uuid AND lease_until > clock_timestamp() AND `+holdsExecutionGate,
		c.ID, c.token, state, code, delay.Microseconds())
	if err != nil {
		return fmt.Errorf("update migration job: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return errClaimLost
	}
	return nil
}

package migrationjob

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/migration"
)

func (s *Store) markBatchPending(ctx context.Context, c claim) error {
	tag, err := s.pool.Exec(ctx, `UPDATE migration_async_jobs j SET batch_pending = true
		WHERE id = $1 AND claim_token = $2::uuid AND lease_until > clock_timestamp() AND `+holdsExecutionGate,
		c.ID, c.token)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errClaimLost
	}
	return nil
}

func (s *Store) projectBatch(ctx context.Context, c claim, p batchProgress, release bool) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := s.checkClaim(ctx, tx, c); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE migration_async_jobs SET scope_rows = $2, committed_batches = $3,
			committed_rows = $4, compensated_batches=$5, compensated_rows=$6, batch_failures = 0, batch_pending = false, updated_at = clock_timestamp()
			WHERE id = $1`, c.ID, p.scope, p.batches, p.rows, p.compensatedBatches, p.compensatedRows)
		if err != nil {
			return err
		}
		if migration.Operation(c.Operation).IsRecovery() {
			_, err = tx.Exec(ctx, `UPDATE migration_async_jobs SET committed_batches=$2, committed_rows=$3,
				compensated_batches=$4, compensated_rows=$5, updated_at=clock_timestamp()
				WHERE execution_id::text=$1 AND owns_database AND batch_paused`,
				c.TargetExecutionID, p.batches, p.rows, p.compensatedBatches, p.compensatedRows)
		}
		if err != nil || !release {
			return err
		}
		return releaseClaim(ctx, tx, c, batchState(p.batches, p.compensatedBatches, Running), "", 0)
	})
}

func (s *Store) retryBatch(ctx context.Context, c claim, code string) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := s.checkClaim(ctx, tx, c); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE migration_async_jobs SET batch_failures = batch_failures + 1, batch_pending = false
			WHERE id = $1`, c.ID); err != nil {
			return err
		}
		return releaseClaim(ctx, tx, c, batchState(c.CommittedBatches, c.CompensatedBatches, Retrying), code,
			retryDelay*time.Duration(1<<c.batchFailures))
	})
}

func batchState(committed, compensated int64, fallback State) State {
	if compensated > 0 {
		return PartiallyReverted
	}
	if committed > 0 {
		return Partial
	}
	return fallback
}

func (s *Store) settleRecovery(ctx context.Context, tx pgx.Tx, c claim) error {
	if err := releaseClaim(ctx, tx, c, Succeeded, "", 0); err != nil {
		return err
	}
	state := Recovered
	if c.Operation == string(migration.OpCompensation) {
		state = Compensated
	}
	tag, err := tx.Exec(ctx, `UPDATE migration_async_jobs SET state=$2, owns_database=false, batch_paused=false,
		batch_pending=false, updated_at=clock_timestamp()
		WHERE execution_id::text=$1 AND owns_database AND batch_paused`, c.TargetExecutionID, state)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errors.New("migration job: batch recovery owner changed")
	}
	return nil
}

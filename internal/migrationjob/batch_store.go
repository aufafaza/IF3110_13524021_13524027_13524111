package migrationjob

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

func (s *Store) markBatchPending(ctx context.Context, c claim) error {
	tag, err := s.pool.Exec(ctx, `UPDATE migration_async_jobs SET batch_pending = true
		WHERE id = $1 AND claim_token = $2::uuid AND lease_until > clock_timestamp() AND owns_database`,
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
			committed_rows = $4, batch_failures = 0, batch_pending = false, updated_at = clock_timestamp()
			WHERE id = $1`, c.ID, p.scope, p.batches, p.rows)
		if err != nil || !release {
			return err
		}
		return releaseClaim(ctx, tx, c, batchState(p.batches, Running), "", 0)
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
		return releaseClaim(ctx, tx, c, batchState(c.CommittedBatches, Retrying), code,
			retryDelay*time.Duration(1<<c.batchFailures))
	})
}

func batchState(committed int64, fallback State) State {
	if committed > 0 {
		return Partial
	}
	return fallback
}

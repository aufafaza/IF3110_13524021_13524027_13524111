package migration

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
)

const MaxBatchSize = 10_000

type BatchTable struct {
	Schema string `json:"schema"`
	Table  string `json:"table"`
	Key    string `json:"key"`
}

type BatchConfig struct {
	ScopeSQL string      `json:"scope_sql"`
	Size     int         `json:"size"`
	Recovery *BatchTable `json:"recovery,omitempty"`
}

var (
	ErrInvalidBatch        = errors.New("migration: batch needs a single scope SELECT and a size between 1 and 10000")
	ErrRecoveryUnavailable = errors.New("migration: no partial batch execution is available for this recovery")
	ErrCompensationUnsafe  = errors.New("migration: safe compensation is unavailable; create a corrective migration")
	ErrBatchDirection      = errors.New("migration: this batched definition requires batch recovery")
)

func ValidateBatchConfig(c *BatchConfig, up string, down *string) error {
	if c == nil {
		return nil
	}
	if c.Size < 1 || c.Size > MaxBatchSize || len(SplitStatements(c.ScopeSQL)) != 1 ||
		ValidateSQL(c.ScopeSQL) != nil || len(SplitStatements(up)) != 1 ||
		(down != nil && len(SplitStatements(*down)) != 1) {
		return ErrInvalidBatch
	}
	if c.Recovery != nil {
		for _, name := range []string{c.Recovery.Schema, c.Recovery.Table, c.Recovery.Key} {
			if strings.TrimSpace(name) == "" || strings.ContainsRune(name, 0) {
				return ErrInvalidBatch
			}
		}
	}
	return nil
}

type RecoveryPosition struct {
	ExecutionID        string
	CompensatedBatches int64
}

func (r *PostgresRepository) RecoveryPosition(ctx context.Context, m Migration, executionID string) (RecoveryPosition, error) {
	var p RecoveryPosition
	err := r.pool.QueryRow(ctx, `SELECT execution_id::text, compensated_batches
		FROM migration_async_jobs
		WHERE database_id=$1 AND migration_id=$2 AND revision=$3
		  AND operation='apply' AND mode='batch' AND owns_database AND batch_paused
		  AND state='partial' AND committed_batches > 0 AND ($4='' OR execution_id::text=$4)`,
		m.DatabaseID, m.ID, CurrentRevision(m), executionID).Scan(&p.ExecutionID, &p.CompensatedBatches)
	if errors.Is(err, pgx.ErrNoRows) {
		return RecoveryPosition{}, ErrRecoveryUnavailable
	}
	return p, err
}

func (s *Service) planRecovery(ctx context.Context, m Migration, op Operation, executionID string) (Plan, error) {
	r, ok := s.repo.(interface {
		RecoveryPosition(context.Context, Migration, string) (RecoveryPosition, error)
	})
	if !ok || m.BatchConfig == nil {
		return Plan{}, ErrRecoveryUnavailable
	}
	p, err := r.RecoveryPosition(ctx, m, executionID)
	if err != nil {
		return Plan{}, err
	}
	sql := m.SQL
	if op == OpCompensation {
		if m.DownSQL == nil || m.BatchConfig.Recovery == nil {
			return Plan{}, ErrCompensationUnsafe
		}
		sql = *m.DownSQL
	} else if p.CompensatedBatches > 0 {
		return Plan{}, ErrRecoveryUnavailable
	}
	return Plan{Operation: op, MigrationID: m.ID, Revision: m.Revision,
		SQL: sql, TargetExecutionID: p.ExecutionID, BatchConfig: m.BatchConfig}, nil
}

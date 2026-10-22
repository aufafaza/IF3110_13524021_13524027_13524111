package migration

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type CreateDraftParams struct {
	DatabaseID int64
	Title      string
	SQL        string
	CreatedBy  int64
}

type UpdateDraftParams struct {
	ID         int64
	DatabaseID int64
	Title      string
	SQL        string
}

type RecordExecutionParams struct {
	ID           int64
	DatabaseID   int64
	Version      int
	Status       Status
	ExecutedBy   int64
	ExecutedAt   time.Time
	ErrorMessage *string
}

type Repository interface {
	CreateDraft(ctx context.Context, params CreateDraftParams) (Migration, error)
	UpdateDraft(ctx context.Context, params UpdateDraftParams) (Migration, error)
	GetByID(ctx context.Context, databaseID, migrationID int64) (Migration, error)
	ListByDatabase(ctx context.Context, databaseID int64) ([]Migration, error)
	NextVersion(ctx context.Context, databaseID int64) (int, error)
	RecordExecution(ctx context.Context, params RecordExecutionParams) (Migration, error)
}

type PostgresRepository struct {
	pool *pgxpool.Pool
}

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

const selectMigrationColumns = `
	m.id,
	m.database_id,
	m.version,
	m.title,
	m.sql,
	m.status,
	m.created_by,
	COALESCE(creator.username, ''),
	m.executed_by,
	executor.username,
	m.created_at,
	m.executed_at,
	m.error_message
`

const fromMigrationsJoined = `
	FROM migrations m
	LEFT JOIN users creator ON creator.id = m.created_by
	LEFT JOIN users executor ON executor.id = m.executed_by
`

func scanMigration(row pgx.Row) (Migration, error) {
	var m Migration
	var statusStr string
	err := row.Scan(
		&m.ID,
		&m.DatabaseID,
		&m.Version,
		&m.Title,
		&m.SQL,
		&statusStr,
		&m.CreatedBy,
		&m.CreatedByUsername,
		&m.ExecutedBy,
		&m.ExecutedByUsername,
		&m.CreatedAt,
		&m.ExecutedAt,
		&m.ErrorMessage,
	)
	if err != nil {
		return Migration{}, err
	}
	m.Status = Status(statusStr)
	return m, nil
}

func (r *PostgresRepository) CreateDraft(ctx context.Context, params CreateDraftParams) (Migration, error) {
	var id int64
	err := r.pool.QueryRow(ctx,
		`INSERT INTO migrations (database_id, title, sql, status, created_by)
		 VALUES ($1, $2, $3, 'draft', $4)
		 RETURNING id`,
		params.DatabaseID, params.Title, params.SQL, params.CreatedBy,
	).Scan(&id)
	if err != nil {
		return Migration{}, fmt.Errorf("migration: create draft: %w", err)
	}
	return r.GetByID(ctx, params.DatabaseID, id)
}

func (r *PostgresRepository) UpdateDraft(ctx context.Context, params UpdateDraftParams) (Migration, error) {
	var id int64
	err := r.pool.QueryRow(ctx,
		`UPDATE migrations
		 SET title = $1, sql = $2
		 WHERE id = $3 AND database_id = $4 AND status = 'draft'
		 RETURNING id`,
		params.Title, params.SQL, params.ID, params.DatabaseID,
	).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Migration{}, r.classifyNonDraftError(ctx, params.DatabaseID, params.ID)
	}
	if err != nil {
		return Migration{}, fmt.Errorf("migration: update draft: %w", err)
	}
	return r.GetByID(ctx, params.DatabaseID, id)
}

func (r *PostgresRepository) GetByID(ctx context.Context, databaseID, migrationID int64) (Migration, error) {
	query := "SELECT " + selectMigrationColumns + fromMigrationsJoined + " WHERE m.database_id = $1 AND m.id = $2"
	m, err := scanMigration(r.pool.QueryRow(ctx, query, databaseID, migrationID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Migration{}, ErrNotFound
	}
	if err != nil {
		return Migration{}, fmt.Errorf("migration: get by id: %w", err)
	}
	return m, nil
}

func (r *PostgresRepository) ListByDatabase(ctx context.Context, databaseID int64) ([]Migration, error) {
	query := "SELECT " + selectMigrationColumns + fromMigrationsJoined + `
		WHERE m.database_id = $1
		ORDER BY CASE WHEN m.version IS NULL THEN 1 ELSE 0 END ASC,
		         m.version ASC,
		         m.created_at ASC,
		         m.id ASC`

	rows, err := r.pool.Query(ctx, query, databaseID)
	if err != nil {
		return nil, fmt.Errorf("migration: list by database: %w", err)
	}
	defer rows.Close()

	var result []Migration
	for rows.Next() {
		m, err := scanMigration(rows)
		if err != nil {
			return nil, fmt.Errorf("migration: scan row: %w", err)
		}
		result = append(result, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("migration: rows err: %w", err)
	}
	return result, nil
}

func (r *PostgresRepository) NextVersion(ctx context.Context, databaseID int64) (int, error) {
	var next int
	err := r.pool.QueryRow(ctx,
		`SELECT COALESCE(MAX(version), 0) + 1 FROM migrations WHERE database_id = $1`,
		databaseID,
	).Scan(&next)
	if err != nil {
		return 0, fmt.Errorf("migration: next version: %w", err)
	}
	return next, nil
}

func (r *PostgresRepository) RecordExecution(ctx context.Context, params RecordExecutionParams) (Migration, error) {
	if !StatusDraft.CanTransitionTo(params.Status) {
		return Migration{}, ErrInvalidTransition
	}
	if params.Version <= 0 {
		return Migration{}, errors.New("migration: version must be positive")
	}

	var id int64
	err := r.pool.QueryRow(ctx,
		`UPDATE migrations
		 SET version = $1,
		     status = $2,
		     executed_by = $3,
		     executed_at = $4,
		     error_message = $5
		 WHERE id = $6 AND database_id = $7 AND status = 'draft'
		 RETURNING id`,
		params.Version,
		string(params.Status),
		params.ExecutedBy,
		params.ExecutedAt,
		params.ErrorMessage,
		params.ID,
		params.DatabaseID,
	).Scan(&id)

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return Migration{}, ErrVersionConflict
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return Migration{}, r.classifyNonDraftError(ctx, params.DatabaseID, params.ID)
	}
	if err != nil {
		return Migration{}, fmt.Errorf("migration: record execution: %w", err)
	}
	return r.GetByID(ctx, params.DatabaseID, id)
}

func (r *PostgresRepository) classifyNonDraftError(ctx context.Context, databaseID, migrationID int64) error {
	var statusStr string
	err := r.pool.QueryRow(ctx,
		`SELECT status FROM migrations WHERE database_id = $1 AND id = $2`,
		databaseID, migrationID,
	).Scan(&statusStr)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("migration: check status: %w", err)
	}
	if Status(statusStr) == StatusApplied {
		return ErrAlreadyApplied
	}
	return ErrNotDraft
}

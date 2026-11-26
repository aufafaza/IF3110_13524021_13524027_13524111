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
	DatabaseID         int64
	Title              string
	SQL                string
	DownSQL            *string
	IrreversibleReason *string
	CreatedBy          int64
}

type UpdateDraftParams struct {
	ID                 int64
	DatabaseID         int64
	Title              string
	SQL                string
	DownSQL            *string
	IrreversibleReason *string
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
	m.down_sql,
	m.irreversible_reason,
	m.revision,
	m.first_executed_at,
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
		&m.DownSQL,
		&m.IrreversibleReason,
		&m.Revision,
		&m.FirstExecutedAt,
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
	down, reason := params.DownSQL, params.IrreversibleReason
	if down == nil && reason == nil {
		noDown := "No down SQL provided"
		reason = &noDown
	}
	err := r.pool.QueryRow(ctx,
		`INSERT INTO migrations (database_id, title, sql, down_sql, irreversible_reason, status, created_by)
		 VALUES ($1, $2, $3, $4, $5, 'draft', $6)
		 RETURNING id`,
		params.DatabaseID, params.Title, params.SQL, down, reason, params.CreatedBy,
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
		 SET title = $1, sql = $2,
		     down_sql = CASE WHEN $3::text IS NULL AND $4::text IS NULL
		                     THEN down_sql ELSE $3::text END,
		     irreversible_reason = CASE WHEN $3::text IS NULL AND $4::text IS NULL
		                                THEN irreversible_reason ELSE $4::text END,
		     revision = revision + 1
		 WHERE id = $5 AND database_id = $6 AND status = 'draft'
		 RETURNING id`,
		params.Title, params.SQL, params.DownSQL, params.IrreversibleReason, params.ID, params.DatabaseID,
	).Scan(&id)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23000" {
		return Migration{}, ErrImmutable
	}
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

// AttemptResult: stub kontrak dengan PIC 1 (satu baris per attempt).
type AttemptResult struct {
	DatabaseID            int64
	MigrationID           int64
	Revision              int
	ExecutionID           string
	AttemptID             string
	Operation             Operation
	Status                AttemptStatus
	ActorID               int64
	ExecutedSQL           string
	ErrorMessage          *string
	RollbackOfExecutionID *string
	At                    time.Time
}

type HistoryRepository interface {
	// Idempoten: redelivery (execution_id, attempt_id sama) tidak menambah baris.
	AppendHistory(ctx context.Context, r AttemptResult) (entry HistoryEntry, created bool, err error)
	ListHistoryEntries(ctx context.Context, databaseID int64) ([]HistoryEntry, error)
}

const historyColumns = `id, database_id, migration_id, revision, execution_id, attempt_id,
	operation, status, actor_id, executed_sql, error_message, rollback_of_execution_id, created_at`

func scanHistory(row pgx.Row) (HistoryEntry, error) {
	var e HistoryEntry
	var op, st string
	err := row.Scan(&e.ID, &e.DatabaseID, &e.MigrationID, &e.Revision, &e.ExecutionID, &e.AttemptID,
		&op, &st, &e.ActorID, &e.ExecutedSQL, &e.ErrorMessage, &e.RollbackOfExecutionID, &e.CreatedAt)
	e.Operation, e.Status = Operation(op), AttemptStatus(st)
	return e, err
}

func (r *PostgresRepository) AppendHistory(ctx context.Context, a AttemptResult) (HistoryEntry, bool, error) {
	e, err := scanHistory(r.pool.QueryRow(ctx,
		`INSERT INTO migration_history
		   (database_id, migration_id, revision, execution_id, attempt_id, operation, status,
		    actor_id, executed_sql, error_message, rollback_of_execution_id, created_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		 ON CONFLICT (execution_id, attempt_id) DO NOTHING
		 RETURNING `+historyColumns,
		a.DatabaseID, a.MigrationID, a.Revision, a.ExecutionID, a.AttemptID,
		string(a.Operation), string(a.Status), a.ActorID, a.ExecutedSQL,
		a.ErrorMessage, a.RollbackOfExecutionID, a.At))
	if err == nil {
		return e, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return HistoryEntry{}, false, fmt.Errorf("migration: append history: %w", err)
	}
	e, err = scanHistory(r.pool.QueryRow(ctx,
		`SELECT `+historyColumns+` FROM migration_history WHERE execution_id = $1 AND attempt_id = $2`,
		a.ExecutionID, a.AttemptID))
	if err != nil {
		return HistoryEntry{}, false, fmt.Errorf("migration: load existing attempt: %w", err)
	}
	return e, false, nil
}

func (r *PostgresRepository) ListHistoryEntries(ctx context.Context, databaseID int64) ([]HistoryEntry, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+historyColumns+` FROM migration_history h WHERE database_id = $1
		 ORDER BY (execution_id = 'exec-' || migration_id AND attempt_id = 'a1') DESC,
		 CASE WHEN execution_id = 'exec-' || migration_id AND attempt_id = 'a1'
		      THEN (SELECT version FROM migrations WHERE id = h.migration_id) END NULLS LAST,
		 id`, databaseID)
	if err != nil {
		return nil, fmt.Errorf("migration: list history: %w", err)
	}
	defer rows.Close()
	out := []HistoryEntry{}
	for rows.Next() {
		e, err := scanHistory(rows)
		if err != nil {
			return nil, fmt.Errorf("migration: scan history: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

var _ HistoryRepository = (*PostgresRepository)(nil)

func (r *PostgresRepository) LockDefinition(ctx context.Context, databaseID, migrationID int64, revision int) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE migrations SET first_executed_at = now()
		 WHERE id = $1 AND database_id = $2 AND revision = $3 AND first_executed_at IS NULL`,
		migrationID, databaseID, revision)
	if err != nil {
		return fmt.Errorf("migration: lock definition: %w", err)
	}
	return nil
}

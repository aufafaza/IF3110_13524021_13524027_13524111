package admin

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/access"
)

const (
	pgUniqueViolation     = "23505"
	pgForeignKeyViolation = "23503"
)

type Store interface {
	ListUsers(ctx context.Context) ([]User, error)
	CreateUser(ctx context.Context, username, passwordHash string, isPlatformAdmin bool) (int64, error)
	SetUserActive(ctx context.Context, userID int64, active bool) error
	SetPasswordHash(ctx context.Context, userID int64, passwordHash string) error

	ListGroups(ctx context.Context) ([]Group, error)
	GetGroup(ctx context.Context, groupID int64) (Group, error)
	CreateGroup(ctx context.Context, name string) (int64, error)
	DeleteGroup(ctx context.Context, groupID int64) error

	ListMembers(ctx context.Context, groupID int64) ([]User, error)
	ListNonMembers(ctx context.Context, groupID int64) ([]User, error)
	MemberIDs(ctx context.Context, groupID int64) ([]int64, error)
	AddMember(ctx context.Context, groupID, userID int64) error
	RemoveMember(ctx context.Context, groupID, userID int64) error

	ListDatabases(ctx context.Context) ([]Database, error)
	ListGrants(ctx context.Context, groupID int64) ([]Grant, error)
	SetGrant(ctx context.Context, groupID, databaseID int64, level access.Level) error
	RemoveGrant(ctx context.Context, groupID, databaseID int64) error
}

type PostgresStore struct {
	pool *pgxpool.Pool
}

var _ Store = (*PostgresStore)(nil)

func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool}
}

func pgCode(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}

func (s *PostgresStore) scanUsers(ctx context.Context, op, sql string, args ...any) ([]User, error) {
	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("admin: %s: %w", op, err)
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Username, &u.IsPlatformAdmin, &u.IsActive, &u.CreatedAt); err != nil {
			return nil, fmt.Errorf("admin: %s: %w", op, err)
		}
		users = append(users, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("admin: %s: %w", op, err)
	}
	return users, nil
}

func (s *PostgresStore) ListUsers(ctx context.Context) ([]User, error) {
	return s.scanUsers(ctx, "list users",
		`SELECT id, username, is_platform_admin, is_active, created_at
		FROM users ORDER BY username`)
}

func (s *PostgresStore) CreateUser(ctx context.Context, username, passwordHash string, isPlatformAdmin bool) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx,
		`INSERT INTO users (username, password_hash, is_platform_admin)
		VALUES ($1, $2, $3) RETURNING id`,
		username, passwordHash, isPlatformAdmin,
	).Scan(&id)
	if pgCode(err) == pgUniqueViolation {
		return 0, ErrUsernameTaken
	}
	if err != nil {
		return 0, fmt.Errorf("admin: create user: %w", err)
	}
	return id, nil
}

func (s *PostgresStore) SetUserActive(ctx context.Context, userID int64, active bool) error {
	tag, err := s.pool.Exec(ctx, `UPDATE users SET is_active = $2 WHERE id = $1`, userID, active)
	if err != nil {
		return fmt.Errorf("admin: set user active: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PostgresStore) SetPasswordHash(ctx context.Context, userID int64, passwordHash string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE users SET password_hash = $2 WHERE id = $1`, userID, passwordHash)
	if err != nil {
		return fmt.Errorf("admin: set password: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PostgresStore) ListGroups(ctx context.Context) ([]Group, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT g.id, g.name, count(ug.user_id)
		FROM groups g
		LEFT JOIN user_groups ug ON ug.group_id = g.id
		GROUP BY g.id, g.name
		ORDER BY g.name`)
	if err != nil {
		return nil, fmt.Errorf("admin: list groups: %w", err)
	}
	defer rows.Close()

	var groups []Group
	for rows.Next() {
		var g Group
		if err := rows.Scan(&g.ID, &g.Name, &g.MemberCount); err != nil {
			return nil, fmt.Errorf("admin: list groups: %w", err)
		}
		groups = append(groups, g)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("admin: list groups: %w", err)
	}
	return groups, nil
}

func (s *PostgresStore) GetGroup(ctx context.Context, groupID int64) (Group, error) {
	var g Group
	err := s.pool.QueryRow(ctx,
		`SELECT g.id, g.name, count(ug.user_id)
		FROM groups g
		LEFT JOIN user_groups ug ON ug.group_id = g.id
		WHERE g.id = $1
		GROUP BY g.id, g.name`, groupID,
	).Scan(&g.ID, &g.Name, &g.MemberCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return Group{}, ErrNotFound
	}
	if err != nil {
		return Group{}, fmt.Errorf("admin: get group: %w", err)
	}
	return g, nil
}

func (s *PostgresStore) CreateGroup(ctx context.Context, name string) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `INSERT INTO groups (name) VALUES ($1) RETURNING id`, name).Scan(&id)
	if pgCode(err) == pgUniqueViolation {
		return 0, ErrGroupNameTaken
	}
	if err != nil {
		return 0, fmt.Errorf("admin: create group: %w", err)
	}
	return id, nil
}

func (s *PostgresStore) DeleteGroup(ctx context.Context, groupID int64) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM groups WHERE id = $1`, groupID)
	if err != nil {
		return fmt.Errorf("admin: delete group: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PostgresStore) ListMembers(ctx context.Context, groupID int64) ([]User, error) {
	return s.scanUsers(ctx, "list members",
		`SELECT u.id, u.username, u.is_platform_admin, u.is_active, u.created_at
		FROM users u
		JOIN user_groups ug ON ug.user_id = u.id
		WHERE ug.group_id = $1
		ORDER BY u.username`, groupID)
}

func (s *PostgresStore) ListNonMembers(ctx context.Context, groupID int64) ([]User, error) {
	return s.scanUsers(ctx, "list non-members",
		`SELECT u.id, u.username, u.is_platform_admin, u.is_active, u.created_at
		FROM users u
		WHERE u.is_active
		  AND NOT EXISTS (
			SELECT 1 FROM user_groups ug
			WHERE ug.user_id = u.id AND ug.group_id = $1)
		ORDER BY u.username`, groupID)
}

func (s *PostgresStore) MemberIDs(ctx context.Context, groupID int64) ([]int64, error) {
	rows, err := s.pool.Query(ctx, `SELECT user_id FROM user_groups WHERE group_id = $1`, groupID)
	if err != nil {
		return nil, fmt.Errorf("admin: member ids: %w", err)
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("admin: member ids: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("admin: member ids: %w", err)
	}
	return ids, nil
}

func (s *PostgresStore) AddMember(ctx context.Context, groupID, userID int64) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO user_groups (user_id, group_id) VALUES ($1, $2)
		ON CONFLICT DO NOTHING`, userID, groupID)
	if pgCode(err) == pgForeignKeyViolation {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("admin: add member: %w", err)
	}
	return nil
}

func (s *PostgresStore) RemoveMember(ctx context.Context, groupID, userID int64) error {
	_, err := s.pool.Exec(ctx,
		`DELETE FROM user_groups WHERE user_id = $1 AND group_id = $2`, userID, groupID)
	if err != nil {
		return fmt.Errorf("admin: remove member: %w", err)
	}
	return nil
}

func (s *PostgresStore) ListDatabases(ctx context.Context) ([]Database, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, name FROM managed_databases ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("admin: list databases: %w", err)
	}
	defer rows.Close()

	var dbs []Database
	for rows.Next() {
		var d Database
		if err := rows.Scan(&d.ID, &d.Name); err != nil {
			return nil, fmt.Errorf("admin: list databases: %w", err)
		}
		dbs = append(dbs, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("admin: list databases: %w", err)
	}
	return dbs, nil
}

func (s *PostgresStore) ListGrants(ctx context.Context, groupID int64) ([]Grant, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT d.id, d.name, gda.level
		FROM group_db_access gda
		JOIN managed_databases d ON d.id = gda.database_id
		WHERE gda.group_id = $1
		ORDER BY d.name`, groupID)
	if err != nil {
		return nil, fmt.Errorf("admin: list grants: %w", err)
	}
	defer rows.Close()

	var grants []Grant
	for rows.Next() {
		var g Grant
		var level string
		if err := rows.Scan(&g.DatabaseID, &g.DatabaseName, &level); err != nil {
			return nil, fmt.Errorf("admin: list grants: %w", err)
		}
		if g.Level, err = access.ParseLevel(level); err != nil {
			return nil, fmt.Errorf("admin: list grants: %w", err)
		}
		grants = append(grants, g)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("admin: list grants: %w", err)
	}
	return grants, nil
}

func (s *PostgresStore) SetGrant(ctx context.Context, groupID, databaseID int64, level access.Level) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO group_db_access (group_id, database_id, level) VALUES ($1, $2, $3)
		ON CONFLICT (group_id, database_id) DO UPDATE SET level = EXCLUDED.level`,
		groupID, databaseID, level.String())
	if pgCode(err) == pgForeignKeyViolation {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("admin: set grant: %w", err)
	}
	return nil
}

func (s *PostgresStore) RemoveGrant(ctx context.Context, groupID, databaseID int64) error {
	_, err := s.pool.Exec(ctx,
		`DELETE FROM group_db_access WHERE group_id = $1 AND database_id = $2`, groupID, databaseID)
	if err != nil {
		return fmt.Errorf("admin: remove grant: %w", err)
	}
	return nil
}

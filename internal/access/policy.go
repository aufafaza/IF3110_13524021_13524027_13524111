package access

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func (l Level) String() string {
	switch l {
	case LevelView:
		return "view"
	case LevelEdit:
		return "edit"
	}
	return "none"
}

func ParseLevel(s string) (Level, error) {
	switch s {
	case "view":
		return LevelView, nil
	case "edit":
		return LevelEdit, nil
	}
	return LevelNone, fmt.Errorf("access: unknown level %q", s)
}

func Highest(levels ...Level) Level {
	highest := LevelNone
	for _, l := range levels {
		if l > highest {
			highest = l
		}
	}
	return highest
}

type PostgresChecker struct {
	pool *pgxpool.Pool
}

var _ Checker = (*PostgresChecker)(nil)

func NewPostgresChecker(pool *pgxpool.Pool) *PostgresChecker {
	return &PostgresChecker{pool: pool}
}

func (c *PostgresChecker) IsPlatformAdmin(ctx context.Context, userID int64) (bool, error) {
	var isAdmin bool
	err := c.pool.QueryRow(ctx,
		`SELECT is_platform_admin FROM users WHERE id = $1 AND is_active`,
		userID,
	).Scan(&isAdmin)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("access: is platform admin: %w", err)
	}
	return isAdmin, nil
}

func (c *PostgresChecker) EffectiveLevel(ctx context.Context, userID, databaseID int64) (Level, error) {
	rows, err := c.pool.Query(ctx,
		`SELECT gda.level
		FROM group_db_access gda
		JOIN user_groups ug ON ug.group_id = gda.group_id
		JOIN users u ON u.id = ug.user_id
		WHERE ug.user_id = $1 AND gda.database_id = $2 AND u.is_active`,
		userID, databaseID,
	)
	if err != nil {
		return LevelNone, fmt.Errorf("access: effective level: %w", err)
	}
	defer rows.Close()

	var levels []Level
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return LevelNone, fmt.Errorf("access: effective level: %w", err)
		}
		l, err := ParseLevel(raw)
		if err != nil {
			return LevelNone, err
		}
		levels = append(levels, l)
	}
	if err := rows.Err(); err != nil {
		return LevelNone, fmt.Errorf("access: effective level: %w", err)
	}
	return Highest(levels...), nil
}

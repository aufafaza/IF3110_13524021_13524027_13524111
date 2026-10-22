package migration

import (
	"context"
	"fmt"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5/pgxpool"
)

type AccessLevel int

const (
	LevelNone AccessLevel = iota
	LevelView
	LevelEdit
)

func (l AccessLevel) String() string {
	switch l {
	case LevelView:
		return "view"
	case LevelEdit:
		return "edit"
	default:
		return "none"
	}
}

type AccessChecker interface {
	EffectiveLevel(ctx context.Context, userID, databaseID int64) (AccessLevel, error)
}

type genericLevelChecker[L ~int] interface {
	EffectiveLevel(ctx context.Context, userID, databaseID int64) (L, error)
}

type checkerAdapter[L ~int] struct {
	inner genericLevelChecker[L]
}

func (a checkerAdapter[L]) EffectiveLevel(ctx context.Context, userID, databaseID int64) (AccessLevel, error) {
	lvl, err := a.inner.EffectiveLevel(ctx, userID, databaseID)
	if err != nil {
		return LevelNone, err
	}
	return AccessLevel(lvl), nil
}

func AdaptChecker[L ~int](c genericLevelChecker[L]) AccessChecker {
	return checkerAdapter[L]{inner: c}
}

type PostgresAccessChecker struct {
	pool *pgxpool.Pool
}

func NewPostgresAccessChecker(pool *pgxpool.Pool) *PostgresAccessChecker {
	return &PostgresAccessChecker{pool: pool}
}

func (c *PostgresAccessChecker) EffectiveLevel(ctx context.Context, userID, databaseID int64) (AccessLevel, error) {
	rows, err := c.pool.Query(ctx,
		`SELECT gda.level
		 FROM group_db_access gda
		 INNER JOIN user_groups ug ON ug.group_id = gda.group_id
		 INNER JOIN users u ON u.id = ug.user_id
		 WHERE ug.user_id = $1
		   AND gda.database_id = $2
		   AND u.is_active = TRUE`,
		userID, databaseID,
	)
	if err != nil {
		return LevelNone, fmt.Errorf("migration: query effective level: %w", err)
	}
	defer rows.Close()

	effective := LevelNone
	for rows.Next() {
		var lvlStr string
		if err := rows.Scan(&lvlStr); err != nil {
			return LevelNone, fmt.Errorf("migration: scan effective level: %w", err)
		}
		switch lvlStr {
		case "edit":
			if effective < LevelEdit {
				effective = LevelEdit
			}
		case "view":
			if effective < LevelView {
				effective = LevelView
			}
		}
	}
	if err := rows.Err(); err != nil {
		return LevelNone, fmt.Errorf("migration: effective level rows: %w", err)
	}
	return effective, nil
}

type User struct {
	ID              int64
	Username        string
	IsPlatformAdmin bool
}

type userContextKey struct{}

func WithUser(ctx context.Context, u User) context.Context {
	return context.WithValue(ctx, userContextKey{}, u)
}

func UserFromContext(ctx context.Context) (User, bool) {
	u, ok := ctx.Value(userContextKey{}).(User)
	return u, ok
}

type UserExtractor func(ctx context.Context) (User, bool)

func RequireDBAccess(checker AccessChecker, min AccessLevel, extractUser UserExtractor) func(http.Handler) http.Handler {
	if extractUser == nil {
		extractUser = UserFromContext
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, ok := extractUser(r.Context())
			if !ok || user.ID == 0 {
				http.Error(w, "unauthenticated", http.StatusUnauthorized)
				return
			}

			dbIDStr := r.PathValue("dbID")
			dbID, err := strconv.ParseInt(dbIDStr, 10, 64)
			if err != nil || dbID <= 0 {
				http.Error(w, "invalid database id", http.StatusBadRequest)
				return
			}

			level, err := checker.EffectiveLevel(r.Context(), user.ID, dbID)
			if err != nil {
				http.Error(w, "failed to verify database access", http.StatusInternalServerError)
				return
			}
			if level < min {
				http.Error(w, "forbidden: insufficient database permissions", http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

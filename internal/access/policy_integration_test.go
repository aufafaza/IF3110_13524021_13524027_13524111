//go:build integration

package access_test

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/access"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/platformdb"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/migrations"
)

type fixture struct {
	pool    *pgxpool.Pool
	checker *access.PostgresChecker
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()

	pool, err := platformdb.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	if _, err := pool.Exec(ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
		t.Fatal(err)
	}
	if err := platformdb.Migrate(ctx, pool, migrations.Platform()); err != nil {
		t.Fatal(err)
	}
	return fixture{pool: pool, checker: access.NewPostgresChecker(pool)}
}

func (f fixture) insertID(t *testing.T, sql string, args ...any) int64 {
	t.Helper()
	var id int64
	if err := f.pool.QueryRow(context.Background(), sql, args...).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func (f fixture) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := f.pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatal(err)
	}
}

func (f fixture) user(t *testing.T, name string, admin, active bool) int64 {
	return f.insertID(t,
		`INSERT INTO users (username, password_hash, is_platform_admin, is_active)
		VALUES ($1, 'h', $2, $3) RETURNING id`, name, admin, active)
}

func (f fixture) group(t *testing.T, name string) int64 {
	return f.insertID(t, `INSERT INTO groups (name) VALUES ($1) RETURNING id`, name)
}

func (f fixture) database(t *testing.T, name string) int64 {
	return f.insertID(t,
		`INSERT INTO managed_databases (name, host, db_name, db_user, password_enc)
		VALUES ($1, 'localhost', 'x', 'x', '\x00') RETURNING id`, name)
}

func (f fixture) join(t *testing.T, userID, groupID int64) {
	f.exec(t, `INSERT INTO user_groups (user_id, group_id) VALUES ($1, $2)`, userID, groupID)
}

func (f fixture) grant(t *testing.T, groupID, dbID int64, level string) {
	f.exec(t, `INSERT INTO group_db_access (group_id, database_id, level) VALUES ($1, $2, $3)`,
		groupID, dbID, level)
}

func (f fixture) level(t *testing.T, userID, dbID int64) access.Level {
	t.Helper()
	l, err := f.checker.EffectiveLevel(context.Background(), userID, dbID)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestEffectiveLevelNoGroups(t *testing.T) {
	f := newFixture(t)
	u := f.user(t, "alice", false, true)
	db := f.database(t, "db1")

	if got := f.level(t, u, db); got != access.LevelNone {
		t.Errorf("level = %v, want none", got)
	}
}

func TestEffectiveLevelSingleGroup(t *testing.T) {
	for _, tc := range []struct {
		grant string
		want  access.Level
	}{{"view", access.LevelView}, {"edit", access.LevelEdit}} {
		t.Run(tc.grant, func(t *testing.T) {
			f := newFixture(t)
			u := f.user(t, "alice", false, true)
			g := f.group(t, "g")
			db := f.database(t, "db1")
			f.join(t, u, g)
			f.grant(t, g, db, tc.grant)

			if got := f.level(t, u, db); got != tc.want {
				t.Errorf("level = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestEffectiveLevelHighestWinsAcrossGroups(t *testing.T) {
	f := newFixture(t)
	u := f.user(t, "alice", false, true)
	viewers, editors := f.group(t, "viewers"), f.group(t, "editors")
	db := f.database(t, "db1")
	f.join(t, u, viewers)
	f.join(t, u, editors)
	f.grant(t, viewers, db, "view")
	f.grant(t, editors, db, "edit")

	if got := f.level(t, u, db); got != access.LevelEdit {
		t.Errorf("level = %v, want edit", got)
	}
}

func TestEffectiveLevelSameGroupDifferentLevelPerDatabase(t *testing.T) {
	f := newFixture(t)
	u := f.user(t, "alice", false, true)
	g := f.group(t, "g")
	db1, db2, db3 := f.database(t, "db1"), f.database(t, "db2"), f.database(t, "db3")
	f.join(t, u, g)
	f.grant(t, g, db1, "view")
	f.grant(t, g, db2, "edit")

	if got := f.level(t, u, db1); got != access.LevelView {
		t.Errorf("db1 = %v, want view", got)
	}
	if got := f.level(t, u, db2); got != access.LevelEdit {
		t.Errorf("db2 = %v, want edit", got)
	}
	if got := f.level(t, u, db3); got != access.LevelNone {
		t.Errorf("db3 = %v, want none", got)
	}
}

func TestEffectiveLevelIsolatedBetweenUsersAndGroups(t *testing.T) {
	f := newFixture(t)
	alice := f.user(t, "alice", false, true)
	bob := f.user(t, "bob", false, true)
	member, other := f.group(t, "member"), f.group(t, "other")
	db := f.database(t, "db1")
	f.join(t, alice, member)
	f.join(t, bob, other)
	f.grant(t, member, db, "edit")

	if got := f.level(t, bob, db); got != access.LevelNone {
		t.Errorf("bob level = %v, want none", got)
	}
}

func TestEffectiveLevelGrantWithoutMembership(t *testing.T) {
	f := newFixture(t)
	u := f.user(t, "alice", false, true)
	g := f.group(t, "g")
	db := f.database(t, "db1")
	f.grant(t, g, db, "edit")

	if got := f.level(t, u, db); got != access.LevelNone {
		t.Errorf("level = %v, want none (not a member)", got)
	}
}

func TestEffectiveLevelInactiveUser(t *testing.T) {
	f := newFixture(t)
	u := f.user(t, "alice", false, false)
	g := f.group(t, "g")
	db := f.database(t, "db1")
	f.join(t, u, g)
	f.grant(t, g, db, "edit")

	if got := f.level(t, u, db); got != access.LevelNone {
		t.Errorf("level = %v, want none for an inactive user", got)
	}
}

func TestEffectiveLevelPlatformAdminGetsNothingByDefault(t *testing.T) {
	f := newFixture(t)
	admin := f.user(t, "admin", true, true)
	db := f.database(t, "db1")

	if got := f.level(t, admin, db); got != access.LevelNone {
		t.Errorf("level = %v, want none: admin is not a database access level", got)
	}
}

func TestIsPlatformAdmin(t *testing.T) {
	f := newFixture(t)
	admin := f.user(t, "admin", true, true)
	member := f.user(t, "member", false, true)
	disabledAdmin := f.user(t, "disabled", true, false)

	tests := []struct {
		name string
		id   int64
		want bool
	}{
		{"admin", admin, true},
		{"regular user", member, false},
		{"inactive admin", disabledAdmin, false},
		{"unknown user", 9999, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := f.checker.IsPlatformAdmin(context.Background(), tc.id)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("IsPlatformAdmin = %v, want %v", got, tc.want)
			}
		})
	}
}

//go:build integration

package admin_test

import (
	"context"
	"errors"
	"os"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/access"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/admin"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/platformdb"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/migrations"
)

type fixture struct {
	pool  *pgxpool.Pool
	store *admin.PostgresStore
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
	return fixture{pool: pool, store: admin.NewPostgresStore(pool)}
}

func (f fixture) database(t *testing.T, name string) int64 {
	t.Helper()
	var id int64
	err := f.pool.QueryRow(context.Background(),
		`INSERT INTO managed_databases (name, host, db_name, db_user, password_enc)
		VALUES ($1, 'localhost', 'x', 'x', '\x00') RETURNING id`, name).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func (f fixture) user(t *testing.T, name string) int64 {
	t.Helper()
	id, err := f.store.CreateUser(context.Background(), name, "hash", false)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func (f fixture) group(t *testing.T, name string) int64 {
	t.Helper()
	id, err := f.store.CreateGroup(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func usernames(users []admin.User) []string {
	var names []string
	for _, u := range users {
		names = append(names, u.Username)
	}
	return names
}

func TestStore_CreateAndListUsers(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.user(t, "bob")
	if _, err := f.store.CreateUser(ctx, "alice", "hash", true); err != nil {
		t.Fatal(err)
	}

	users, err := f.store.ListUsers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := usernames(users); !slices.Equal(got, []string{"alice", "bob"}) {
		t.Fatalf("users = %v, want sorted [alice bob]", got)
	}
	if !users[0].IsPlatformAdmin || !users[0].IsActive {
		t.Errorf("alice = %+v, want admin and active", users[0])
	}
	if users[1].IsPlatformAdmin {
		t.Error("bob must not be admin")
	}
}

func TestStore_CreateUserDuplicate(t *testing.T) {
	f := newFixture(t)
	f.user(t, "alice")

	_, err := f.store.CreateUser(context.Background(), "alice", "hash", false)
	if !errors.Is(err, admin.ErrUsernameTaken) {
		t.Fatalf("got %v, want ErrUsernameTaken", err)
	}
}

func TestStore_SetUserActiveAndPassword(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	id := f.user(t, "alice")

	if err := f.store.SetUserActive(ctx, id, false); err != nil {
		t.Fatal(err)
	}
	if err := f.store.SetPasswordHash(ctx, id, "new-hash"); err != nil {
		t.Fatal(err)
	}

	var active bool
	var hash string
	err := f.pool.QueryRow(ctx, `SELECT is_active, password_hash FROM users WHERE id = $1`, id).Scan(&active, &hash)
	if err != nil {
		t.Fatal(err)
	}
	if active || hash != "new-hash" {
		t.Errorf("is_active=%v hash=%q", active, hash)
	}

	if err := f.store.SetUserActive(ctx, 9999, true); !errors.Is(err, admin.ErrNotFound) {
		t.Errorf("SetUserActive unknown = %v, want ErrNotFound", err)
	}
	if err := f.store.SetPasswordHash(ctx, 9999, "h"); !errors.Is(err, admin.ErrNotFound) {
		t.Errorf("SetPasswordHash unknown = %v, want ErrNotFound", err)
	}
}

func TestStore_GroupsLifecycle(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	analysts := f.group(t, "analysts")
	f.group(t, "devs")
	if _, err := f.store.CreateGroup(ctx, "analysts"); !errors.Is(err, admin.ErrGroupNameTaken) {
		t.Fatalf("duplicate group = %v, want ErrGroupNameTaken", err)
	}

	alice := f.user(t, "alice")
	if err := f.store.AddMember(ctx, analysts, alice); err != nil {
		t.Fatal(err)
	}

	groups, err := f.store.ListGroups(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 2 || groups[0].Name != "analysts" || groups[0].MemberCount != 1 || groups[1].MemberCount != 0 {
		t.Fatalf("groups = %+v", groups)
	}

	g, err := f.store.GetGroup(ctx, analysts)
	if err != nil || g.MemberCount != 1 {
		t.Fatalf("GetGroup = %+v, %v", g, err)
	}
	if _, err := f.store.GetGroup(ctx, 9999); !errors.Is(err, admin.ErrNotFound) {
		t.Errorf("GetGroup unknown = %v, want ErrNotFound", err)
	}

	if err := f.store.DeleteGroup(ctx, analysts); err != nil {
		t.Fatal(err)
	}
	if err := f.store.DeleteGroup(ctx, analysts); !errors.Is(err, admin.ErrNotFound) {
		t.Errorf("second delete = %v, want ErrNotFound", err)
	}
	var memberships int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM user_groups`).Scan(&memberships); err != nil {
		t.Fatal(err)
	}
	if memberships != 0 {
		t.Errorf("%d memberships left after the group was deleted", memberships)
	}
}

func TestStore_Membership(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	g := f.group(t, "analysts")
	alice := f.user(t, "alice")
	bob := f.user(t, "bob")
	carol := f.user(t, "carol")
	if err := f.store.SetUserActive(ctx, carol, false); err != nil {
		t.Fatal(err)
	}

	if err := f.store.AddMember(ctx, g, alice); err != nil {
		t.Fatal(err)
	}
	if err := f.store.AddMember(ctx, g, alice); err != nil {
		t.Fatalf("adding an existing member must be idempotent: %v", err)
	}

	members, err := f.store.ListMembers(ctx, g)
	if err != nil {
		t.Fatal(err)
	}
	if got := usernames(members); !slices.Equal(got, []string{"alice"}) {
		t.Fatalf("members = %v", got)
	}

	candidates, err := f.store.ListNonMembers(ctx, g)
	if err != nil {
		t.Fatal(err)
	}
	if got := usernames(candidates); !slices.Equal(got, []string{"bob"}) {
		t.Errorf("non-members = %v, want only active non-members [bob]", got)
	}

	ids, err := f.store.MemberIDs(ctx, g)
	if err != nil || !slices.Equal(ids, []int64{alice}) {
		t.Errorf("MemberIDs = %v, %v", ids, err)
	}

	if err := f.store.RemoveMember(ctx, g, alice); err != nil {
		t.Fatal(err)
	}
	if err := f.store.RemoveMember(ctx, g, bob); err != nil {
		t.Fatalf("removing a non-member must not fail: %v", err)
	}
	if ids, _ := f.store.MemberIDs(ctx, g); len(ids) != 0 {
		t.Errorf("members after removal = %v", ids)
	}

	if err := f.store.AddMember(ctx, g, 9999); !errors.Is(err, admin.ErrNotFound) {
		t.Errorf("unknown user = %v, want ErrNotFound", err)
	}
	if err := f.store.AddMember(ctx, 9999, alice); !errors.Is(err, admin.ErrNotFound) {
		t.Errorf("unknown group = %v, want ErrNotFound", err)
	}
}

func TestStore_Grants(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	g := f.group(t, "analysts")
	sales := f.database(t, "sales")
	hr := f.database(t, "hr")

	if err := f.store.SetGrant(ctx, g, sales, access.LevelView); err != nil {
		t.Fatal(err)
	}
	if err := f.store.SetGrant(ctx, g, hr, access.LevelEdit); err != nil {
		t.Fatal(err)
	}
	if err := f.store.SetGrant(ctx, g, sales, access.LevelEdit); err != nil {
		t.Fatalf("changing a level must upsert: %v", err)
	}

	grants, err := f.store.ListGrants(ctx, g)
	if err != nil {
		t.Fatal(err)
	}
	want := []admin.Grant{
		{DatabaseID: hr, DatabaseName: "hr", Level: access.LevelEdit},
		{DatabaseID: sales, DatabaseName: "sales", Level: access.LevelEdit},
	}
	if !slices.Equal(grants, want) {
		t.Fatalf("grants = %+v, want %+v", grants, want)
	}

	if err := f.store.RemoveGrant(ctx, g, hr); err != nil {
		t.Fatal(err)
	}
	if grants, _ := f.store.ListGrants(ctx, g); len(grants) != 1 || grants[0].DatabaseID != sales {
		t.Errorf("grants after removal = %+v", grants)
	}

	if err := f.store.SetGrant(ctx, g, 9999, access.LevelView); !errors.Is(err, admin.ErrNotFound) {
		t.Errorf("unknown database = %v, want ErrNotFound", err)
	}
	if err := f.store.SetGrant(ctx, 9999, sales, access.LevelView); !errors.Is(err, admin.ErrNotFound) {
		t.Errorf("unknown group = %v, want ErrNotFound", err)
	}

	dbs, err := f.store.ListDatabases(ctx)
	if err != nil || len(dbs) != 2 || dbs[0].Name != "hr" {
		t.Errorf("ListDatabases = %+v, %v", dbs, err)
	}
}

func TestStore_GrantsFeedTheAccessChecker(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	checker := access.NewPostgresChecker(f.pool)

	viewers := f.group(t, "viewers")
	editors := f.group(t, "editors")
	db := f.database(t, "sales")
	alice := f.user(t, "alice")

	for _, step := range []struct {
		name string
		do   func() error
		want access.Level
	}{
		{"no membership", func() error { return nil }, access.LevelNone},
		{"member, no grant", func() error { return f.store.AddMember(ctx, viewers, alice) }, access.LevelNone},
		{"view grant", func() error { return f.store.SetGrant(ctx, viewers, db, access.LevelView) }, access.LevelView},
		{"second group with edit wins", func() error {
			if err := f.store.AddMember(ctx, editors, alice); err != nil {
				return err
			}
			return f.store.SetGrant(ctx, editors, db, access.LevelEdit)
		}, access.LevelEdit},
		{"edit grant removed", func() error { return f.store.RemoveGrant(ctx, editors, db) }, access.LevelView},
		{"left the viewers group", func() error { return f.store.RemoveMember(ctx, viewers, alice) }, access.LevelNone},
	} {
		if err := step.do(); err != nil {
			t.Fatalf("%s: %v", step.name, err)
		}
		got, err := checker.EffectiveLevel(ctx, alice, db)
		if err != nil {
			t.Fatalf("%s: %v", step.name, err)
		}
		if got != step.want {
			t.Errorf("%s: level = %v, want %v", step.name, got, step.want)
		}
	}
}

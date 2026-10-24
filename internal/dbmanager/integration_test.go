package dbmanager_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/dbmanager"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/platformdb"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/migrations"
)

const fixtureSQL = `
CREATE SCHEMA viewer_fixture;
CREATE TABLE viewer_fixture.authors (
	id    serial PRIMARY KEY,
	name  text NOT NULL,
	email text UNIQUE,
	bio   text
);
CREATE TABLE viewer_fixture.books (
	id        serial PRIMARY KEY,
	author_id int NOT NULL REFERENCES viewer_fixture.authors(id),
	title     text NOT NULL,
	pages     int CHECK (pages > 0),
	price     numeric(8,2) DEFAULT 9.99
);
CREATE INDEX books_title_idx ON viewer_fixture.books USING btree (title);
INSERT INTO viewer_fixture.authors (name, email, bio) VALUES
	('Ann', 'ann@example.com', NULL),
	('Bob', 'bob@example.com', repeat('x', 500));
INSERT INTO viewer_fixture.books (author_id, title, pages)
	SELECT 1, 'Book ' || g, 100 + g FROM generate_series(1, 120) g;
`

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func setup(t *testing.T) (context.Context, *pgxpool.Pool, *dbmanager.Registry, dbmanager.DatabaseInput) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := platformdb.Open(ctx, dsn)
	must(t, err)
	t.Cleanup(pool.Close)
	_, err = pool.Exec(ctx, "DROP SCHEMA public CASCADE; DROP SCHEMA IF EXISTS viewer_fixture CASCADE; CREATE SCHEMA public")
	must(t, err)
	must(t, platformdb.Migrate(ctx, pool, migrations.Platform()))
	_, err = pool.Exec(ctx, fixtureSQL)
	must(t, err)

	c, err := dbmanager.NewCipher(bytes.Repeat([]byte{7}, 32))
	must(t, err)
	reg := dbmanager.NewRegistry(pool, c)
	t.Cleanup(reg.Close)

	cfg, err := pgxpool.ParseConfig(dsn)
	must(t, err)
	cc := cfg.ConnConfig
	return ctx, pool, reg, dbmanager.DatabaseInput{
		Name: "fixture", Host: cc.Host, Port: int(cc.Port), DBName: cc.Database,
		DBUser: cc.User, Password: cc.Password, SSLMode: "disable",
	}
}

func newViewer(t *testing.T) (context.Context, *dbmanager.Viewer, int64) {
	t.Helper()
	ctx, _, reg, in := setup(t)
	d, err := reg.Create(ctx, in)
	must(t, err)
	return ctx, dbmanager.NewViewer(reg), d.ID
}

func TestCreateEncryptsPassword(t *testing.T) {
	ctx, pool, reg, in := setup(t)
	d, err := reg.Create(ctx, in)
	must(t, err)

	var enc []byte
	must(t, pool.QueryRow(ctx, "SELECT password_enc FROM managed_databases WHERE id=$1", d.ID).Scan(&enc))
	if len(enc) == 0 || bytes.Contains(enc, []byte(in.Password)) {
		t.Fatal("password tersimpan sebagai plaintext atau kosong")
	}
}

func TestCreateDuplicateName(t *testing.T) {
	ctx, _, reg, in := setup(t)
	_, err := reg.Create(ctx, in)
	must(t, err)
	if _, err := reg.Create(ctx, in); !errors.Is(err, dbmanager.ErrNameTaken) {
		t.Fatalf("err = %v, want ErrNameTaken", err)
	}
}

func TestUpdateKeepsPasswordWhenBlank(t *testing.T) {
	ctx, _, reg, in := setup(t)
	d, err := reg.Create(ctx, in)
	must(t, err)

	upd := in
	upd.Name, upd.Password = "renamed", ""
	must(t, reg.Update(ctx, d.ID, upd))

	got, err := reg.Get(ctx, d.ID)
	must(t, err)
	if got.Name != "renamed" {
		t.Errorf("name = %q", got.Name)
	}
	p, err := reg.Pool(ctx, d.ID)
	must(t, err)
	must(t, p.Ping(ctx))

	if err := reg.Update(ctx, 9999, upd); !errors.Is(err, dbmanager.ErrDatabaseNotFound) {
		t.Errorf("update id tak dikenal: err = %v", err)
	}
}

func TestDeleteRemovesDatabase(t *testing.T) {
	ctx, _, reg, in := setup(t)
	d, err := reg.Create(ctx, in)
	must(t, err)
	must(t, reg.Delete(ctx, d.ID))

	if _, err := reg.Get(ctx, d.ID); !errors.Is(err, dbmanager.ErrDatabaseNotFound) {
		t.Errorf("Get: err = %v", err)
	}
	if _, err := reg.Pool(ctx, d.ID); !errors.Is(err, dbmanager.ErrDatabaseNotFound) {
		t.Errorf("Pool: err = %v", err)
	}
	if err := reg.Delete(ctx, d.ID); !errors.Is(err, dbmanager.ErrDatabaseNotFound) {
		t.Errorf("Delete ulang: err = %v", err)
	}
}

func TestConnectionFailures(t *testing.T) {
	ctx, _, _, base := setup(t)
	tests := []struct {
		name   string
		mutate func(*dbmanager.DatabaseInput)
		reason string
	}{
		{"user tidak ada", func(i *dbmanager.DatabaseInput) { i.DBUser = "tidak_ada" }, "username atau password database salah"},
		{"database tidak ada", func(i *dbmanager.DatabaseInput) { i.DBName = "tidak_ada" }, "database tidak ditemukan di server"},
		{"server tidak aktif", func(i *dbmanager.DatabaseInput) { i.Port = 1 }, "server tidak dapat dihubungi"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := base
			tt.mutate(&in)
			err := dbmanager.TestConnection(ctx, in)
			var ce *dbmanager.ConnError
			if !errors.Is(err, dbmanager.ErrConnectionFailed) || !errors.As(err, &ce) || ce.Reason != tt.reason {
				t.Fatalf("err = %v, want reason %q", err, tt.reason)
			}
		})
	}
	if err := dbmanager.TestConnection(ctx, base); err != nil {
		t.Fatalf("koneksi valid ditolak: %v", err)
	}
}

func TestFailedPoolIsNotCached(t *testing.T) {
	ctx, _, reg, in := setup(t)
	bad := in
	bad.Port = 1
	d, err := reg.Create(ctx, bad)
	must(t, err)

	if _, err := reg.Pool(ctx, d.ID); !errors.Is(err, dbmanager.ErrConnectionFailed) {
		t.Fatalf("err = %v, want ErrConnectionFailed", err)
	}
	must(t, reg.Update(ctx, d.ID, in))
	p, err := reg.Pool(ctx, d.ID)
	must(t, err)
	must(t, p.Ping(ctx))
}

func TestViewerUnknownDatabase(t *testing.T) {
	ctx, v, _ := newViewer(t)
	if _, err := v.Tables(ctx, 9999); !errors.Is(err, dbmanager.ErrDatabaseNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestViewerTables(t *testing.T) {
	ctx, v, id := newViewer(t)
	tables, err := v.Tables(ctx, id)
	must(t, err)

	found := map[string]bool{}
	for _, tb := range tables {
		if tb.Schema == "pg_catalog" || tb.Schema == "information_schema" {
			t.Errorf("schema sistem ikut tampil: %s.%s", tb.Schema, tb.Name)
		}
		found[tb.Schema+"."+tb.Name] = true
	}
	for _, want := range []string{"viewer_fixture.authors", "viewer_fixture.books"} {
		if !found[want] {
			t.Errorf("%s tidak ditemukan", want)
		}
	}
}

func TestViewerTableInfo(t *testing.T) {
	ctx, v, id := newViewer(t)
	info, err := v.TableInfo(ctx, id, "viewer_fixture", "books")
	must(t, err)

	cols := map[string]dbmanager.Column{}
	for _, c := range info.Columns {
		cols[c.Name] = c
	}
	if len(info.Columns) != 5 {
		t.Fatalf("jumlah kolom = %d, want 5", len(info.Columns))
	}
	if a := cols["author_id"]; a.Nullable || a.Type != "integer" {
		t.Errorf("author_id: %+v", a)
	}
	if !cols["pages"].Nullable {
		t.Error("pages seharusnya nullable")
	}
	if p := cols["price"]; p.Type != "numeric(8,2)" || p.Default == nil || *p.Default != "9.99" {
		t.Errorf("price: %+v", p)
	}

	types := map[string]bool{}
	for _, c := range info.Constraints {
		types[c.Type] = true
	}
	for _, want := range []string{"PRIMARY KEY", "FOREIGN KEY", "CHECK"} {
		if !types[want] {
			t.Errorf("constraint %s tidak ditemukan: %+v", want, info.Constraints)
		}
	}

	idx := map[string]dbmanager.Index{}
	for _, i := range info.Indexes {
		idx[i.Name] = i
	}
	if i := idx["books_pkey"]; !i.Unique || i.Method != "btree" {
		t.Errorf("books_pkey: %+v", i)
	}
	if i := idx["books_title_idx"]; i.Unique || i.Method != "btree" {
		t.Errorf("books_title_idx: %+v", i)
	}

	authors, err := v.TableInfo(ctx, id, "viewer_fixture", "authors")
	must(t, err)
	at := map[string]bool{}
	for _, c := range authors.Constraints {
		at[c.Type] = true
	}
	if !at["PRIMARY KEY"] || !at["UNIQUE"] {
		t.Errorf("constraint authors: %+v", authors.Constraints)
	}
}

func TestViewerRejectsUnknownTable(t *testing.T) {
	ctx, v, id := newViewer(t)
	attempts := []struct{ schema, table string }{
		{"viewer_fixture", "tidak_ada"},
		{"viewer_fixture", `books"; DROP TABLE viewer_fixture.authors; --`},
		{"pg_catalog", "pg_class"},
	}
	for _, a := range attempts {
		if _, err := v.TableInfo(ctx, id, a.schema, a.table); !errors.Is(err, dbmanager.ErrTableNotFound) {
			t.Errorf("TableInfo(%q.%q): err = %v", a.schema, a.table, err)
		}
		if _, err := v.Rows(ctx, id, a.schema, a.table, 1); !errors.Is(err, dbmanager.ErrTableNotFound) {
			t.Errorf("Rows(%q.%q): err = %v", a.schema, a.table, err)
		}
	}
	if _, err := v.Rows(ctx, id, "viewer_fixture", "authors", 1); err != nil {
		t.Fatalf("authors rusak setelah percobaan injection: %v", err)
	}
}

func TestViewerRowsPagination(t *testing.T) {
	ctx, v, id := newViewer(t)
	tests := []struct {
		page, rows int
		hasNext    bool
	}{
		{1, 50, true},
		{2, 50, true},
		{3, 20, false},
		{0, 50, true},
	}
	for _, tt := range tests {
		dp, err := v.Rows(ctx, id, "viewer_fixture", "books", tt.page)
		must(t, err)
		if len(dp.Rows) != tt.rows || dp.HasNext != tt.hasNext {
			t.Errorf("page %d: rows=%d hasNext=%v, want %d/%v", tt.page, len(dp.Rows), dp.HasNext, tt.rows, tt.hasNext)
		}
	}
	dp, _ := v.Rows(ctx, id, "viewer_fixture", "books", 1)
	if got := strings.Join(dp.Columns, ","); got != "id,author_id,title,pages,price" {
		t.Errorf("columns = %s", got)
	}
}

func TestViewerRowsNullAndClip(t *testing.T) {
	ctx, v, id := newViewer(t)
	dp, err := v.Rows(ctx, id, "viewer_fixture", "authors", 1)
	must(t, err)

	byName := map[string][]dbmanager.Cell{}
	for _, r := range dp.Rows {
		byName[r[1].Text] = r
	}
	ann, bob := byName["Ann"], byName["Bob"]
	if ann == nil || bob == nil {
		t.Fatalf("baris Ann/Bob tidak ditemukan: %+v", dp.Rows)
	}
	if !ann[3].Null {
		t.Error("bio Ann seharusnya NULL")
	}
	if n := utf8.RuneCountInString(bob[3].Text); n != 201 || !strings.HasSuffix(bob[3].Text, "…") {
		t.Errorf("bio Bob: %d rune, want 201 (200 + …)", n)
	}
}
package handlers_test

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/access"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/dbmanager"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/web/handlers"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/web/templates"
)

type fakeReg struct {
	dbs       []dbmanager.Database
	created   []dbmanager.DatabaseInput
	updated   []dbmanager.DatabaseInput
	deleted   []int64
	createErr error
}

func (f *fakeReg) Create(_ context.Context, in dbmanager.DatabaseInput) (dbmanager.Database, error) {
	f.created = append(f.created, in)
	return dbmanager.Database{}, f.createErr
}
func (f *fakeReg) List(context.Context) ([]dbmanager.Database, error) { return f.dbs, nil }
func (f *fakeReg) Get(_ context.Context, id int64) (dbmanager.Database, error) {
	for _, d := range f.dbs {
		if d.ID == id {
			return d, nil
		}
	}
	return dbmanager.Database{}, dbmanager.ErrDatabaseNotFound
}
func (f *fakeReg) Update(_ context.Context, _ int64, in dbmanager.DatabaseInput) error {
	f.updated = append(f.updated, in)
	return nil
}
func (f *fakeReg) Delete(_ context.Context, id int64) error {
	f.deleted = append(f.deleted, id)
	return nil
}
func (f *fakeReg) Pool(context.Context, int64) (*pgxpool.Pool, error) {
	return nil, &dbmanager.ConnError{Reason: "server tidak dapat dihubungi"}
}

type fakeViewer struct {
	tables    []dbmanager.Table
	info      dbmanager.TableInfo
	data      dbmanager.DataPage
	err       error
	rowsPages []int
}

func (f *fakeViewer) Tables(context.Context, int64) ([]dbmanager.Table, error) {
	return f.tables, f.err
}
func (f *fakeViewer) TableInfo(context.Context, int64, string, string) (dbmanager.TableInfo, error) {
	return f.info, f.err
}
func (f *fakeViewer) Rows(_ context.Context, _ int64, _, _ string, page int) (dbmanager.DataPage, error) {
	f.rowsPages = append(f.rowsPages, page)
	return f.data, f.err
}

type fakeChecker struct{ levels map[int64]access.Level }

func (f fakeChecker) IsPlatformAdmin(context.Context, int64) (bool, error) { return false, nil }
func (f fakeChecker) EffectiveLevel(_ context.Context, _, dbID int64) (access.Level, error) {
	return f.levels[dbID], nil
}

type dbRig struct {
	h      *handlers.DatabaseHandler
	reg    *fakeReg
	viewer *fakeViewer
}

func newDBRig(t *testing.T, levels map[int64]access.Level, test handlers.ConnTester) dbRig {
	t.Helper()
	tmpl, err := templates.New()
	if err != nil {
		t.Fatal(err)
	}
	reg := &fakeReg{dbs: []dbmanager.Database{
		{ID: 1, Name: "sales", Host: "db1", Port: 5432, DBName: "sales", DBUser: "app", SSLMode: "prefer"},
		{ID: 2, Name: "payroll", Host: "db2", Port: 5432, DBName: "payroll", DBUser: "app", SSLMode: "prefer"},
	}}
	v := &fakeViewer{tables: []dbmanager.Table{{Schema: "public", Name: "orders"}}}
	return dbRig{h: handlers.NewDatabaseHandler(reg, v, fakeChecker{levels}, test, tmpl), reg: reg, viewer: v}
}

func TestDatabases_SelectListsOnlyAccessible(t *testing.T) {
	rig := newDBRig(t, map[int64]access.Level{1: access.LevelEdit}, nil)
	rec := serve(rig.h.SelectPage, adminRequest(http.MethodGet, "/databases", nil, nil))

	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, "sales") || !strings.Contains(body, "View &#43; Edit") {
		t.Fatalf("status %d, body missing accessible database", rec.Code)
	}
	if strings.Contains(body, "payroll") {
		t.Error("a database without a grant was listed")
	}
	if strings.Contains(body, "db1") {
		t.Error("host leaked to a normal user")
	}
}

func TestDatabases_AdminCreatePassesFormAndHidesPassword(t *testing.T) {
	rig := newDBRig(t, nil, nil)
	rec := serve(rig.h.AdminCreate, adminRequest(http.MethodPost, "/admin/databases", url.Values{
		"name": {"new"}, "host": {"h"}, "port": {"5433"}, "db_name": {"d"}, "db_user": {"u"},
		"password": {"s3cret-pw"}, "sslmode": {"disable"},
	}, nil))

	if rec.Code != http.StatusOK || len(rig.reg.created) != 1 {
		t.Fatalf("status %d, created %d", rec.Code, len(rig.reg.created))
	}
	if got := rig.reg.created[0]; got.Port != 5433 || got.Password != "s3cret-pw" || got.SSLMode != "disable" {
		t.Errorf("input = %+v", got)
	}
	if strings.Contains(rec.Body.String(), "s3cret-pw") {
		t.Error("password echoed in response")
	}
}

func TestDatabases_AdminCreateErrorsStayOnForm(t *testing.T) {
	rig := newDBRig(t, nil, nil)
	rig.reg.createErr = dbmanager.ErrNameTaken
	rec := serve(rig.h.AdminCreate, adminRequest(http.MethodPost, "/admin/databases", url.Values{
		"name": {"sales"}, "password": {"s3cret-pw"},
	}, nil))

	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "already exists") {
		t.Errorf("status %d, body %q", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "s3cret-pw") {
		t.Error("password echoed after a failed create")
	}
}

func TestDatabases_TestNewConnection(t *testing.T) {
	for name, tc := range map[string]struct {
		err  error
		want string
	}{
		"ok":     {nil, "Connection successful"},
		"failed": {&dbmanager.ConnError{Reason: "koneksi timeout"}, "Could not connect"},
	} {
		t.Run(name, func(t *testing.T) {
			rig := newDBRig(t, nil, func(context.Context, dbmanager.DatabaseInput) error { return tc.err })
			rec := serve(rig.h.AdminTestNew, adminRequest(http.MethodPost, "/admin/databases/test", url.Values{"host": {"h"}}, nil))
			if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), tc.want) {
				t.Errorf("status %d, body %q", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestDatabases_UpdateWithEmptyPasswordKeepsOld(t *testing.T) {
	rig := newDBRig(t, nil, nil)
	rec := serve(rig.h.AdminUpdate, adminRequest(http.MethodPost, "/admin/databases/1", url.Values{
		"name": {"sales"}, "host": {"db1"}, "port": {"5432"}, "db_name": {"sales"}, "db_user": {"app"}, "sslmode": {"prefer"},
	}, map[string]string{"id": "1"}))

	if rec.Code != http.StatusOK || len(rig.reg.updated) != 1 || rig.reg.updated[0].Password != "" {
		t.Fatalf("status %d, updated %+v", rec.Code, rig.reg.updated)
	}
}

func TestDatabases_DeleteAndUnknownID(t *testing.T) {
	rig := newDBRig(t, nil, nil)
	if rec := serve(rig.h.AdminDelete, adminRequest(http.MethodPost, "/admin/databases/2/delete", url.Values{}, map[string]string{"id": "2"})); rec.Code != http.StatusOK || len(rig.reg.deleted) != 1 {
		t.Errorf("delete: status %d, deleted %v", rec.Code, rig.reg.deleted)
	}
	if rec := serve(rig.h.AdminEditPage, adminRequest(http.MethodGet, "/admin/databases/99", nil, map[string]string{"id": "99"})); rec.Code != http.StatusNotFound {
		t.Errorf("unknown id: status %d, want 404", rec.Code)
	}
	if rec := serve(rig.h.AdminEditPage, adminRequest(http.MethodGet, "/admin/databases/abc", nil, map[string]string{"id": "abc"})); rec.Code != http.StatusNotFound {
		t.Errorf("bad id: status %d, want 404", rec.Code)
	}
}

func TestDatabases_TablesPage(t *testing.T) {
	rig := newDBRig(t, map[int64]access.Level{1: access.LevelView}, nil)
	rec := serve(rig.h.TablesPage, adminRequest(http.MethodGet, "/databases/1", nil, map[string]string{"dbID": "1"}))

	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "orders") || !strings.Contains(rec.Body.String(), "View Only") {
		t.Errorf("status %d, body %q", rec.Code, rec.Body.String())
	}
}

func TestDatabases_NoAccessIs404(t *testing.T) {
	rig := newDBRig(t, map[int64]access.Level{}, nil)
	rec := serve(rig.h.TablesPage, adminRequest(http.MethodGet, "/databases/1", nil, map[string]string{"dbID": "1"}))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status %d, want 404", rec.Code)
	}
}

func TestDatabases_TablePageTabs(t *testing.T) {
	d := "now()"
	rig := newDBRig(t, map[int64]access.Level{1: access.LevelView}, nil)
	rig.viewer.info = dbmanager.TableInfo{
		Columns:     []dbmanager.Column{{Name: "id", Type: "integer"}, {Name: "created", Type: "timestamptz", Default: &d}},
		Constraints: []dbmanager.Constraint{{Name: "orders_pkey", Type: "PRIMARY KEY", Definition: "PRIMARY KEY (id)"}},
		Indexes:     []dbmanager.Index{{Name: "orders_pkey", Unique: true, Method: "btree", Definition: "CREATE UNIQUE INDEX ..."}},
	}
	rig.viewer.data = dbmanager.DataPage{
		Columns: []string{"id", "note"}, Page: 2, HasNext: true,
		Rows: [][]dbmanager.Cell{{{Text: "1"}, {Null: true}}, {{Text: "<b>x</b>"}, {Text: "y"}}},
	}

	get := func(query string) string {
		rec := serve(rig.h.TablePage, adminRequest(http.MethodGet, "/databases/1/table?"+query, nil, map[string]string{"dbID": "1"}))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d", query, rec.Code)
		}
		return rec.Body.String()
	}

	for query, want := range map[string]string{
		"schema=public&table=orders":                 "timestamptz",
		"schema=public&table=orders&tab=constraints": "PRIMARY KEY (id)",
		"schema=public&table=orders&tab=indexes":     "btree",
		"schema=public&table=orders&tab=data&page=2": "NULL",
	} {
		if body := get(query); !strings.Contains(body, want) {
			t.Errorf("%s: missing %q", query, want)
		}
	}
	body := get("schema=public&table=orders&tab=data&page=2")
	if strings.Contains(body, "<b>x</b>") {
		t.Error("cell content was not escaped")
	}
	if !strings.Contains(body, "Previous") || !strings.Contains(body, "Next") {
		t.Error("pager links missing")
	}
	if len(rig.viewer.rowsPages) == 0 || rig.viewer.rowsPages[len(rig.viewer.rowsPages)-1] != 2 {
		t.Errorf("rows pages = %v", rig.viewer.rowsPages)
	}
}

func TestDatabases_TablePageBadInput(t *testing.T) {
	rig := newDBRig(t, map[int64]access.Level{1: access.LevelView}, nil)
	for _, q := range []string{"", "schema=public", "schema=public&table=orders&tab=bogus"} {
		rec := serve(rig.h.TablePage, adminRequest(http.MethodGet, "/databases/1/table?"+q, nil, map[string]string{"dbID": "1"}))
		if rec.Code != http.StatusNotFound {
			t.Errorf("%q: status %d, want 404", q, rec.Code)
		}
	}
}

func TestDatabases_ViewerFailures(t *testing.T) {
	for name, tc := range map[string]struct {
		err  error
		code int
		want string
	}{
		"connection": {&dbmanager.ConnError{Reason: "koneksi timeout"}, http.StatusBadGateway, "Cannot reach sales"},
		"no table":   {dbmanager.ErrTableNotFound, http.StatusNotFound, ""},
		"other":      {errors.New("boom password=hunter2"), http.StatusInternalServerError, ""},
	} {
		t.Run(name, func(t *testing.T) {
			rig := newDBRig(t, map[int64]access.Level{1: access.LevelView}, nil)
			rig.viewer.err = tc.err
			rec := serve(rig.h.TablePage, adminRequest(http.MethodGet, "/databases/1/table?schema=public&table=orders", nil, map[string]string{"dbID": "1"}))
			if rec.Code != tc.code || !strings.Contains(rec.Body.String(), tc.want) {
				t.Errorf("status %d, body %q", rec.Code, rec.Body.String())
			}
			if strings.Contains(rec.Body.String(), "hunter2") {
				t.Error("internal error detail leaked to the client")
			}
		})
	}
}

package handlers

import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/access"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/dbmanager"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/web/templates"
)

type DatabaseRegistry interface {
	Create(ctx context.Context, in dbmanager.DatabaseInput) (dbmanager.Database, error)
	List(ctx context.Context) ([]dbmanager.Database, error)
	Get(ctx context.Context, id int64) (dbmanager.Database, error)
	Update(ctx context.Context, id int64, in dbmanager.DatabaseInput) error
	Delete(ctx context.Context, id int64) error
	Pool(ctx context.Context, id int64) (*pgxpool.Pool, error)
}

type DatabaseViewer interface {
	Tables(ctx context.Context, dbID int64) ([]dbmanager.Table, error)
	TableInfo(ctx context.Context, dbID int64, schema, table string) (dbmanager.TableInfo, error)
	Rows(ctx context.Context, dbID int64, schema, table string, page int) (dbmanager.DataPage, error)
}

type ConnTester func(ctx context.Context, in dbmanager.DatabaseInput) error

type DatabaseHandler struct {
	reg     DatabaseRegistry
	viewer  DatabaseViewer
	checker access.Checker
	test    ConnTester
	tmpl    *templates.Set
}

func NewDatabaseHandler(reg DatabaseRegistry, viewer DatabaseViewer, checker access.Checker, test ConnTester, tmpl *templates.Set) *DatabaseHandler {
	if test == nil {
		test = dbmanager.TestConnection
	}
	return &DatabaseHandler{reg: reg, viewer: viewer, checker: checker, test: test, tmpl: tmpl}
}

type dbForm struct {
	Name, Host, DBName, DBUser, SSLMode string
	Port                                int
}

func (f dbForm) input(password string) dbmanager.DatabaseInput {
	return dbmanager.DatabaseInput{
		Name: f.Name, Host: f.Host, Port: f.Port, DBName: f.DBName,
		DBUser: f.DBUser, Password: password, SSLMode: f.SSLMode,
	}
}

func formFromDatabase(d dbmanager.Database) dbForm {
	return dbForm{Name: d.Name, Host: d.Host, Port: d.Port, DBName: d.DBName, DBUser: d.DBUser, SSLMode: d.SSLMode}
}

func readDBForm(r *http.Request) dbForm {
	port, _ := strconv.Atoi(strings.TrimSpace(r.PostFormValue("port")))
	return dbForm{
		Name:    strings.TrimSpace(r.PostFormValue("name")),
		Host:    strings.TrimSpace(r.PostFormValue("host")),
		Port:    port,
		DBName:  strings.TrimSpace(r.PostFormValue("db_name")),
		DBUser:  strings.TrimSpace(r.PostFormValue("db_user")),
		SSLMode: strings.TrimSpace(r.PostFormValue("sslmode")),
	}
}

type databasesView struct {
	notice
	Databases []dbmanager.Database
	Form      dbForm
}

type databaseEditView struct {
	notice
	Database dbmanager.Database
	Form     dbForm
}

type connResult struct {
	OK      bool
	Message string
}

type accessibleDB struct {
	ID    int64
	Name  string
	Level access.Level
}

type selectView struct {
	Databases []accessibleDB
}

type tableLink struct {
	Name   string
	URL    string
	Active bool
}

type schemaGroup struct {
	Name   string
	Tables []tableLink
}

type tablesView struct {
	DatabaseID   int64
	DatabaseName string
	Level        access.Level
	Schemas      []schemaGroup
}

type tabLink struct {
	Label  string
	Key    string
	URL    string
	Active bool
}

type tableView struct {
	DatabaseID   int64
	DatabaseName string
	Level        access.Level
	Schemas      []schemaGroup
	Schema       string
	Table        string
	Tab          string
	Tabs         []tabLink
	Info         dbmanager.TableInfo
	Data         dbmanager.DataPage
	PrevURL      string
	NextURL      string
}

type errorView struct {
	Heading string
	Message string
	BackURL string
}

func (h *DatabaseHandler) fail(w http.ResponseWriter, op string, err error) {
	if errors.Is(err, dbmanager.ErrDatabaseNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	log.Printf("databases: %s: %v", op, err)
	http.Error(w, "internal error", http.StatusInternalServerError)
}

func inputMessage(err error) (string, bool) {
	switch {
	case errors.Is(err, dbmanager.ErrNameTaken):
		return "A database with that name already exists.", true
	case errors.Is(err, dbmanager.ErrInvalidInput):
		return strings.TrimPrefix(err.Error(), dbmanager.ErrInvalidInput.Error()+": "), true
	}
	return "", false
}

func connMessage(err error) (string, bool) {
	var ce *dbmanager.ConnError
	if errors.As(err, &ce) {
		return "Could not connect: " + ce.Reason + ".", true
	}
	if errors.Is(err, dbmanager.ErrConnectionFailed) {
		return "Could not connect to the database.", true
	}
	return "", false
}

func (h *DatabaseHandler) renderDatabases(w http.ResponseWriter, r *http.Request, n notice, form dbForm) {
	list, err := h.reg.List(r.Context())
	if err != nil {
		h.fail(w, "list", err)
		return
	}
	h.tmpl.Fragment(w, http.StatusOK, "admin/databases", "databases", databasesView{notice: n, Databases: list, Form: form})
}

func (h *DatabaseHandler) AdminListPage(w http.ResponseWriter, r *http.Request) {
	list, err := h.reg.List(r.Context())
	if err != nil {
		h.fail(w, "list", err)
		return
	}
	h.tmpl.Page(w, r, http.StatusOK, "admin/databases", "Databases", databasesView{Databases: list, Form: dbForm{Port: 5432, SSLMode: "prefer"}})
}

func (h *DatabaseHandler) AdminCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	form := readDBForm(r)
	_, err := h.reg.Create(r.Context(), form.input(r.PostFormValue("password")))
	if err == nil {
		h.renderDatabases(w, r, notice{Message: "Database added."}, dbForm{Port: 5432, SSLMode: "prefer"})
		return
	}
	if msg, ok := inputMessage(err); ok {
		h.renderDatabases(w, r, notice{Error: msg}, form)
		return
	}
	h.fail(w, "create", err)
}

func (h *DatabaseHandler) AdminTestNew(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	in := readDBForm(r).input(r.PostFormValue("password"))
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	err := h.test(ctx, in)
	h.renderConnResult(w, "test new", err)
}

func (h *DatabaseHandler) renderConnResult(w http.ResponseWriter, op string, err error) {
	res := connResult{OK: true, Message: "Connection successful."}
	if err != nil {
		if msg, ok := inputMessage(err); ok {
			res = connResult{Message: msg}
		} else if msg, ok := connMessage(err); ok {
			res = connResult{Message: msg}
		} else {
			log.Printf("databases: %s: %v", op, err)
			res = connResult{Message: "Could not test the connection."}
		}
	}
	h.tmpl.Fragment(w, http.StatusOK, "admin/database", "conn_result", res)
}

func (h *DatabaseHandler) AdminEditPage(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		http.NotFound(w, r)
		return
	}
	d, err := h.reg.Get(r.Context(), id)
	if err != nil {
		h.fail(w, "get", err)
		return
	}
	h.tmpl.Page(w, r, http.StatusOK, "admin/database", "Edit database", databaseEditView{Database: d, Form: formFromDatabase(d)})
}

func (h *DatabaseHandler) AdminUpdate(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	form := readDBForm(r)
	err := h.reg.Update(r.Context(), id, form.input(r.PostFormValue("password")))

	n := notice{Message: "Database updated. Leave the password empty to keep the current one."}
	if err != nil {
		if msg, ok := inputMessage(err); ok {
			n = notice{Error: msg}
		} else {
			h.fail(w, "update", err)
			return
		}
	}
	d, gerr := h.reg.Get(r.Context(), id)
	if gerr != nil {
		h.fail(w, "get", gerr)
		return
	}
	if err != nil {
		h.tmpl.Fragment(w, http.StatusOK, "admin/database", "database_form", databaseEditView{notice: n, Database: d, Form: form})
		return
	}
	h.tmpl.Fragment(w, http.StatusOK, "admin/database", "database_form", databaseEditView{notice: n, Database: d, Form: formFromDatabase(d)})
}

func (h *DatabaseHandler) AdminTestSaved(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		http.NotFound(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	pool, err := h.reg.Pool(ctx, id)
	if err == nil {
		if perr := pool.Ping(ctx); perr != nil {
			err = &dbmanager.ConnError{Reason: "server tidak dapat dihubungi"}
		}
	}
	if errors.Is(err, dbmanager.ErrDatabaseNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	h.renderConnResult(w, "test saved", err)
}

func (h *DatabaseHandler) AdminDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		http.NotFound(w, r)
		return
	}
	if err := h.reg.Delete(r.Context(), id); err != nil {
		h.fail(w, "delete", err)
		return
	}
	h.renderDatabases(w, r, notice{Message: "Database removed. Its group grants and migration history were removed too."}, dbForm{Port: 5432, SSLMode: "prefer"})
}

func (h *DatabaseHandler) SelectPage(w http.ResponseWriter, r *http.Request) {
	user, ok := access.UserFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthenticated", http.StatusUnauthorized)
		return
	}
	all, err := h.reg.List(r.Context())
	if err != nil {
		h.fail(w, "list", err)
		return
	}
	var out []accessibleDB
	for _, d := range all {
		level, err := h.checker.EffectiveLevel(r.Context(), user.ID, d.ID)
		if err != nil {
			h.fail(w, "effective level", err)
			return
		}
		if level > access.LevelNone {
			out = append(out, accessibleDB{ID: d.ID, Name: d.Name, Level: level})
		}
	}
	h.tmpl.Page(w, r, http.StatusOK, "db/select", "Select database", selectView{Databases: out})
}

func tableURL(dbID int64, schema, table, tab string, page int) string {
	q := url.Values{"schema": {schema}, "table": {table}}
	if tab != "" && tab != "columns" {
		q.Set("tab", tab)
	}
	if page > 1 {
		q.Set("page", strconv.Itoa(page))
	}
	return "/databases/" + strconv.FormatInt(dbID, 10) + "/table?" + q.Encode()
}

func groupTables(dbID int64, tables []dbmanager.Table, activeSchema, activeTable string) []schemaGroup {
	var groups []schemaGroup
	for _, t := range tables {
		if len(groups) == 0 || groups[len(groups)-1].Name != t.Schema {
			groups = append(groups, schemaGroup{Name: t.Schema})
		}
		g := &groups[len(groups)-1]
		g.Tables = append(g.Tables, tableLink{
			Name:   t.Name,
			URL:    tableURL(dbID, t.Schema, t.Name, "", 1),
			Active: t.Schema == activeSchema && t.Name == activeTable,
		})
	}
	return groups
}

func (h *DatabaseHandler) viewerContext(w http.ResponseWriter, r *http.Request) (dbmanager.Database, access.Level, bool) {
	id, ok := pathID(r, access.DatabaseIDParam)
	if !ok {
		http.NotFound(w, r)
		return dbmanager.Database{}, access.LevelNone, false
	}
	user, _ := access.UserFromContext(r.Context())
	level, err := h.checker.EffectiveLevel(r.Context(), user.ID, id)
	if err != nil {
		h.fail(w, "effective level", err)
		return dbmanager.Database{}, access.LevelNone, false
	}
	if level == access.LevelNone {
		http.NotFound(w, r)
		return dbmanager.Database{}, access.LevelNone, false
	}
	d, err := h.reg.Get(r.Context(), id)
	if err != nil {
		h.fail(w, "get", err)
		return dbmanager.Database{}, access.LevelNone, false
	}
	return d, level, true
}

func (h *DatabaseHandler) viewerFailure(w http.ResponseWriter, r *http.Request, d dbmanager.Database, op string, err error) {
	back := "/databases/" + strconv.FormatInt(d.ID, 10)
	switch {
	case errors.Is(err, dbmanager.ErrDatabaseNotFound), errors.Is(err, dbmanager.ErrTableNotFound):
		http.NotFound(w, r)
	case errors.Is(err, dbmanager.ErrConnectionFailed):
		msg, _ := connMessage(err)
		log.Printf("databases: %s (db %d): %v", op, d.ID, err)
		h.tmpl.Page(w, r, http.StatusBadGateway, "db/error", "Connection failed", errorView{
			Heading: "Cannot reach " + d.Name,
			Message: msg + " Ask a platform admin to check the connection settings.",
			BackURL: "/databases",
		})
	default:
		log.Printf("databases: %s (db %d): %v", op, d.ID, err)
		h.tmpl.Page(w, r, http.StatusInternalServerError, "db/error", "Error", errorView{
			Heading: "Something went wrong",
			Message: "The request could not be completed.",
			BackURL: back,
		})
	}
}

func (h *DatabaseHandler) TablesPage(w http.ResponseWriter, r *http.Request) {
	d, level, ok := h.viewerContext(w, r)
	if !ok {
		return
	}
	tables, err := h.viewer.Tables(r.Context(), d.ID)
	if err != nil {
		h.viewerFailure(w, r, d, "tables", err)
		return
	}
	h.tmpl.Page(w, r, http.StatusOK, "db/tables", d.Name, tablesView{
		DatabaseID: d.ID, DatabaseName: d.Name, Level: level,
		Schemas: groupTables(d.ID, tables, "", ""),
	})
}

var tabOrder = []struct{ Key, Label string }{
	{"columns", "Columns"}, {"constraints", "Constraints"}, {"indexes", "Indexes"}, {"data", "Data"},
}

func (h *DatabaseHandler) TablePage(w http.ResponseWriter, r *http.Request) {
	d, level, ok := h.viewerContext(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	schema, table := q.Get("schema"), q.Get("table")
	if schema == "" || table == "" {
		http.NotFound(w, r)
		return
	}
	tab := q.Get("tab")
	switch tab {
	case "columns", "constraints", "indexes", "data":
	case "":
		tab = "columns"
	default:
		http.NotFound(w, r)
		return
	}
	page, _ := strconv.Atoi(q.Get("page"))
	if page < 1 {
		page = 1
	}

	view := tableView{DatabaseID: d.ID, DatabaseName: d.Name, Level: level, Schema: schema, Table: table, Tab: tab}

	if tab == "data" {
		dp, err := h.viewer.Rows(r.Context(), d.ID, schema, table, page)
		if err != nil {
			h.viewerFailure(w, r, d, "rows", err)
			return
		}
		view.Data = dp
		if dp.Page > 1 {
			view.PrevURL = tableURL(d.ID, schema, table, "data", dp.Page-1)
		}
		if dp.HasNext {
			view.NextURL = tableURL(d.ID, schema, table, "data", dp.Page+1)
		}
	} else {
		info, err := h.viewer.TableInfo(r.Context(), d.ID, schema, table)
		if err != nil {
			h.viewerFailure(w, r, d, "table info", err)
			return
		}
		view.Info = info
	}

	tables, err := h.viewer.Tables(r.Context(), d.ID)
	if err != nil {
		h.viewerFailure(w, r, d, "tables", err)
		return
	}
	view.Schemas = groupTables(d.ID, tables, schema, table)
	for _, t := range tabOrder {
		view.Tabs = append(view.Tabs, tabLink{
			Label: t.Label, Key: t.Key, Active: t.Key == tab,
			URL: tableURL(d.ID, schema, table, t.Key, 1),
		})
	}
	h.tmpl.Page(w, r, http.StatusOK, "db/table", schema+"."+table, view)
}
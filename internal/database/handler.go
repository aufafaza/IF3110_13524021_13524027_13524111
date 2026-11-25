package database

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/access"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/web/templates"
)

type DatabaseRegistry interface {
	Create(ctx context.Context, in DatabaseInput) (Database, error)
	List(ctx context.Context) ([]Database, error)
	Get(ctx context.Context, id int64) (Database, error)
	Update(ctx context.Context, id int64, in DatabaseInput) error
	Delete(ctx context.Context, id int64) error
	Pool(ctx context.Context, id int64) (*pgxpool.Pool, error)
}

type DatabaseViewer interface {
	Tables(ctx context.Context, dbID int64) ([]Table, error)
	TableInfo(ctx context.Context, dbID int64, schema, table string) (TableInfo, error)
	Rows(ctx context.Context, dbID int64, schema, table string, page int) (DataPage, error)
}

type ConnTester func(ctx context.Context, in DatabaseInput) error

type Handler struct {
	reg     DatabaseRegistry
	viewer  DatabaseViewer
	checker access.DatabaseChecker
	test    ConnTester
	tmpl    *templates.Set
}

func NewHandler(reg DatabaseRegistry, viewer DatabaseViewer, checker access.DatabaseChecker, test ConnTester, tmpl *templates.Set) *Handler {
	if test == nil {
		test = TestConnection
	}
	return &Handler{reg: reg, viewer: viewer, checker: checker, test: test, tmpl: tmpl}
}

type dbForm struct {
	Name, Host, DBName, DBUser, SSLMode string
	Port                                int
}

func (f dbForm) input(password string) DatabaseInput {
	return DatabaseInput{
		Name: f.Name, Host: f.Host, Port: f.Port, DBName: f.DBName,
		DBUser: f.DBUser, Password: password, SSLMode: f.SSLMode,
	}
}

func formFromDatabase(d Database) dbForm {
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
	Databases []Database
	Form      dbForm
}

type databaseEditView struct {
	notice
	Database Database
	Form     dbForm
}

type connResult struct {
	OK      bool
	Message string
}

type accessibleDB struct {
	ID    int64        `json:"id"`
	Name  string       `json:"name"`
	Level access.Level `json:"-"`
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
	Info         TableInfo
	Data         DataPage
	PrevURL      string
	NextURL      string
}

type errorView struct {
	Heading string
	Message string
	BackURL string
}

func (h *Handler) fail(w http.ResponseWriter, op string, err error) {
	if errors.Is(err, ErrDatabaseNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	log.Printf("databases: %s: %v", op, err)
	http.Error(w, "internal error", http.StatusInternalServerError)
}

func (h *Handler) RegisterAPIRoutes(mux *http.ServeMux, guard *access.Guard) {
	admin := guard.RequireAdmin
	login := guard.RequireLogin
	mux.Handle("GET /api/admin/databases", admin(http.HandlerFunc(h.apiAdminDatabases)))
	mux.Handle("POST /api/admin/databases", admin(http.HandlerFunc(h.apiAdminCreateDatabase)))
	mux.Handle("GET /api/admin/databases/{id}", admin(http.HandlerFunc(h.apiAdminDatabase)))
	mux.Handle("PUT /api/admin/databases/{id}", admin(http.HandlerFunc(h.apiAdminUpdateDatabase)))
	mux.Handle("DELETE /api/admin/databases/{id}", admin(http.HandlerFunc(h.apiAdminDeleteDatabase)))
	mux.Handle("GET /api/databases/{dbID}/schema", login(http.HandlerFunc(h.apiSchema)))
	mux.Handle("GET /api/databases/{dbID}/table", login(http.HandlerFunc(h.apiTable)))
}

func databaseAPIJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func databaseAPIError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	message := "internal error"
	switch {
	case errors.Is(err, ErrDatabaseNotFound), errors.Is(err, ErrTableNotFound):
		status, message = http.StatusNotFound, "not found"
	case errors.Is(err, ErrInvalidInput), errors.Is(err, ErrNameTaken):
		status, message = http.StatusBadRequest, err.Error()
	}
	if status == http.StatusInternalServerError {
		log.Printf("database api: %v", err)
	}
	databaseAPIJSON(w, status, map[string]string{"error": message})
}

func readDatabaseAPIJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		databaseAPIJSON(w, http.StatusUnsupportedMediaType, map[string]string{"error": "Content-Type must be application/json"})
		return false
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		databaseAPIJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return false
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		databaseAPIJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return false
	}
	return true
}

type databaseAPIView struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Host      string    `json:"host"`
	Port      int       `json:"port"`
	DBName    string    `json:"db_name"`
	DBUser    string    `json:"db_user"`
	SSLMode   string    `json:"sslmode"`
	CreatedAt time.Time `json:"created_at"`
}

func publicDatabase(d Database) databaseAPIView {
	return databaseAPIView{d.ID, d.Name, d.Host, d.Port, d.DBName, d.DBUser, d.SSLMode, d.CreatedAt}
}

func (h *Handler) apiAdminDatabases(w http.ResponseWriter, r *http.Request) {
	dbs, err := h.reg.List(r.Context())
	if err != nil {
		databaseAPIError(w, err)
		return
	}
	out := make([]databaseAPIView, 0, len(dbs))
	for _, d := range dbs {
		out = append(out, publicDatabase(d))
	}
	databaseAPIJSON(w, http.StatusOK, map[string]any{"databases": out})
}

func (h *Handler) apiAdminCreateDatabase(w http.ResponseWriter, r *http.Request) {
	var in DatabaseInput
	if !readDatabaseAPIJSON(w, r, &in) {
		return
	}
	d, err := h.reg.Create(r.Context(), in)
	if err != nil {
		databaseAPIError(w, err)
		return
	}
	databaseAPIJSON(w, http.StatusCreated, publicDatabase(d))
}

func (h *Handler) apiAdminDatabase(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		databaseAPIJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid database id"})
		return
	}
	d, err := h.reg.Get(r.Context(), id)
	if err != nil {
		databaseAPIError(w, err)
		return
	}
	databaseAPIJSON(w, http.StatusOK, publicDatabase(d))
}

func (h *Handler) apiAdminUpdateDatabase(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		databaseAPIJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid database id"})
		return
	}
	var in DatabaseInput
	if !readDatabaseAPIJSON(w, r, &in) {
		return
	}
	if err := h.reg.Update(r.Context(), id, in); err != nil {
		databaseAPIError(w, err)
		return
	}
	d, err := h.reg.Get(r.Context(), id)
	if err != nil {
		databaseAPIError(w, err)
		return
	}
	databaseAPIJSON(w, http.StatusOK, publicDatabase(d))
}

func (h *Handler) apiAdminDeleteDatabase(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		databaseAPIJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid database id"})
		return
	}
	if err := h.reg.Delete(r.Context(), id); err != nil {
		databaseAPIError(w, err)
		return
	}
	databaseAPIJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}

func (h *Handler) apiSelect(w http.ResponseWriter, r *http.Request) {
	user, ok := access.UserFromContext(r.Context())
	if !ok {
		databaseAPIJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthenticated"})
		return
	}
	dbs, err := h.reg.List(r.Context())
	if err != nil {
		databaseAPIError(w, err)
		return
	}
	type selected struct {
		ID    int64        `json:"id"`
		Name  string       `json:"name"`
		Level access.Level `json:"level"`
	}
	out := make([]selected, 0)
	for _, d := range dbs {
		level, err := h.checker.EffectiveLevel(r.Context(), user.ID, d.ID)
		if err != nil {
			databaseAPIError(w, err)
			return
		}
		if level > access.LevelNone {
			out = append(out, selected{d.ID, d.Name, level})
		}
	}
	databaseAPIJSON(w, http.StatusOK, map[string]any{"databases": out})
}

func (h *Handler) apiSchema(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, access.DatabaseIDParam)
	if !ok {
		databaseAPIJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid database id"})
		return
	}
	user, _ := access.UserFromContext(r.Context())
	level, err := h.checker.EffectiveLevel(r.Context(), user.ID, id)
	if err != nil || level < access.LevelView {
		if err != nil {
			databaseAPIError(w, err)
		} else {
			databaseAPIJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		}
		return
	}
	tables, err := h.viewer.Tables(r.Context(), id)
	if err != nil {
		databaseAPIError(w, err)
		return
	}
	if tables == nil {
		tables = []Table{}
	}
	databaseAPIJSON(w, http.StatusOK, map[string]any{"database_id": id, "tables": tables})
}

func (h *Handler) apiTable(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, access.DatabaseIDParam)
	if !ok {
		databaseAPIJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid database id"})
		return
	}
	user, _ := access.UserFromContext(r.Context())
	level, err := h.checker.EffectiveLevel(r.Context(), user.ID, id)
	if err != nil || level < access.LevelView {
		if err != nil {
			databaseAPIError(w, err)
		} else {
			databaseAPIJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		}
		return
	}
	schema, table := r.URL.Query().Get("schema"), r.URL.Query().Get("table")
	if schema == "" || table == "" {
		databaseAPIJSON(w, http.StatusBadRequest, map[string]string{"error": "schema and table are required"})
		return
	}
	if r.URL.Query().Get("data") == "true" {
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		data, err := h.viewer.Rows(r.Context(), id, schema, table, page)
		if err != nil {
			databaseAPIError(w, err)
			return
		}
		databaseAPIJSON(w, http.StatusOK, data)
		return
	}
	info, err := h.viewer.TableInfo(r.Context(), id, schema, table)
	if err != nil {
		databaseAPIError(w, err)
		return
	}
	databaseAPIJSON(w, http.StatusOK, info)
}

func inputMessage(err error) (string, bool) {
	switch {
	case errors.Is(err, ErrNameTaken):
		return "A database with that name already exists.", true
	case errors.Is(err, ErrInvalidInput):
		return strings.TrimPrefix(err.Error(), ErrInvalidInput.Error()+": "), true
	}
	return "", false
}

func connMessage(err error) (string, bool) {
	var ce *ConnError
	if errors.As(err, &ce) {
		return "Could not connect: " + ce.Reason + ".", true
	}
	if errors.Is(err, ErrConnectionFailed) {
		return "Could not connect to the database.", true
	}
	return "", false
}

func (h *Handler) renderDatabases(w http.ResponseWriter, r *http.Request, n notice, form dbForm) {
	list, err := h.reg.List(r.Context())
	if err != nil {
		h.fail(w, "list", err)
		return
	}
	h.tmpl.Fragment(w, http.StatusOK, "admin/databases", "databases", databasesView{notice: n, Databases: list, Form: form})
}

func (h *Handler) AdminListPage(w http.ResponseWriter, r *http.Request) {
	list, err := h.reg.List(r.Context())
	if err != nil {
		h.fail(w, "list", err)
		return
	}
	h.tmpl.Page(w, r, http.StatusOK, "admin/databases", "Databases", databasesView{Databases: list, Form: dbForm{Port: 5432, SSLMode: "prefer"}})
}

func (h *Handler) AdminCreate(w http.ResponseWriter, r *http.Request) {
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

func (h *Handler) AdminTestNew(w http.ResponseWriter, r *http.Request) {
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

func (h *Handler) renderConnResult(w http.ResponseWriter, op string, err error) {
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

func (h *Handler) AdminEditPage(w http.ResponseWriter, r *http.Request) {
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

func (h *Handler) AdminUpdate(w http.ResponseWriter, r *http.Request) {
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

func (h *Handler) AdminTestSaved(w http.ResponseWriter, r *http.Request) {
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
			err = &ConnError{Reason: "server tidak dapat dihubungi"}
		}
	}
	if errors.Is(err, ErrDatabaseNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	h.renderConnResult(w, "test saved", err)
}

func (h *Handler) AdminDelete(w http.ResponseWriter, r *http.Request) {
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

func (h *Handler) SelectPage(w http.ResponseWriter, r *http.Request) {
	user, ok := access.UserFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthenticated", http.StatusUnauthorized)
		return
	}
	out, err := h.accessibleDatabases(r.Context(), user.ID)
	if err != nil {
		h.fail(w, "list", err)
		return
	}
	h.tmpl.Page(w, r, http.StatusOK, "db/select", "Select database", selectView{Databases: out})
}

func (h *Handler) accessibleDatabases(ctx context.Context, userID int64) ([]accessibleDB, error) {
	all, err := h.reg.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]accessibleDB, 0)
	for _, d := range all {
		level, err := h.checker.EffectiveLevel(ctx, userID, d.ID)
		if err != nil {
			return nil, err
		}
		if level > access.LevelNone {
			out = append(out, accessibleDB{ID: d.ID, Name: d.Name, Level: level})
		}
	}
	return out, nil
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

func groupTables(dbID int64, tables []Table, activeSchema, activeTable string) []schemaGroup {
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

func (h *Handler) viewerContext(w http.ResponseWriter, r *http.Request) (Database, access.Level, bool) {
	id, ok := pathID(r, access.DatabaseIDParam)
	if !ok {
		http.NotFound(w, r)
		return Database{}, access.LevelNone, false
	}
	user, _ := access.UserFromContext(r.Context())
	level, err := h.checker.EffectiveLevel(r.Context(), user.ID, id)
	if err != nil {
		h.fail(w, "effective level", err)
		return Database{}, access.LevelNone, false
	}
	if level == access.LevelNone {
		http.NotFound(w, r)
		return Database{}, access.LevelNone, false
	}
	d, err := h.reg.Get(r.Context(), id)
	if err != nil {
		h.fail(w, "get", err)
		return Database{}, access.LevelNone, false
	}
	return d, level, true
}

func (h *Handler) viewerFailure(w http.ResponseWriter, r *http.Request, d Database, op string, err error) {
	back := "/databases/" + strconv.FormatInt(d.ID, 10)
	switch {
	case errors.Is(err, ErrDatabaseNotFound), errors.Is(err, ErrTableNotFound):
		http.NotFound(w, r)
	case errors.Is(err, ErrConnectionFailed):
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

func (h *Handler) TablesPage(w http.ResponseWriter, r *http.Request) {
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

func (h *Handler) TablePage(w http.ResponseWriter, r *http.Request) {
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

type notice struct {
	Message string
	Error   string
}

func pathID(r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	return id, err == nil && id > 0
}

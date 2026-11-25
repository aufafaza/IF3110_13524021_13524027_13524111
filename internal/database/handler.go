package database

import (
	"context"
	"errors"
	"log"
	"net/http"
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

func (h *Handler) fail(w http.ResponseWriter, op string, err error) {
	if errors.Is(err, ErrDatabaseNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	log.Printf("databases: %s: %v", op, err)
	http.Error(w, "internal error", http.StatusInternalServerError)
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

type notice struct {
	Message string
	Error   string
}

func pathID(r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	return id, err == nil && id > 0
}

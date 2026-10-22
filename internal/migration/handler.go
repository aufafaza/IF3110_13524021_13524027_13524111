package migration

import (
	"bytes"
	"encoding/json"
	"errors"
	"html/template"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"time"

	migrationtemplates "github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/web/templates/migration"
)

type Handler struct {
	service     *Service
	extractUser UserExtractor
	extractCSRF func(*http.Request) string
	templates   *template.Template
	mux         *http.ServeMux
}

type HandlerOption func(*Handler)

func WithUserExtractor(fn UserExtractor) HandlerOption {
	return func(h *Handler) {
		if fn != nil {
			h.extractUser = fn
		}
	}
}

func WithCSRFTokenExtractor(fn func(*http.Request) string) HandlerOption {
	return func(h *Handler) {
		if fn != nil {
			h.extractCSRF = fn
		}
	}
}

func WithTemplateFS(fsys fs.FS) HandlerOption {
	return func(h *Handler) {
		if fsys != nil {
			h.templates = mustParseTemplates(fsys)
		}
	}
}

func NewHandler(service *Service, opts ...HandlerOption) *Handler {
	h := &Handler{
		service:     service,
		extractUser: UserFromContext,
		extractCSRF: func(*http.Request) string { return "" },
		templates:   mustParseTemplates(migrationtemplates.FS),
		mux:         http.NewServeMux(),
	}
	for _, opt := range opts {
		opt(h)
	}
	h.RegisterRoutes(h.mux)
	return h
}

func mustParseTemplates(fsys fs.FS) *template.Template {
	funcs := template.FuncMap{
		"derefInt": func(p *int) int {
			if p == nil {
				return 0
			}
			return *p
		},
		"derefInt64": func(p *int64) int64 {
			if p == nil {
				return 0
			}
			return *p
		},
		"derefStr": func(p *string) string {
			if p == nil {
				return ""
			}
			return *p
		},
		"formatTime": func(t time.Time) string {
			if t.IsZero() {
				return "-"
			}
			return t.UTC().Format("2006-01-02 15:04:05 UTC")
		},
		"formatTimePtr": func(t *time.Time) string {
			if t == nil || t.IsZero() {
				return "-"
			}
			return t.UTC().Format("2006-01-02 15:04:05 UTC")
		},
	}
	return template.Must(template.New("").Funcs(funcs).ParseFS(fsys, "*.html"))
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	viewGuard := RequireDBAccess(h.service.checker, LevelView, h.extractUser)
	editGuard := RequireDBAccess(h.service.checker, LevelEdit, h.extractUser)

	mux.Handle("GET /databases/{dbID}/migrations", viewGuard(http.HandlerFunc(h.handleListHistory)))
	mux.Handle("GET /databases/{dbID}/migrations/new", editGuard(http.HandlerFunc(h.handleNewEditor)))
	mux.Handle("POST /databases/{dbID}/migrations", editGuard(http.HandlerFunc(h.handleCreateOrPreview)))
	mux.Handle("GET /databases/{dbID}/migrations/{migrationID}", viewGuard(http.HandlerFunc(h.handleDetail)))
	mux.Handle("GET /databases/{dbID}/migrations/{migrationID}/edit", editGuard(http.HandlerFunc(h.handleEditEditor)))
	mux.Handle("POST /databases/{dbID}/migrations/{migrationID}", editGuard(http.HandlerFunc(h.handleUpdateOrPreview)))
	mux.Handle("POST /databases/{dbID}/migrations/{migrationID}/execute", editGuard(http.HandlerFunc(h.handleExecute)))
}

type historyViewData struct {
	DatabaseID int64
	CanEdit    bool
	Executed   []Migration
	Drafts     []Migration
}

type editorViewData struct {
	DatabaseID int64
	IsEdit     bool
	Migration  Migration
	Title      string
	SQL        string
	Preview    *PreviewResult
	Error      string
	CSRFToken  string
}

type detailViewData struct {
	DatabaseID int64
	CanEdit    bool
	Migration  Migration
	Statements []string
	Error      string
	CSRFToken  string
}

func (h *Handler) handleListHistory(w http.ResponseWriter, r *http.Request) {
	user, dbID, ok := h.requireUserAndDB(w, r)
	if !ok {
		return
	}

	level, err := h.service.UserLevel(r.Context(), user.ID, dbID)
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	items, err := h.service.ListHistory(r.Context(), user.ID, dbID)
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	if wantsJSON(r) {
		writeJSON(w, http.StatusOK, map[string]any{
			"database_id": dbID,
			"can_edit":    level >= LevelEdit,
			"migrations":  items,
		})
		return
	}

	var executed, drafts []Migration
	for _, m := range items {
		if m.Status == StatusDraft {
			drafts = append(drafts, m)
		} else {
			executed = append(executed, m)
		}
	}

	h.renderTemplate(w, http.StatusOK, "history.html", historyViewData{
		DatabaseID: dbID,
		CanEdit:    level >= LevelEdit,
		Executed:   executed,
		Drafts:     drafts,
	})
}

func (h *Handler) handleNewEditor(w http.ResponseWriter, r *http.Request) {
	user, dbID, ok := h.requireUserAndDB(w, r)
	if !ok {
		return
	}
	if _, err := h.service.requireLevel(r.Context(), user.ID, dbID, LevelEdit); err != nil {
		h.writeError(w, r, err)
		return
	}

	h.renderTemplate(w, http.StatusOK, "editor.html", editorViewData{
		DatabaseID: dbID,
		IsEdit:     false,
		CSRFToken:  h.extractCSRF(r),
	})
}

func (h *Handler) handleEditEditor(w http.ResponseWriter, r *http.Request) {
	user, dbID, migID, ok := h.requireUserDBAndMigration(w, r)
	if !ok {
		return
	}
	if _, err := h.service.requireLevel(r.Context(), user.ID, dbID, LevelEdit); err != nil {
		h.writeError(w, r, err)
		return
	}

	m, err := h.service.GetDetail(r.Context(), user.ID, dbID, migID)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	if m.Status != StatusDraft {
		h.writeError(w, r, ErrNotDraft)
		return
	}

	h.renderTemplate(w, http.StatusOK, "editor.html", editorViewData{
		DatabaseID: dbID,
		IsEdit:     true,
		Migration:  m,
		Title:      m.Title,
		SQL:        m.SQL,
		CSRFToken:  h.extractCSRF(r),
	})
}

func (h *Handler) handleCreateOrPreview(w http.ResponseWriter, r *http.Request) {
	user, dbID, ok := h.requireUserAndDB(w, r)
	if !ok {
		return
	}

	title, sqlStr, action, err := parseMigrationInput(r)
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	if action == "preview" {
		preview, err := h.service.PreviewSQL(r.Context(), user.ID, dbID, title, sqlStr)
		if err != nil {
			if wantsJSON(r) {
				h.writeError(w, r, err)
				return
			}
			h.renderTemplate(w, statusForError(err), "editor.html", editorViewData{
				DatabaseID: dbID,
				IsEdit:     false,
				Title:      title,
				SQL:        sqlStr,
				Error:      err.Error(),
				CSRFToken:  h.extractCSRF(r),
			})
			return
		}
		if wantsJSON(r) {
			writeJSON(w, http.StatusOK, preview)
			return
		}
		h.renderTemplate(w, http.StatusOK, "editor.html", editorViewData{
			DatabaseID: dbID,
			IsEdit:     false,
			Title:      preview.Title,
			SQL:        preview.SQL,
			Preview:    &preview,
			CSRFToken:  h.extractCSRF(r),
		})
		return
	}

	created, err := h.service.CreateDraft(r.Context(), user.ID, dbID, title, sqlStr)
	if err != nil {
		if wantsJSON(r) {
			h.writeError(w, r, err)
			return
		}
		h.renderTemplate(w, statusForError(err), "editor.html", editorViewData{
			DatabaseID: dbID,
			IsEdit:     false,
			Title:      title,
			SQL:        sqlStr,
			Error:      err.Error(),
			CSRFToken:  h.extractCSRF(r),
		})
		return
	}

	if wantsJSON(r) {
		writeJSON(w, http.StatusCreated, created)
		return
	}
	http.Redirect(w, r, "/databases/"+strconv.FormatInt(dbID, 10)+"/migrations/"+strconv.FormatInt(created.ID, 10), http.StatusSeeOther)
}

func (h *Handler) handleUpdateOrPreview(w http.ResponseWriter, r *http.Request) {
	user, dbID, migID, ok := h.requireUserDBAndMigration(w, r)
	if !ok {
		return
	}

	title, sqlStr, action, err := parseMigrationInput(r)
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	if action == "preview" {
		preview, err := h.service.PreviewSQL(r.Context(), user.ID, dbID, title, sqlStr)
		if err != nil {
			if wantsJSON(r) {
				h.writeError(w, r, err)
				return
			}
			h.renderTemplate(w, statusForError(err), "editor.html", editorViewData{
				DatabaseID: dbID,
				IsEdit:     true,
				Migration:  Migration{ID: migID, DatabaseID: dbID, Status: StatusDraft},
				Title:      title,
				SQL:        sqlStr,
				Error:      err.Error(),
				CSRFToken:  h.extractCSRF(r),
			})
			return
		}
		if wantsJSON(r) {
			writeJSON(w, http.StatusOK, preview)
			return
		}
		h.renderTemplate(w, http.StatusOK, "editor.html", editorViewData{
			DatabaseID: dbID,
			IsEdit:     true,
			Migration:  Migration{ID: migID, DatabaseID: dbID, Status: StatusDraft},
			Title:      preview.Title,
			SQL:        preview.SQL,
			Preview:    &preview,
			CSRFToken:  h.extractCSRF(r),
		})
		return
	}

	updated, err := h.service.UpdateDraft(r.Context(), user.ID, dbID, migID, title, sqlStr)
	if err != nil {
		if wantsJSON(r) {
			h.writeError(w, r, err)
			return
		}
		if errors.Is(err, ErrNotFound) || errors.Is(err, ErrForbidden) || errors.Is(err, ErrAlreadyApplied) || errors.Is(err, ErrNotDraft) {
			h.writeError(w, r, err)
			return
		}
		h.renderTemplate(w, statusForError(err), "editor.html", editorViewData{
			DatabaseID: dbID,
			IsEdit:     true,
			Migration:  Migration{ID: migID, DatabaseID: dbID, Status: StatusDraft},
			Title:      title,
			SQL:        sqlStr,
			Error:      err.Error(),
			CSRFToken:  h.extractCSRF(r),
		})
		return
	}

	if wantsJSON(r) {
		writeJSON(w, http.StatusOK, updated)
		return
	}
	http.Redirect(w, r, "/databases/"+strconv.FormatInt(dbID, 10)+"/migrations/"+strconv.FormatInt(updated.ID, 10), http.StatusSeeOther)
}

func (h *Handler) handleDetail(w http.ResponseWriter, r *http.Request) {
	user, dbID, migID, ok := h.requireUserDBAndMigration(w, r)
	if !ok {
		return
	}

	level, err := h.service.UserLevel(r.Context(), user.ID, dbID)
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	m, err := h.service.GetDetail(r.Context(), user.ID, dbID, migID)
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	if wantsJSON(r) {
		writeJSON(w, http.StatusOK, m)
		return
	}

	h.renderTemplate(w, http.StatusOK, "detail.html", detailViewData{
		DatabaseID: dbID,
		CanEdit:    level >= LevelEdit,
		Migration:  m,
		Statements: SplitStatements(m.SQL),
		CSRFToken:  h.extractCSRF(r),
	})
}

func (h *Handler) handleExecute(w http.ResponseWriter, r *http.Request) {
	user, dbID, migID, ok := h.requireUserDBAndMigration(w, r)
	if !ok {
		return
	}

	confirmed, err := parseConfirmInput(r)
	if err != nil || !confirmed {
		h.writeError(w, r, ErrConfirmationMissing)
		return
	}

	executed, execErr := h.service.Execute(r.Context(), user.ID, dbID, migID)
	if execErr != nil {
		if executed.ID != 0 && executed.Status == StatusFailed {
			if wantsJSON(r) {
				writeJSON(w, http.StatusUnprocessableEntity, executed)
				return
			}
			h.renderTemplate(w, http.StatusUnprocessableEntity, "detail.html", detailViewData{
				DatabaseID: dbID,
				CanEdit:    true,
				Migration:  executed,
				Statements: SplitStatements(executed.SQL),
				CSRFToken:  h.extractCSRF(r),
			})
			return
		}
		h.writeError(w, r, execErr)
		return
	}

	if wantsJSON(r) {
		writeJSON(w, http.StatusOK, executed)
		return
	}
	http.Redirect(w, r, "/databases/"+strconv.FormatInt(dbID, 10)+"/migrations/"+strconv.FormatInt(executed.ID, 10), http.StatusSeeOther)
}

func (h *Handler) requireUserAndDB(w http.ResponseWriter, r *http.Request) (User, int64, bool) {
	user, ok := h.extractUser(r.Context())
	if !ok || user.ID <= 0 {
		h.writeError(w, r, ErrUnauthenticated)
		return User{}, 0, false
	}
	dbID, err := strconv.ParseInt(r.PathValue("dbID"), 10, 64)
	if err != nil || dbID <= 0 {
		http.Error(w, "invalid database id", http.StatusBadRequest)
		return User{}, 0, false
	}
	return user, dbID, true
}

func (h *Handler) requireUserDBAndMigration(w http.ResponseWriter, r *http.Request) (User, int64, int64, bool) {
	user, dbID, ok := h.requireUserAndDB(w, r)
	if !ok {
		return User{}, 0, 0, false
	}
	migID, err := strconv.ParseInt(r.PathValue("migrationID"), 10, 64)
	if err != nil || migID <= 0 {
		http.Error(w, "invalid migration id", http.StatusBadRequest)
		return User{}, 0, 0, false
	}
	return user, dbID, migID, true
}

func parseMigrationInput(r *http.Request) (title, sqlStr, action string, err error) {
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		var body struct {
			Title  string `json:"title"`
			SQL    string `json:"sql"`
			Action string `json:"action"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			return "", "", "", err
		}
		return body.Title, body.SQL, body.Action, nil
	}
	if err := r.ParseForm(); err != nil {
		return "", "", "", err
	}
	return r.FormValue("title"), r.FormValue("sql"), r.FormValue("action"), nil
}

func parseConfirmInput(r *http.Request) (bool, error) {
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		var body struct {
			Confirm bool `json:"confirm"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			return false, err
		}
		return body.Confirm, nil
	}
	if err := r.ParseForm(); err != nil {
		return false, err
	}
	v := strings.ToLower(strings.TrimSpace(r.FormValue("confirm")))
	return v == "true" || v == "1" || v == "on" || v == "yes", nil
}

func wantsJSON(r *http.Request) bool {
	accept := r.Header.Get("Accept")
	ct := r.Header.Get("Content-Type")
	return strings.Contains(accept, "application/json") || strings.HasPrefix(ct, "application/json")
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func (h *Handler) renderTemplate(w http.ResponseWriter, status int, name string, data any) {
	var buf bytes.Buffer
	if err := h.templates.ExecuteTemplate(&buf, name, data); err != nil {
		http.Error(w, "template render error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = buf.WriteTo(w)
}

func (h *Handler) writeError(w http.ResponseWriter, r *http.Request, err error) {
	code := statusForError(err)
	if wantsJSON(r) {
		writeJSON(w, code, map[string]string{"error": err.Error()})
		return
	}
	http.Error(w, err.Error(), code)
}

func statusForError(err error) int {
	switch {
	case errors.Is(err, ErrUnauthenticated):
		return http.StatusUnauthorized
	case errors.Is(err, ErrForbidden):
		return http.StatusForbidden
	case errors.Is(err, ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, ErrAlreadyApplied), errors.Is(err, ErrNotDraft), errors.Is(err, ErrVersionConflict):
		return http.StatusConflict
	case errors.Is(err, ErrEmptyTitle), errors.Is(err, ErrEmptySQL), errors.Is(err, ErrNonTransactionalSQL), errors.Is(err, ErrConfirmationMissing):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

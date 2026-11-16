package migration

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"mime"
	"net/http"
	"strconv"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/access"
)

type Handler struct{ service *Service }

func NewHandler(service *Service) *Handler { return &Handler{service: service} }

var errInvalidRequest = errors.New("migration: invalid request")

func (h *Handler) handleListHistory(w http.ResponseWriter, r *http.Request) {
	user, dbID, ok := h.requireUserAndDB(w, r)
	if !ok {
		return
	}
	items, err := h.service.ListHistory(r.Context(), user.ID, dbID)
	if err != nil {
		writeError(w, err)
		return
	}
	level, err := h.service.UserLevel(r.Context(), user.ID, dbID)
	if err != nil {
		writeError(w, err)
		return
	}
	if items == nil {
		items = []Migration{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"database_id": dbID, "can_edit": level >= access.LevelEdit, "migrations": items})
}

type migrationInput struct {
	Title  string `json:"title"`
	SQL    string `json:"sql"`
	Action string `json:"action"`
}

func (h *Handler) handleCreateOrPreview(w http.ResponseWriter, r *http.Request) {
	user, dbID, ok := h.requireUserAndDB(w, r)
	if !ok {
		return
	}
	var in migrationInput
	if !readJSON(w, r, &in) {
		return
	}
	if h.preview(w, r, user.ID, dbID, in) {
		return
	}
	created, err := h.service.CreateDraft(r.Context(), user.ID, dbID, in.Title, in.SQL)
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Location", "/databases/"+strconv.FormatInt(dbID, 10)+"/migrations/"+strconv.FormatInt(created.ID, 10))
	writeJSON(w, http.StatusCreated, created)
}

func (h *Handler) handleUpdateOrPreview(w http.ResponseWriter, r *http.Request) {
	user, dbID, id, ok := h.requireUserDBAndMigration(w, r)
	if !ok {
		return
	}
	var in migrationInput
	if !readJSON(w, r, &in) {
		return
	}
	if h.preview(w, r, user.ID, dbID, in) {
		return
	}
	updated, err := h.service.UpdateDraft(r.Context(), user.ID, dbID, id, in.Title, in.SQL)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (h *Handler) preview(w http.ResponseWriter, r *http.Request, userID, dbID int64, in migrationInput) bool {
	var result PreviewResult
	var err error
	switch in.Action {
	case "", "save":
		return false
	case "preview":
		result, err = h.service.PreviewSQL(r.Context(), userID, dbID, in.Title, in.SQL)
	case "explain", "explain_analyze":
		result, err = h.service.AnalyzeSQL(r.Context(), userID, dbID, in.Title, in.SQL, in.Action == "explain_analyze")
	default:
		writeError(w, errInvalidRequest)
		return true
	}
	if err != nil {
		writeError(w, err)
	} else {
		writeJSON(w, http.StatusOK, result)
	}
	return true
}

func (h *Handler) handleDetail(w http.ResponseWriter, r *http.Request) {
	user, dbID, id, ok := h.requireUserDBAndMigration(w, r)
	if !ok {
		return
	}
	m, err := h.service.GetDetail(r.Context(), user.ID, dbID, id)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, m)
}

func (h *Handler) handleExplain(w http.ResponseWriter, r *http.Request) {
	user, dbID, id, ok := h.requireUserDBAndMigration(w, r)
	if !ok {
		return
	}
	var in struct {
		Analyze bool `json:"analyze"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	report, err := h.service.ExplainMigration(r.Context(), user.ID, dbID, id, in.Analyze)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func (h *Handler) handleExecute(w http.ResponseWriter, r *http.Request) {
	user, dbID, id, ok := h.requireUserDBAndMigration(w, r)
	if !ok {
		return
	}
	var in struct {
		Confirm bool `json:"confirm"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if !in.Confirm {
		writeError(w, ErrConfirmationMissing)
		return
	}
	executed, err := h.service.Execute(r.Context(), user.ID, dbID, id)
	if err != nil {
		if executed.ID != 0 && executed.Status == StatusFailed {
			writeJSON(w, http.StatusUnprocessableEntity, executed)
		} else {
			writeError(w, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, executed)
}

func (h *Handler) requireUserAndDB(w http.ResponseWriter, r *http.Request) (access.User, int64, bool) {
	user, ok := access.UserFromContext(r.Context())
	if !ok || user.ID <= 0 {
		writeError(w, ErrUnauthenticated)
		return access.User{}, 0, false
	}
	id, err := strconv.ParseInt(r.PathValue(access.DatabaseIDParam), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, errInvalidRequest)
		return access.User{}, 0, false
	}
	return user, id, true
}

func (h *Handler) requireUserDBAndMigration(w http.ResponseWriter, r *http.Request) (access.User, int64, int64, bool) {
	user, dbID, ok := h.requireUserAndDB(w, r)
	if !ok {
		return access.User{}, 0, 0, false
	}
	id, err := strconv.ParseInt(r.PathValue("migrationID"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, errInvalidRequest)
		return access.User{}, 0, 0, false
	}
	return user, dbID, id, true
}

func readJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{"error": "Content-Type must be application/json"})
		return false
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		writeError(w, errInvalidRequest)
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		writeError(w, errInvalidRequest)
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, err error) {
	status := statusForError(err)
	message := err.Error()
	if status == http.StatusInternalServerError {
		log.Printf("migration: %v", err)
		message = "internal error"
	}
	writeJSON(w, status, map[string]string{"error": message})
}

func statusForError(err error) int {
	switch {
	case errors.Is(err, errInvalidRequest):
		return http.StatusBadRequest
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

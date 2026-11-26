package migration

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"mime"
	"net/http"
	"strconv"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/access"
)

type ReviewDispatcher interface {
	DispatchReview(context.Context, Review) (QueueJob, error)
}

type Handler struct {
	service *Service
	queue   ReviewDispatcher
}

func NewHandler(service *Service) *Handler { return &Handler{service: service} }

func NewQueuedHandler(service *Service, queue ReviewDispatcher) *Handler {
	return &Handler{service: service, queue: queue}
}

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
	Title              string  `json:"title"`
	SQL                string  `json:"sql"`
	Action             string  `json:"action"`
	DownSQL            *string `json:"down_sql"`
	IrreversibleReason *string `json:"irreversible_reason"`
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
	var created Migration
	var err error
	if in.DownSQL != nil || in.IrreversibleReason != nil {
		created, err = h.service.CreateDraftWithDefinition(r.Context(), user.ID, dbID, in.Title, in.SQL, in.DownSQL, in.IrreversibleReason)
	} else {
		created, err = h.service.CreateDraft(r.Context(), user.ID, dbID, in.Title, in.SQL)
	}
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
	var updated Migration
	var err error
	if in.DownSQL != nil || in.IrreversibleReason != nil {
		updated, err = h.service.UpdateDraftWithDefinition(r.Context(), user.ID, dbID, id, in.Title, in.SQL, in.DownSQL, in.IrreversibleReason)
	} else {
		updated, err = h.service.UpdateDraft(r.Context(), user.ID, dbID, id, in.Title, in.SQL)
	}
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
	level, err := h.service.UserLevel(r.Context(), user.ID, dbID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Migration
		CanEdit bool `json:"can_edit"`
	}{m, level >= access.LevelEdit})
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
		Confirm   bool   `json:"confirm"`
		Operation string `json:"operation"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if !in.Confirm {
		writeError(w, ErrConfirmationMissing)
		return
	}
	if h.queue != nil {
		op, err := normalizeOperation(in.Operation)
		if err != nil || op != string(OpApply) {
			writeError(w, ErrInvalidOperation)
			return
		}
		m, review, err := h.service.ApprovedApply(r.Context(), user.ID, dbID, id)
		if err != nil {
			writeError(w, err)
			return
		}
		job, err := h.queue.DispatchReview(r.Context(), review)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusAccepted, struct {
			Migration
			JobID int64 `json:"job_id"`
		}{m, job.ID})
		return
	}
	executed, err := h.service.ExecuteOperation(r.Context(), user.ID, dbID, id, in.Operation)
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

func (h *Handler) handleCurrentApplyReview(w http.ResponseWriter, r *http.Request) {
	user, dbID, id, ok := h.requireUserDBAndMigration(w, r)
	if !ok {
		return
	}
	var revision int
	if raw := r.URL.Query().Get("revision"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value <= 0 {
			writeError(w, errInvalidRequest)
			return
		}
		revision = value
	}
	review, err := h.service.CurrentApplyReview(r.Context(), user.ID, dbID, id, revision)
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, struct {
		Review *Review `json:"review"`
	}{review})
}

func (h *Handler) handleSubmitReview(w http.ResponseWriter, r *http.Request) {
	user, dbID, id, ok := h.requireUserDBAndMigration(w, r)
	if !ok {
		return
	}
	var in struct {
		Operation string `json:"operation"`
		Revision  int    `json:"revision"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if in.Revision < 0 {
		writeError(w, errInvalidRequest)
		return
	}
	review, err := h.service.SubmitReview(r.Context(), user.ID, dbID, id, in.Operation, in.Revision)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, review)
}

func (h *Handler) handleListReviews(w http.ResponseWriter, r *http.Request) {
	user, dbID, ok := h.requireUserAndDB(w, r)
	if !ok {
		return
	}
	reviews, err := h.service.ListReviews(r.Context(), user.ID, dbID)
	if err != nil {
		writeError(w, err)
		return
	}
	if reviews == nil {
		reviews = []Review{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"database_id": dbID, "reviews": reviews})
}

func (h *Handler) handleReviewDecision(w http.ResponseWriter, r *http.Request) {
	user, ok := access.UserFromContext(r.Context())
	if !ok || user.ID <= 0 {
		writeError(w, ErrUnauthenticated)
		return
	}
	requestID := r.PathValue("requestID")
	if requestID == "" {
		writeError(w, errInvalidRequest)
		return
	}
	var in struct {
		GroupID  int64  `json:"group_id"`
		Decision string `json:"decision"`
		Comment  string `json:"comment"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if in.GroupID < 0 {
		writeError(w, errInvalidRequest)
		return
	}
	review, err := h.service.DecideReview(r.Context(), user.ID, requestID, in.GroupID, in.Decision, in.Comment)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, review)
}

func (h *Handler) handleEnqueueReview(w http.ResponseWriter, r *http.Request) {
	user, ok := access.UserFromContext(r.Context())
	if !ok || user.ID <= 0 {
		writeError(w, ErrUnauthenticated)
		return
	}
	if h.queue == nil {
		writeError(w, ErrApprovalsUnavailable)
		return
	}
	review, err := h.service.ReviewForDispatch(r.Context(), user.ID, r.PathValue("requestID"))
	if err != nil {
		writeError(w, err)
		return
	}
	job, err := h.queue.DispatchReview(r.Context(), review)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, job)
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
	case errors.Is(err, ErrNotFound), errors.Is(err, ErrReviewNotFound):
		return http.StatusNotFound
	case errors.Is(err, ErrAlreadyApplied), errors.Is(err, ErrNotDraft), errors.Is(err, ErrVersionConflict), errors.Is(err, ErrNotLatestActive), errors.Is(err, ErrIrreversible), errors.Is(err, ErrImmutable), errors.Is(err, ErrNothingToForward), errors.Is(err, ErrRevisionMismatch):
		return http.StatusConflict
	case errors.Is(err, ErrReviewClosed), errors.Is(err, ErrRevisionChanged), errors.Is(err, ErrApprovalRequired), errors.Is(err, ErrQueueNotApproved):
		return http.StatusConflict
	case errors.Is(err, ErrNotGroupLeader), errors.Is(err, ErrLeaderMissing):
		return http.StatusForbidden
	case errors.Is(err, ErrInvalidOperation), errors.Is(err, ErrReviewGroupRequired), errors.Is(err, ErrInvalidDecision):
		return http.StatusBadRequest
	case errors.Is(err, ErrEmptyTitle), errors.Is(err, ErrEmptySQL), errors.Is(err, ErrNonTransactionalSQL), errors.Is(err, ErrConfirmationMissing), errors.Is(err, ErrInvalidDefinition), errors.Is(err, ErrInvalidAttempt):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

func (h *Handler) handlePlan(w http.ResponseWriter, r *http.Request) {
	user, dbID, id, ok := h.requireUserDBAndMigration(w, r)
	if !ok {
		return
	}
	var in struct {
		Operation Operation `json:"operation"`
		Revision  int       `json:"revision"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	plan, err := h.service.PlanOperation(r.Context(), user.ID, dbID, id, in.Operation, in.Revision)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, plan)
}

func (h *Handler) handleListAttemptHistory(w http.ResponseWriter, r *http.Request) {
	user, dbID, ok := h.requireUserAndDB(w, r)
	if !ok {
		return
	}
	hide, _ := strconv.ParseBool(r.URL.Query().Get("hide_rollback"))
	entries, err := h.service.ListAttemptHistory(r.Context(), user.ID, dbID, hide)
	if err != nil {
		writeError(w, err)
		return
	}
	if entries == nil {
		entries = []HistoryEntry{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"database_id": dbID, "hide_rollback": hide, "entries": entries,
	})
}

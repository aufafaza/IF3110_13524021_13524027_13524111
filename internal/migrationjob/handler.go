package migrationjob

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/access"
)

type Handler struct {
	store   *Store
	checker access.DatabaseChecker
}

func NewHandler(store *Store, checker access.DatabaseChecker) *Handler {
	return &Handler{store: store, checker: checker}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	view := access.RequireDBAccess(h.checker, access.LevelView)
	mux.Handle("GET /api/databases/{dbID}/jobs/{jobID}", view(http.HandlerFunc(h.status)))
}

func (h *Handler) status(w http.ResponseWriter, r *http.Request) {
	databaseID, _ := strconv.ParseInt(r.PathValue(access.DatabaseIDParam), 10, 64)
	jobID, err := strconv.ParseInt(r.PathValue("jobID"), 10, 64)
	if err != nil || jobID <= 0 {
		access.WriteAPIError(w, "invalid job id", http.StatusBadRequest)
		return
	}
	job, err := h.store.Get(r.Context(), databaseID, jobID)
	if errors.Is(err, ErrNotFound) {
		access.WriteAPIError(w, "migration job not found", http.StatusNotFound)
		return
	}
	if err != nil {
		access.WriteAPIError(w, "failed to read migration job", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(job)
}

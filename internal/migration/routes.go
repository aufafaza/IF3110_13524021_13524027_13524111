package migration

import (
	"net/http"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/access"
)

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	viewGuard := access.RequireDBAccess(h.service.checker, access.LevelView)
	editGuard := access.RequireDBAccess(h.service.checker, access.LevelEdit)

	mux.Handle("GET /databases/{dbID}/migrations", viewGuard(http.HandlerFunc(h.handleListHistory)))
	mux.Handle("POST /databases/{dbID}/migrations", editGuard(http.HandlerFunc(h.handleCreateOrPreview)))
	mux.Handle("GET /databases/{dbID}/migrations/{migrationID}", viewGuard(http.HandlerFunc(h.handleDetail)))
	mux.Handle("POST /databases/{dbID}/migrations/{migrationID}", editGuard(http.HandlerFunc(h.handleUpdateOrPreview)))
	mux.Handle("POST /databases/{dbID}/migrations/{migrationID}/explain", editGuard(http.HandlerFunc(h.handleExplain)))
	mux.Handle("POST /databases/{dbID}/migrations/{migrationID}/execute", editGuard(http.HandlerFunc(h.handleExecute)))
}

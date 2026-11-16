package migration

import "net/http"

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	viewGuard := RequireDBAccess(h.service.checker, LevelView, h.extractUser)
	editGuard := RequireDBAccess(h.service.checker, LevelEdit, h.extractUser)

	mux.Handle("GET /databases/{dbID}/migrations", viewGuard(http.HandlerFunc(h.handleListHistory)))
	mux.Handle("GET /databases/{dbID}/migrations/new", editGuard(http.HandlerFunc(h.handleNewEditor)))
	mux.Handle("POST /databases/{dbID}/migrations", editGuard(http.HandlerFunc(h.handleCreateOrPreview)))
	mux.Handle("GET /databases/{dbID}/migrations/{migrationID}", viewGuard(http.HandlerFunc(h.handleDetail)))
	mux.Handle("GET /databases/{dbID}/migrations/{migrationID}/edit", editGuard(http.HandlerFunc(h.handleEditEditor)))
	mux.Handle("POST /databases/{dbID}/migrations/{migrationID}", editGuard(http.HandlerFunc(h.handleUpdateOrPreview)))
	mux.Handle("POST /databases/{dbID}/migrations/{migrationID}/explain", editGuard(http.HandlerFunc(h.handleExplain)))
	mux.Handle("POST /databases/{dbID}/migrations/{migrationID}/execute", editGuard(http.HandlerFunc(h.handleExecute)))
}

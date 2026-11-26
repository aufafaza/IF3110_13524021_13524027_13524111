package database

import (
	"net/http"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/access"
)

func (h *Handler) RegisterRoutes(mux *http.ServeMux, guard *access.Guard) {
	h.RegisterAPIRoutes(mux, guard)
	admin := guard.RequireAdmin

	mux.Handle("GET /admin/databases", admin(http.HandlerFunc(h.AdminListPage)))
	mux.Handle("POST /admin/databases", admin(http.HandlerFunc(h.AdminCreate)))
	mux.Handle("POST /admin/databases/test", admin(http.HandlerFunc(h.AdminTestNew)))
	mux.Handle("GET /admin/databases/{id}", admin(http.HandlerFunc(h.AdminEditPage)))
	mux.Handle("POST /admin/databases/{id}", admin(http.HandlerFunc(h.AdminUpdate)))
	mux.Handle("POST /admin/databases/{id}/test", admin(http.HandlerFunc(h.AdminTestSaved)))
	mux.Handle("POST /admin/databases/{id}/delete", admin(http.HandlerFunc(h.AdminDelete)))

	mux.HandleFunc("GET /api/databases", h.listDatabasesAPI)
	apiView := access.RequireDBAccess(h.checker, access.LevelView)
	mux.Handle("GET /api/databases/{dbID}/tables", apiView(http.HandlerFunc(h.listTablesAPI)))
	mux.Handle("GET /api/databases/{dbID}/table", apiView(http.HandlerFunc(h.tableInfoAPI)))
	mux.Handle("GET /api/databases/{dbID}/table/rows", apiView(http.HandlerFunc(h.tableRowsAPI)))
}

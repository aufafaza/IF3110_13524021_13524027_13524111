package web

import (
	"net/http"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/access"
)

func registerDatabaseRoutes(mux *http.ServeMux, d Deps) {
	h := d.Databases
	if h == nil {
		return
	}
	admin := d.Guard.RequireAdmin
	view := d.Guard.RequireDBAccess(access.LevelView)

	mux.Handle("GET /admin/databases", admin(http.HandlerFunc(h.AdminListPage)))
	mux.Handle("POST /admin/databases", admin(http.HandlerFunc(h.AdminCreate)))
	mux.Handle("POST /admin/databases/test", admin(http.HandlerFunc(h.AdminTestNew)))
	mux.Handle("GET /admin/databases/{id}", admin(http.HandlerFunc(h.AdminEditPage)))
	mux.Handle("POST /admin/databases/{id}", admin(http.HandlerFunc(h.AdminUpdate)))
	mux.Handle("POST /admin/databases/{id}/test", admin(http.HandlerFunc(h.AdminTestSaved)))
	mux.Handle("POST /admin/databases/{id}/delete", admin(http.HandlerFunc(h.AdminDelete)))

	mux.Handle("GET /databases", d.Guard.RequireLogin(http.HandlerFunc(h.SelectPage)))
	mux.Handle("GET /databases/{dbID}", view(http.HandlerFunc(h.TablesPage)))
	mux.Handle("GET /databases/{dbID}/table", view(http.HandlerFunc(h.TablePage)))
}
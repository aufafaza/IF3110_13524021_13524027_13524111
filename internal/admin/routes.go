package admin

import (
	"net/http"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/access"
)

func (h *Handler) RegisterRoutes(mux *http.ServeMux, guard *access.Guard) {
	h.RegisterAPIRoutes(mux, guard)
	adminRoute := func(pattern string, h http.HandlerFunc) {
		mux.Handle(pattern, guard.RequireAdmin(h))
	}
	adminRoute("GET /admin/users", h.UsersPage)
	adminRoute("POST /admin/users", h.CreateUser)
	adminRoute("POST /admin/users/{id}/active", h.SetUserActive)
	adminRoute("POST /admin/users/{id}/password", h.ResetPassword)

	adminRoute("GET /admin/groups", h.GroupsPage)
	adminRoute("POST /admin/groups", h.CreateGroup)
	adminRoute("GET /admin/groups/{id}", h.GroupPage)
	adminRoute("POST /admin/groups/{id}/delete", h.DeleteGroup)
	adminRoute("POST /admin/groups/{id}/members", h.AddMember)
	adminRoute("POST /admin/groups/{id}/members/{userID}/remove", h.RemoveMember)
	adminRoute("POST /admin/groups/{id}/grants", h.SetGrant)
	adminRoute("POST /admin/groups/{id}/grants/{dbID}/remove", h.RemoveGrant)
}

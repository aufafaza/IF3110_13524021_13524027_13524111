package web

import "net/http"

func registerAdminRoutes(mux *http.ServeMux, d Deps) {
	h := d.Admin
	if h == nil {
		return
	}
	admin := d.Guard.RequireAdmin
	route := func(pattern string, fn http.HandlerFunc) {
		mux.Handle(pattern, admin(fn))
	}

	route("GET /admin/users", h.UsersPage)
	route("POST /admin/users", h.CreateUser)
	route("POST /admin/users/{id}/active", h.SetUserActive)
	route("POST /admin/users/{id}/password", h.ResetPassword)

	route("GET /admin/groups", h.GroupsPage)
	route("POST /admin/groups", h.CreateGroup)
	route("GET /admin/groups/{id}", h.GroupPage)
	route("POST /admin/groups/{id}/delete", h.DeleteGroup)
	route("POST /admin/groups/{id}/members", h.AddMember)
	route("POST /admin/groups/{id}/members/{userID}/remove", h.RemoveMember)
	route("POST /admin/groups/{id}/grants", h.SetGrant)
	route("POST /admin/groups/{id}/grants/{dbID}/remove", h.RemoveGrant)
}
package handlers

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strconv"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/access"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/admin"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/web/templates"
)

type AdminService interface {
	ListUsers(ctx context.Context) ([]admin.User, error)
	CreateUser(ctx context.Context, username, password string, isPlatformAdmin bool) error
	SetUserActive(ctx context.Context, actorID, userID int64, active bool) error
	ResetPassword(ctx context.Context, userID int64, newPassword string) error

	ListGroups(ctx context.Context) ([]admin.Group, error)
	GetGroup(ctx context.Context, groupID int64) (admin.Group, error)
	CreateGroup(ctx context.Context, name string) error
	DeleteGroup(ctx context.Context, actorID, groupID int64) error

	ListMembers(ctx context.Context, groupID int64) ([]admin.User, error)
	ListNonMembers(ctx context.Context, groupID int64) ([]admin.User, error)
	AddMember(ctx context.Context, actorID, groupID, userID int64) error
	RemoveMember(ctx context.Context, actorID, groupID, userID int64) error

	ListDatabases(ctx context.Context) ([]admin.Database, error)
	ListGrants(ctx context.Context, groupID int64) ([]admin.Grant, error)
	SetGrant(ctx context.Context, actorID, groupID, databaseID int64, level access.Level) error
	RemoveGrant(ctx context.Context, actorID, groupID, databaseID int64) error
}

type AdminHandler struct {
	svc  AdminService
	tmpl *templates.Set
}

func NewAdminHandler(svc AdminService, tmpl *templates.Set) *AdminHandler {
	return &AdminHandler{svc: svc, tmpl: tmpl}
}

type notice struct {
	Message string
	Error   string
}

type usersView struct {
	notice
	Users         []admin.User
	CurrentUserID int64
}

type groupsView struct {
	notice
	Groups []admin.Group
}

type membersView struct {
	notice
	GroupID    int64
	Members    []admin.User
	Candidates []admin.User
}

type grantsView struct {
	notice
	GroupID   int64
	Grants    []admin.Grant
	Databases []admin.Database
}

type groupPage struct {
	Group   admin.Group
	Members membersView
	Grants  grantsView
}

func userMessage(err error) (string, bool) {
	var ve *admin.ValidationError
	switch {
	case errors.As(err, &ve):
		return ve.Msg, true
	case errors.Is(err, admin.ErrUsernameTaken):
		return "That username is already taken.", true
	case errors.Is(err, admin.ErrGroupNameTaken):
		return "A group with that name already exists.", true
	case errors.Is(err, admin.ErrSelfDeactivate):
		return "You cannot deactivate your own account.", true
	}
	return "", false
}

func (h *AdminHandler) fail(w http.ResponseWriter, op string, err error) {
	if errors.Is(err, admin.ErrNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	log.Printf("admin: %s: %v", op, err)
	http.Error(w, "internal error", http.StatusInternalServerError)
}

func (h *AdminHandler) outcome(w http.ResponseWriter, op string, err error, success string) (notice, bool) {
	if err == nil {
		return notice{Message: success}, true
	}
	if msg, ok := userMessage(err); ok {
		return notice{Error: msg}, true
	}
	h.fail(w, op, err)
	return notice{}, false
}

func pathID(r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	return id, err == nil && id > 0
}

func formID(r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(r.PostFormValue(name), 10, 64)
	return id, err == nil && id > 0
}

func actorID(r *http.Request) int64 {
	user, _ := access.UserFromContext(r.Context())
	return user.ID
}

func (h *AdminHandler) UsersPage(w http.ResponseWriter, r *http.Request) {
	view, ok := h.usersView(w, r, notice{})
	if !ok {
		return
	}
	h.tmpl.Page(w, r, http.StatusOK, "admin/users", "Users", view)
}

func (h *AdminHandler) usersView(w http.ResponseWriter, r *http.Request, n notice) (usersView, bool) {
	users, err := h.svc.ListUsers(r.Context())
	if err != nil {
		h.fail(w, "list users", err)
		return usersView{}, false
	}
	return usersView{notice: n, Users: users, CurrentUserID: actorID(r)}, true
}

func (h *AdminHandler) renderUsers(w http.ResponseWriter, r *http.Request, n notice) {
	if view, ok := h.usersView(w, r, n); ok {
		h.tmpl.Fragment(w, http.StatusOK, "admin/users", "users", view)
	}
}

func (h *AdminHandler) CreateUser(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	err := h.svc.CreateUser(r.Context(), r.PostFormValue("username"), r.PostFormValue("password"), r.PostFormValue("is_admin") != "")
	n, ok := h.outcome(w, "create user", err, "User created.")
	if !ok {
		return
	}
	h.renderUsers(w, r, n)
}

func (h *AdminHandler) SetUserActive(w http.ResponseWriter, r *http.Request) {
	userID, ok := pathID(r, "id")
	if !ok {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	active, err := strconv.ParseBool(r.PostFormValue("active"))
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	success := "User deactivated."
	if active {
		success = "User activated."
	}
	n, ok := h.outcome(w, "set user active", h.svc.SetUserActive(r.Context(), actorID(r), userID, active), success)
	if !ok {
		return
	}
	h.renderUsers(w, r, n)
}

func (h *AdminHandler) ResetPassword(w http.ResponseWriter, r *http.Request) {
	userID, ok := pathID(r, "id")
	if !ok {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	err := h.svc.ResetPassword(r.Context(), userID, r.PostFormValue("password"))
	n, ok := h.outcome(w, "reset password", err, "Password reset. The user's sessions were ended.")
	if !ok {
		return
	}
	h.renderUsers(w, r, n)
}

func (h *AdminHandler) renderGroups(w http.ResponseWriter, r *http.Request, n notice) {
	groups, err := h.svc.ListGroups(r.Context())
	if err != nil {
		h.fail(w, "list groups", err)
		return
	}
	h.tmpl.Fragment(w, http.StatusOK, "admin/groups", "groups", groupsView{notice: n, Groups: groups})
}

func (h *AdminHandler) GroupsPage(w http.ResponseWriter, r *http.Request) {
	groups, err := h.svc.ListGroups(r.Context())
	if err != nil {
		h.fail(w, "list groups", err)
		return
	}
	h.tmpl.Page(w, r, http.StatusOK, "admin/groups", "Groups", groupsView{Groups: groups})
}

func (h *AdminHandler) CreateGroup(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	n, ok := h.outcome(w, "create group", h.svc.CreateGroup(r.Context(), r.PostFormValue("name")), "Group created.")
	if !ok {
		return
	}
	h.renderGroups(w, r, n)
}

func (h *AdminHandler) DeleteGroup(w http.ResponseWriter, r *http.Request) {
	groupID, ok := pathID(r, "id")
	if !ok {
		http.NotFound(w, r)
		return
	}
	n, ok := h.outcome(w, "delete group", h.svc.DeleteGroup(r.Context(), actorID(r), groupID), "Group deleted.")
	if !ok {
		return
	}
	h.renderGroups(w, r, n)
}

func (h *AdminHandler) membersView(w http.ResponseWriter, r *http.Request, groupID int64, n notice) (membersView, bool) {
	members, err := h.svc.ListMembers(r.Context(), groupID)
	if err != nil {
		h.fail(w, "list members", err)
		return membersView{}, false
	}
	candidates, err := h.svc.ListNonMembers(r.Context(), groupID)
	if err != nil {
		h.fail(w, "list non-members", err)
		return membersView{}, false
	}
	return membersView{notice: n, GroupID: groupID, Members: members, Candidates: candidates}, true
}

func (h *AdminHandler) grantsView(w http.ResponseWriter, r *http.Request, groupID int64, n notice) (grantsView, bool) {
	grants, err := h.svc.ListGrants(r.Context(), groupID)
	if err != nil {
		h.fail(w, "list grants", err)
		return grantsView{}, false
	}
	databases, err := h.svc.ListDatabases(r.Context())
	if err != nil {
		h.fail(w, "list databases", err)
		return grantsView{}, false
	}
	return grantsView{notice: n, GroupID: groupID, Grants: grants, Databases: databases}, true
}

func (h *AdminHandler) renderMembers(w http.ResponseWriter, r *http.Request, groupID int64, n notice) {
	if view, ok := h.membersView(w, r, groupID, n); ok {
		h.tmpl.Fragment(w, http.StatusOK, "admin/group", "members", view)
	}
}

func (h *AdminHandler) renderGrants(w http.ResponseWriter, r *http.Request, groupID int64, n notice) {
	if view, ok := h.grantsView(w, r, groupID, n); ok {
		h.tmpl.Fragment(w, http.StatusOK, "admin/group", "grants", view)
	}
}

func (h *AdminHandler) GroupPage(w http.ResponseWriter, r *http.Request) {
	groupID, ok := pathID(r, "id")
	if !ok {
		http.NotFound(w, r)
		return
	}
	group, err := h.svc.GetGroup(r.Context(), groupID)
	if err != nil {
		h.fail(w, "get group", err)
		return
	}
	members, ok := h.membersView(w, r, groupID, notice{})
	if !ok {
		return
	}
	grants, ok := h.grantsView(w, r, groupID, notice{})
	if !ok {
		return
	}
	h.tmpl.Page(w, r, http.StatusOK, "admin/group", group.Name, groupPage{Group: group, Members: members, Grants: grants})
}

func (h *AdminHandler) AddMember(w http.ResponseWriter, r *http.Request) {
	groupID, ok := pathID(r, "id")
	if !ok {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	userID, ok := formID(r, "user_id")
	if !ok {
		h.renderMembers(w, r, groupID, notice{Error: "Choose a user."})
		return
	}
	n, ok := h.outcome(w, "add member", h.svc.AddMember(r.Context(), actorID(r), groupID, userID), "Member added. Their sessions were ended so the change applies on next sign-in.")
	if !ok {
		return
	}
	h.renderMembers(w, r, groupID, n)
}

func (h *AdminHandler) RemoveMember(w http.ResponseWriter, r *http.Request) {
	groupID, ok := pathID(r, "id")
	if !ok {
		http.NotFound(w, r)
		return
	}
	userID, ok := pathID(r, "userID")
	if !ok {
		http.NotFound(w, r)
		return
	}
	n, ok := h.outcome(w, "remove member", h.svc.RemoveMember(r.Context(), actorID(r), groupID, userID), "Member removed.")
	if !ok {
		return
	}
	h.renderMembers(w, r, groupID, n)
}

func (h *AdminHandler) SetGrant(w http.ResponseWriter, r *http.Request) {
	groupID, ok := pathID(r, "id")
	if !ok {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	databaseID, ok := formID(r, "database_id")
	if !ok {
		h.renderGrants(w, r, groupID, notice{Error: "Choose a database."})
		return
	}
	level, err := access.ParseLevel(r.PostFormValue("level"))
	if err != nil {
		h.renderGrants(w, r, groupID, notice{Error: "Choose an access level."})
		return
	}
	n, ok := h.outcome(w, "set grant", h.svc.SetGrant(r.Context(), actorID(r), groupID, databaseID, level), "Access saved.")
	if !ok {
		return
	}
	h.renderGrants(w, r, groupID, n)
}

func (h *AdminHandler) RemoveGrant(w http.ResponseWriter, r *http.Request) {
	groupID, ok := pathID(r, "id")
	if !ok {
		http.NotFound(w, r)
		return
	}
	databaseID, ok := pathID(r, "dbID")
	if !ok {
		http.NotFound(w, r)
		return
	}
	n, ok := h.outcome(w, "remove grant", h.svc.RemoveGrant(r.Context(), actorID(r), groupID, databaseID), "Access removed.")
	if !ok {
		return
	}
	h.renderGrants(w, r, groupID, n)
}

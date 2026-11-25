package admin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/access"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/web/templates"
)

type AdminService interface {
	ListUsers(ctx context.Context) ([]User, error)
	CreateUser(ctx context.Context, username, password string, isPlatformAdmin bool) error
	SetUserActive(ctx context.Context, actorID, userID int64, active bool) error
	ResetPassword(ctx context.Context, userID int64, newPassword string) error

	ListGroups(ctx context.Context) ([]Group, error)
	GetGroup(ctx context.Context, groupID int64) (Group, error)
	CreateGroup(ctx context.Context, name string) error
	DeleteGroup(ctx context.Context, actorID, groupID int64) error

	ListMembers(ctx context.Context, groupID int64) ([]User, error)
	ListNonMembers(ctx context.Context, groupID int64) ([]User, error)
	AddMember(ctx context.Context, actorID, groupID, userID int64) error
	RemoveMember(ctx context.Context, actorID, groupID, userID int64) error

	ListDatabases(ctx context.Context) ([]Database, error)
	ListGrants(ctx context.Context, groupID int64) ([]Grant, error)
	SetGrant(ctx context.Context, actorID, groupID, databaseID int64, level access.Level) error
	RemoveGrant(ctx context.Context, actorID, groupID, databaseID int64) error
}

type Handler struct {
	svc  AdminService
	tmpl *templates.Set
}

func NewHandler(svc AdminService, tmpl *templates.Set) *Handler {
	return &Handler{svc: svc, tmpl: tmpl}
}

type notice struct {
	Message string
	Error   string
}

type usersView struct {
	notice
	Users         []User
	CurrentUserID int64
}

type groupsView struct {
	notice
	Groups []Group
}

type membersView struct {
	notice
	GroupID    int64
	Members    []User
	Candidates []User
}

type grantsView struct {
	notice
	GroupID   int64
	Grants    []Grant
	Databases []Database
}

type groupPage struct {
	Group   Group
	Members membersView
	Grants  grantsView
}

func userMessage(err error) (string, bool) {
	var ve *ValidationError
	switch {
	case errors.As(err, &ve):
		return ve.Msg, true
	case errors.Is(err, ErrUsernameTaken):
		return "That username is already taken.", true
	case errors.Is(err, ErrGroupNameTaken):
		return "A group with that name already exists.", true
	case errors.Is(err, ErrSelfDeactivate):
		return "You cannot deactivate your own account.", true
	}
	return "", false
}

func (h *Handler) fail(w http.ResponseWriter, op string, err error) {
	if errors.Is(err, ErrNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	log.Printf("admin: %s: %v", op, err)
	http.Error(w, "internal error", http.StatusInternalServerError)
}

func (h *Handler) RegisterAPIRoutes(mux *http.ServeMux, guard *access.Guard) {
	api := guard.RequireAdmin
	mux.Handle("GET /api/users", api(http.HandlerFunc(h.apiUsers)))
	mux.Handle("POST /api/users", api(http.HandlerFunc(h.apiCreateUser)))
	mux.Handle("GET /api/groups", api(http.HandlerFunc(h.apiGroups)))
	mux.Handle("POST /api/groups", api(http.HandlerFunc(h.apiGroups)))
	mux.Handle("GET /api/groups/{id}", api(http.HandlerFunc(h.apiGroup)))
	mux.Handle("POST /api/groups/{id}/leader", api(http.HandlerFunc(h.apiSetLeader)))
	mux.Handle("POST /api/groups/{id}/members", api(http.HandlerFunc(h.apiMembers)))
	mux.Handle("POST /api/groups/{id}/grants", api(http.HandlerFunc(h.apiGrants)))
}

func apiJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func apiBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		apiJSON(w, http.StatusUnsupportedMediaType, map[string]string{"error": "Content-Type must be application/json"})
		return false
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		apiJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return false
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		apiJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return false
	}
	return true
}

func apiError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	message := "internal error"
	if msg, ok := userMessage(err); ok {
		status, message = http.StatusBadRequest, msg
	} else if errors.Is(err, ErrNotFound) {
		status, message = http.StatusNotFound, "not found"
	}
	if status == http.StatusInternalServerError {
		log.Printf("admin api: %v", err)
	}
	apiJSON(w, status, map[string]string{"error": message})
}

func (h *Handler) apiUsers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		apiJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	users, err := h.svc.ListUsers(r.Context())
	if err != nil {
		apiError(w, err)
		return
	}
	if users == nil {
		users = []User{}
	}
	apiJSON(w, http.StatusOK, map[string]any{"users": users})
}

func (h *Handler) apiCreateUser(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
		IsAdmin  bool   `json:"is_platform_admin"`
	}
	if !apiBody(w, r, &in) {
		return
	}
	if err := h.svc.CreateUser(r.Context(), in.Username, in.Password, in.IsAdmin); err != nil {
		apiError(w, err)
		return
	}
	apiJSON(w, http.StatusCreated, map[string]bool{"created": true})
}

func (h *Handler) apiGroups(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		var in struct {
			Name string `json:"name"`
		}
		if !apiBody(w, r, &in) {
			return
		}
		if err := h.svc.CreateGroup(r.Context(), in.Name); err != nil {
			apiError(w, err)
			return
		}
		apiJSON(w, http.StatusCreated, map[string]bool{"created": true})
		return
	}
	groups, err := h.svc.ListGroups(r.Context())
	if err != nil {
		apiError(w, err)
		return
	}
	if groups == nil {
		groups = []Group{}
	}
	apiJSON(w, http.StatusOK, map[string]any{"groups": groups})
}

func (h *Handler) apiGroup(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		apiJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid group id"})
		return
	}
	g, err := h.svc.GetGroup(r.Context(), id)
	if err != nil {
		apiError(w, err)
		return
	}
	members, err := h.svc.ListMembers(r.Context(), id)
	if err != nil {
		apiError(w, err)
		return
	}
	grants, err := h.svc.ListGrants(r.Context(), id)
	if err != nil {
		apiError(w, err)
		return
	}
	apiJSON(w, http.StatusOK, map[string]any{"group": g, "members": members, "grants": grants})
}

func (h *Handler) apiSetLeader(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		apiJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid group id"})
		return
	}
	var in struct {
		UserID int64 `json:"user_id"`
	}
	if !apiBody(w, r, &in) {
		return
	}
	setter, ok := h.svc.(interface {
		SetGroupLeader(context.Context, int64, int64) error
	})
	if !ok {
		apiJSON(w, http.StatusNotImplemented, map[string]string{"error": "group leader management is unavailable"})
		return
	}
	if err := setter.SetGroupLeader(r.Context(), id, in.UserID); err != nil {
		apiError(w, err)
		return
	}
	apiJSON(w, http.StatusOK, map[string]any{"group_id": id, "leader_user_id": in.UserID})
}

func (h *Handler) apiMembers(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		apiJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid group id"})
		return
	}
	var in struct {
		UserID int64  `json:"user_id"`
		Action string `json:"action"`
	}
	if !apiBody(w, r, &in) {
		return
	}
	var err error
	actor := actorID(r)
	if strings.EqualFold(in.Action, "remove") {
		err = h.svc.RemoveMember(r.Context(), actor, id, in.UserID)
	} else {
		err = h.svc.AddMember(r.Context(), actor, id, in.UserID)
	}
	if err != nil {
		apiError(w, err)
		return
	}
	apiJSON(w, http.StatusOK, map[string]bool{"updated": true})
}

func (h *Handler) apiGrants(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		apiJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid group id"})
		return
	}
	var in struct {
		DatabaseID int64  `json:"database_id"`
		Level      string `json:"level"`
		Action     string `json:"action"`
	}
	if !apiBody(w, r, &in) {
		return
	}
	var err error
	if strings.EqualFold(in.Action, "remove") {
		err = h.svc.RemoveGrant(r.Context(), actorID(r), id, in.DatabaseID)
	} else {
		level, parseErr := access.ParseLevel(strings.ToLower(in.Level))
		if parseErr != nil {
			apiError(w, &ValidationError{Msg: "Choose an access level."})
			return
		}
		err = h.svc.SetGrant(r.Context(), actorID(r), id, in.DatabaseID, level)
	}
	if err != nil {
		apiError(w, err)
		return
	}
	apiJSON(w, http.StatusOK, map[string]bool{"updated": true})
}

func (h *Handler) outcome(w http.ResponseWriter, op string, err error, success string) (notice, bool) {
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

func (h *Handler) UsersPage(w http.ResponseWriter, r *http.Request) {
	view, ok := h.usersView(w, r, notice{})
	if !ok {
		return
	}
	h.tmpl.Page(w, r, http.StatusOK, "admin/users", "Users", view)
}

func (h *Handler) usersView(w http.ResponseWriter, r *http.Request, n notice) (usersView, bool) {
	users, err := h.svc.ListUsers(r.Context())
	if err != nil {
		h.fail(w, "list users", err)
		return usersView{}, false
	}
	return usersView{notice: n, Users: users, CurrentUserID: actorID(r)}, true
}

func (h *Handler) renderUsers(w http.ResponseWriter, r *http.Request, n notice) {
	if view, ok := h.usersView(w, r, n); ok {
		h.tmpl.Fragment(w, http.StatusOK, "admin/users", "users", view)
	}
}

func (h *Handler) CreateUser(w http.ResponseWriter, r *http.Request) {
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

func (h *Handler) SetUserActive(w http.ResponseWriter, r *http.Request) {
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

func (h *Handler) ResetPassword(w http.ResponseWriter, r *http.Request) {
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

func (h *Handler) renderGroups(w http.ResponseWriter, r *http.Request, n notice) {
	groups, err := h.svc.ListGroups(r.Context())
	if err != nil {
		h.fail(w, "list groups", err)
		return
	}
	h.tmpl.Fragment(w, http.StatusOK, "admin/groups", "groups", groupsView{notice: n, Groups: groups})
}

func (h *Handler) GroupsPage(w http.ResponseWriter, r *http.Request) {
	groups, err := h.svc.ListGroups(r.Context())
	if err != nil {
		h.fail(w, "list groups", err)
		return
	}
	h.tmpl.Page(w, r, http.StatusOK, "admin/groups", "Groups", groupsView{Groups: groups})
}

func (h *Handler) CreateGroup(w http.ResponseWriter, r *http.Request) {
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

func (h *Handler) DeleteGroup(w http.ResponseWriter, r *http.Request) {
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

func (h *Handler) membersView(w http.ResponseWriter, r *http.Request, groupID int64, n notice) (membersView, bool) {
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

func (h *Handler) grantsView(w http.ResponseWriter, r *http.Request, groupID int64, n notice) (grantsView, bool) {
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

func (h *Handler) renderMembers(w http.ResponseWriter, r *http.Request, groupID int64, n notice) {
	if view, ok := h.membersView(w, r, groupID, n); ok {
		h.tmpl.Fragment(w, http.StatusOK, "admin/group", "members", view)
	}
}

func (h *Handler) renderGrants(w http.ResponseWriter, r *http.Request, groupID int64, n notice) {
	if view, ok := h.grantsView(w, r, groupID, n); ok {
		h.tmpl.Fragment(w, http.StatusOK, "admin/group", "grants", view)
	}
}

func (h *Handler) GroupPage(w http.ResponseWriter, r *http.Request) {
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

func (h *Handler) AddMember(w http.ResponseWriter, r *http.Request) {
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

func (h *Handler) RemoveMember(w http.ResponseWriter, r *http.Request) {
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

func (h *Handler) SetGrant(w http.ResponseWriter, r *http.Request) {
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

func (h *Handler) RemoveGrant(w http.ResponseWriter, r *http.Request) {
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

package admin

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"mime"
	"net/http"
	"strings"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/access"
)

func (h *Handler) RegisterAPIRoutes(mux *http.ServeMux, guard *access.Guard) {
	api := guard.RequireAdmin
	mux.Handle("GET /api/users", api(http.HandlerFunc(h.apiUsers)))
	mux.Handle("POST /api/users", api(http.HandlerFunc(h.apiCreateUser)))
	mux.Handle("GET /api/groups", api(http.HandlerFunc(h.apiGroups)))
	mux.Handle("POST /api/groups", api(http.HandlerFunc(h.apiGroups)))
	mux.Handle("GET /api/groups/{id}", api(http.HandlerFunc(h.apiGroup)))
	mux.Handle("DELETE /api/groups/{id}", api(http.HandlerFunc(h.apiDeleteGroup)))
	mux.Handle("POST /api/users/{id}/active", api(http.HandlerFunc(h.apiUserActive)))
	mux.Handle("POST /api/users/{id}/password", api(http.HandlerFunc(h.apiUserPassword)))
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

func (h *Handler) apiDeleteGroup(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		access.WriteAPIError(w, "Invalid group ID.", http.StatusBadRequest)
		return
	}
	if err := h.svc.DeleteGroup(r.Context(), actorID(r), id); err != nil {
		apiError(w, err)
		return
	}
	apiJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}

func (h *Handler) apiUserActive(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		access.WriteAPIError(w, "Invalid user ID.", http.StatusBadRequest)
		return
	}
	var input struct {
		Active *bool `json:"active"`
	}
	if !apiBody(w, r, &input) {
		return
	}
	if input.Active == nil {
		access.WriteAPIError(w, "Active status is required.", http.StatusBadRequest)
		return
	}
	if err := h.svc.SetUserActive(r.Context(), actorID(r), id, *input.Active); err != nil {
		apiError(w, err)
		return
	}
	apiJSON(w, http.StatusOK, map[string]bool{"updated": true})
}

func (h *Handler) apiUserPassword(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		access.WriteAPIError(w, "Invalid user ID.", http.StatusBadRequest)
		return
	}
	var input struct {
		Password string `json:"password"`
	}
	if !apiBody(w, r, &input) {
		return
	}
	if err := h.svc.ResetPassword(r.Context(), id, input.Password); err != nil {
		apiError(w, err)
		return
	}
	apiJSON(w, http.StatusOK, map[string]bool{"updated": true})
}

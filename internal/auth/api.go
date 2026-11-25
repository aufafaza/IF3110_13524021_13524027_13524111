package auth

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"mime"
	"net/http"
	"time"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/access"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/session"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/web/csrf"
)

type sessionUser struct {
	ID              int64  `json:"id"`
	Username        string `json:"username"`
	IsPlatformAdmin bool   `json:"is_platform_admin"`
}

func apiUser(user access.User) sessionUser {
	return sessionUser{user.ID, user.Username, user.IsPlatformAdmin}
}

func (h *Handler) Session(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	user, authenticated := access.UserFromContext(r.Context())
	if authenticated {
		token, ok := csrf.TokenFromContext(r.Context())
		if !ok {
			access.WriteAPIError(w, "internal error", 500)
			return
		}
		authJSON(w, 200, map[string]any{"authenticated": true, "user": apiUser(user), "csrf_token": token})
		return
	}
	token, err := csrf.NewPreSessionToken()
	if err != nil {
		access.WriteAPIError(w, "internal error", 500)
		return
	}
	h.apiPreSessionCookie(w, token, 0)
	authJSON(w, 200, map[string]any{"authenticated": false, "user": nil, "csrf_token": token})
}

func (h *Handler) apiPreSessionCookie(w http.ResponseWriter, token string, maxAge int) {
	http.SetCookie(w, &http.Cookie{Name: csrf.PreSessionCookie, Value: token, Path: "/api/auth", HttpOnly: true, Secure: h.cookie.Secure, SameSite: h.cookie.SameSite, MaxAge: maxAge})
}

func (h *Handler) APILogin(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		access.WriteAPIError(w, "Content-Type must be application/json", 415)
		return
	}
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&in); err != nil {
		access.WriteAPIError(w, "invalid request", 400)
		return
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		access.WriteAPIError(w, "invalid request", 400)
		return
	}
	if in.Username == "" || in.Password == "" {
		access.WriteAPIError(w, "username and password are required", 400)
		return
	}
	user, err := h.auth.Login(r.Context(), in.Username, in.Password)
	if errors.Is(err, ErrInvalidCredentials) {
		access.WriteAPIError(w, "Invalid username or password", 401)
		return
	}
	if err != nil {
		log.Printf("api login: %v", err)
		access.WriteAPIError(w, "internal error", 500)
		return
	}
	if old, ok := session.ReadCookie(r, h.cookie); ok {
		if err := h.sessions.Logout(r.Context(), old); err != nil && !errors.Is(err, session.ErrUnauthenticated) {
			log.Printf("api login: revoke previous session: %v", err)
			access.WriteAPIError(w, "internal error", 500)
			return
		}
	}
	raw, err := h.sessions.Login(r.Context(), user.ID)
	if err != nil {
		log.Printf("api login: create session: %v", err)
		access.WriteAPIError(w, "internal error", 500)
		return
	}
	session.SetCookie(w, h.cookie, raw, time.Time{})
	h.apiPreSessionCookie(w, "", -1)
	authJSON(w, 200, map[string]any{"user": apiUser(user)})
}

func (h *Handler) APILogout(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if raw, ok := session.ReadCookie(r, h.cookie); ok {
		if err := h.sessions.Logout(r.Context(), raw); err != nil && !errors.Is(err, session.ErrUnauthenticated) {
			log.Printf("api logout: %v", err)
			access.WriteAPIError(w, "internal error", 500)
			return
		}
	}
	session.ClearCookie(w, h.cookie)
	h.apiPreSessionCookie(w, "", -1)
	authJSON(w, 200, map[string]bool{"authenticated": false})
}

func authJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

package auth

import "net/http"

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/auth/session", h.Session)
	mux.HandleFunc("POST /api/auth/login", h.APILogin)
	mux.HandleFunc("POST /api/auth/logout", h.APILogout)
	mux.HandleFunc("GET /login", h.GetLogin)
	mux.HandleFunc("POST /login", h.PostLogin)
	mux.HandleFunc("POST /logout", h.PostLogout)
}

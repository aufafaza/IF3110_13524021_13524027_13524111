package auth

import "net/http"

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /login", h.GetLogin)
	mux.HandleFunc("POST /login", h.PostLogin)
	mux.HandleFunc("POST /logout", h.PostLogout)
}

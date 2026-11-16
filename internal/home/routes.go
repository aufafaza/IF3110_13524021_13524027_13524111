package home

import (
	"net/http"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/access"
)

func (h *Handler) RegisterRoutes(mux *http.ServeMux, guard *access.Guard) {
	mux.Handle("GET /{$}", guard.RequireLogin(http.HandlerFunc(h.Index)))
}

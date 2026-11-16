package home

import (
	"net/http"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/web/templates"
)

type Handler struct {
	tmpl *templates.Set
}

func NewHandler(tmpl *templates.Set) *Handler {
	return &Handler{tmpl: tmpl}
}

func (h *Handler) Index(w http.ResponseWriter, r *http.Request) {
	h.tmpl.Page(w, r, http.StatusOK, "home", "Home", nil)
}

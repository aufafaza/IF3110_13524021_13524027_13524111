package handlers

import (
	"net/http"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/web/templates"
)

type HomeHandler struct {
	tmpl *templates.Set
}

func NewHomeHandler(tmpl *templates.Set) *HomeHandler {
	return &HomeHandler{tmpl: tmpl}
}

func (h *HomeHandler) Index(w http.ResponseWriter, r *http.Request) {
	h.tmpl.Page(w, r, http.StatusOK, "home", "Home", nil)
}

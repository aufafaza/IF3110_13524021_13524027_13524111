package database

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/access"
)

func (h *Handler) listDatabasesAPI(w http.ResponseWriter, r *http.Request) {
	user, ok := access.UserFromContext(r.Context())
	if !ok || user.ID == 0 {
		access.WriteAPIError(w, "unauthenticated", http.StatusUnauthorized)
		return
	}
	databases, err := h.accessibleDatabases(r.Context(), user.ID)
	if err != nil {
		log.Printf("database API: list databases: %v", err)
		access.WriteAPIError(w, "Could not load databases.", http.StatusInternalServerError)
		return
	}
	writeBrowsingJSON(w, map[string]any{"databases": databases})
}

func (h *Handler) listTablesAPI(w http.ResponseWriter, r *http.Request) {
	id, _ := pathID(r, access.DatabaseIDParam) 
	ctx := r.Context()
	db, err := h.reg.Get(ctx, id)
	if err != nil {
		writeBrowsingError(w, err)
		return
	}
	tables, err := h.viewer.Tables(ctx, id)
	if err != nil {
		writeBrowsingError(w, err)
		return
	}
	type tableSummary struct {
		Schema string `json:"schema"`
		Name   string `json:"name"`
	}
	result := make([]tableSummary, 0, len(tables))
	for _, table := range tables {
		result = append(result, tableSummary{Schema: table.Schema, Name: table.Name})
	}
	writeBrowsingJSON(w, map[string]any{
		"database": accessibleDB{ID: db.ID, Name: db.Name},
		"tables":   result,
	})
}

func writeBrowsingJSON(w http.ResponseWriter, body any) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}

func writeBrowsingError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrDatabaseNotFound):
		access.WriteAPIError(w, "Database not found.", http.StatusNotFound)
	case errors.Is(err, ErrConnectionFailed):
		access.WriteAPIError(w, "Could not connect to the database.", http.StatusBadGateway)
	default:
		log.Printf("database API: list tables: %v", err)
		access.WriteAPIError(w, "Could not load tables.", http.StatusInternalServerError)
	}
}

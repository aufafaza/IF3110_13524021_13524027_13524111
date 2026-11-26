package database

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"mime"
	"net/http"
	"time"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/access"
)

func (h *Handler) RegisterAPIRoutes(mux *http.ServeMux, guard *access.Guard) {
	admin := guard.RequireAdmin
	mux.Handle("GET /api/admin/databases", admin(http.HandlerFunc(h.apiAdminDatabases)))
	mux.Handle("POST /api/admin/databases", admin(http.HandlerFunc(h.apiAdminCreateDatabase)))
	mux.Handle("GET /api/admin/databases/{id}", admin(http.HandlerFunc(h.apiAdminDatabase)))
	mux.Handle("PUT /api/admin/databases/{id}", admin(http.HandlerFunc(h.apiAdminUpdateDatabase)))
	mux.Handle("DELETE /api/admin/databases/{id}", admin(http.HandlerFunc(h.apiAdminDeleteDatabase)))
	mux.Handle("POST /api/admin/databases/test", admin(http.HandlerFunc(h.apiTestNew)))
	mux.Handle("POST /api/admin/databases/{id}/test", admin(http.HandlerFunc(h.apiTestSaved)))
}

func databaseAPIJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func databaseAPIError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	message := "internal error"
	switch {
	case errors.Is(err, ErrDatabaseNotFound), errors.Is(err, ErrTableNotFound):
		status, message = http.StatusNotFound, "not found"
	case errors.Is(err, ErrInvalidInput), errors.Is(err, ErrNameTaken):
		status, message = http.StatusBadRequest, err.Error()
	}
	if status == http.StatusInternalServerError {
		log.Printf("database api: %v", err)
	}
	databaseAPIJSON(w, status, map[string]string{"error": message})
}

func readDatabaseAPIJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		databaseAPIJSON(w, http.StatusUnsupportedMediaType, map[string]string{"error": "Content-Type must be application/json"})
		return false
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		databaseAPIJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return false
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		databaseAPIJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return false
	}
	return true
}

type databaseAPIView struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Host      string    `json:"host"`
	Port      int       `json:"port"`
	DBName    string    `json:"db_name"`
	DBUser    string    `json:"db_user"`
	SSLMode   string    `json:"sslmode"`
	CreatedAt time.Time `json:"created_at"`
}

func publicDatabase(d Database) databaseAPIView {
	return databaseAPIView{d.ID, d.Name, d.Host, d.Port, d.DBName, d.DBUser, d.SSLMode, d.CreatedAt}
}

func (h *Handler) apiAdminDatabases(w http.ResponseWriter, r *http.Request) {
	dbs, err := h.reg.List(r.Context())
	if err != nil {
		databaseAPIError(w, err)
		return
	}
	out := make([]databaseAPIView, 0, len(dbs))
	for _, d := range dbs {
		out = append(out, publicDatabase(d))
	}
	databaseAPIJSON(w, http.StatusOK, map[string]any{"databases": out})
}

func (h *Handler) apiAdminCreateDatabase(w http.ResponseWriter, r *http.Request) {
	var in DatabaseInput
	if !readDatabaseAPIJSON(w, r, &in) {
		return
	}
	d, err := h.reg.Create(r.Context(), in)
	if err != nil {
		databaseAPIError(w, err)
		return
	}
	databaseAPIJSON(w, http.StatusCreated, publicDatabase(d))
}

func (h *Handler) apiAdminDatabase(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		databaseAPIJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid database id"})
		return
	}
	d, err := h.reg.Get(r.Context(), id)
	if err != nil {
		databaseAPIError(w, err)
		return
	}
	databaseAPIJSON(w, http.StatusOK, publicDatabase(d))
}

func (h *Handler) apiAdminUpdateDatabase(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		databaseAPIJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid database id"})
		return
	}
	var in DatabaseInput
	if !readDatabaseAPIJSON(w, r, &in) {
		return
	}
	if err := h.reg.Update(r.Context(), id, in); err != nil {
		databaseAPIError(w, err)
		return
	}
	d, err := h.reg.Get(r.Context(), id)
	if err != nil {
		databaseAPIError(w, err)
		return
	}
	databaseAPIJSON(w, http.StatusOK, publicDatabase(d))
}

func (h *Handler) apiAdminDeleteDatabase(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		databaseAPIJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid database id"})
		return
	}
	if err := h.reg.Delete(r.Context(), id); err != nil {
		databaseAPIError(w, err)
		return
	}
	databaseAPIJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}

func (h *Handler) apiTestNew(w http.ResponseWriter, r *http.Request) {
	var in DatabaseInput
	if !readDatabaseAPIJSON(w, r, &in) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	writeConnectionTest(w, h.test(ctx, in))
}

func (h *Handler) apiTestSaved(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		access.WriteAPIError(w, "Invalid database ID.", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	pool, err := h.reg.Pool(ctx, id)
	if err == nil {
		if pingErr := pool.Ping(ctx); pingErr != nil {
			err = &ConnError{Reason: "Could not reach the database."}
		}
	}
	writeConnectionTest(w, err)
}

func writeConnectionTest(w http.ResponseWriter, err error) {
	if msg, ok := connMessage(err); ok {
		access.WriteAPIError(w, msg, http.StatusBadGateway)
		return
	}
	if err != nil {
		databaseAPIError(w, err)
		return
	}
	databaseAPIJSON(w, http.StatusOK, map[string]bool{"connected": true})
}

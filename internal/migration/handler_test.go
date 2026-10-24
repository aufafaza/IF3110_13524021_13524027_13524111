package migration_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/migration"
)

func setupTestHandler() (*migration.Handler, *stubExecutor) {
	repo := newMemoryRepository()
	locker := &noopLocker{}
	exec := &stubExecutor{}
	checker := &stubChecker{
		grants: map[[2]int64]migration.AccessLevel{
			{1, 10}: migration.LevelNone,
			{2, 10}: migration.LevelView,
			{3, 10}: migration.LevelEdit,
		},
	}
	runner := migration.NewRunner(repo, locker, exec)
	svc := migration.NewService(repo, runner, checker)
	return migration.NewHandler(svc), exec
}

func requestAsUser(method, target string, user migration.User, form url.Values) *http.Request {
	var body *strings.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	} else {
		body = strings.NewReader("")
	}
	req := httptest.NewRequest(method, target, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if user.ID != 0 {
		req = req.WithContext(migration.WithUser(req.Context(), user))
	}
	return req
}

func TestHandlerAccessControlAndUI(t *testing.T) {
	h, exec := setupTestHandler()

	noAccessUser := migration.User{ID: 1, Username: "outsider"}
	viewUser := migration.User{ID: 2, Username: "viewer"}
	editUser := migration.User{ID: 3, Username: "editor"}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, requestAsUser(http.MethodGet, "/databases/10/migrations", migration.User{}, nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated GET history: status = %d, want 401", rec.Code)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, requestAsUser(http.MethodGet, "/databases/10/migrations", noAccessUser, nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("LevelNone GET history: status = %d, want 403", rec.Code)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, requestAsUser(http.MethodGet, "/databases/10/migrations", viewUser, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("LevelView GET history: status = %d, want 200", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "+ New Migration") {
		t.Errorf("LevelView history page must not render '+ New Migration' button")
	}
	if !strings.Contains(rec.Body.String(), "View Only") {
		t.Errorf("LevelView history page should indicate View Only access")
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, requestAsUser(http.MethodGet, "/databases/10/migrations/new", viewUser, nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("LevelView GET /new: status = %d, want 403", rec.Code)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, requestAsUser(http.MethodPost, "/databases/10/migrations", viewUser, url.Values{
		"title":  {"Unauthorized migration"},
		"sql":    {"CREATE TABLE hack (id int);"},
		"action": {"save"},
	}))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("LevelView POST /migrations: status = %d, want 403", rec.Code)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, requestAsUser(http.MethodPost, "/databases/10/migrations", editUser, url.Values{
		"title":  {"Create products"},
		"sql":    {"CREATE TABLE products (id int); INSERT INTO products VALUES (1);"},
		"action": {"preview"},
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("LevelEdit preview: status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "SQL Preview (2 statements)") {
		t.Errorf("expected preview box with 2 statements, got body: %s", rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, requestAsUser(http.MethodPost, "/databases/10/migrations", editUser, url.Values{
		"title":  {"Create products"},
		"sql":    {"CREATE TABLE products (id int);"},
		"action": {"save"},
	}))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("LevelEdit save draft: status = %d, want 303", rec.Code)
	}
	detailLoc := rec.Header().Get("Location")
	if detailLoc != "/databases/10/migrations/1" {
		t.Fatalf("redirect Location = %q, want /databases/10/migrations/1", detailLoc)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, requestAsUser(http.MethodGet, detailLoc, viewUser, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("LevelView GET detail: status = %d, want 200", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "Execute Migration") {
		t.Errorf("LevelView detail page must not render Execute Migration button")
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, requestAsUser(http.MethodPost, detailLoc+"/execute", viewUser, url.Values{"confirm": {"true"}}))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("LevelView POST /execute: status = %d, want 403", rec.Code)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, requestAsUser(http.MethodPost, detailLoc+"/execute", editUser, url.Values{}))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("LevelEdit POST /execute without confirm: status = %d, want 400", rec.Code)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, requestAsUser(http.MethodPost, detailLoc+"/execute", editUser, url.Values{"confirm": {"true"}}))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("LevelEdit POST /execute with confirm: status = %d, want 303", rec.Code)
	}
	if exec.calls != 1 {
		t.Fatalf("exec.calls = %d, want 1", exec.calls)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, requestAsUser(http.MethodGet, detailLoc, viewUser, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET detail after execute: status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "v1") || !strings.Contains(rec.Body.String(), "applied") {
		t.Errorf("detail page should display v1 and applied status")
	}
}

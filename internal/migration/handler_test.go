package migration_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/access"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/migration"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/web/csrf"
)

func setupTestHandler() (http.Handler, *stubExecutor) {
	repo := newMemoryRepository()
	exec := &stubExecutor{}
	checker := &stubChecker{grants: map[[2]int64]access.Level{
		{1, 10}: access.LevelNone, {2, 10}: access.LevelView, {3, 10}: access.LevelEdit,
	}}
	runner := migration.NewRunner(repo, &noopLocker{}, &planExecutor{exec})
	svc := migration.NewService(repo, runner, checker)
	mux := http.NewServeMux()
	migration.NewHandler(svc).RegisterRoutes(mux)
	return mux, exec
}

func requestAsUser(method, target string, user access.User, body string) *http.Request {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if user.ID != 0 {
		req = req.WithContext(access.ContextWithUser(req.Context(), user))
	}
	return req
}

func callAPI(t *testing.T, h http.Handler, method, path string, userID int64, body string, status int) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, requestAsUser(method, path, access.User{ID: userID}, body))
	if rec.Code != status {
		t.Fatalf("%s %s: got %d %s, want %d", method, path, rec.Code, rec.Body.String(), status)
	}
	if rec.Header().Get("Content-Type") != "application/json" || !json.Valid(rec.Body.Bytes()) {
		t.Fatalf("expected JSON response, got %s: %s", rec.Header().Get("Content-Type"), rec.Body.String())
	}
	return rec
}

func TestMigrationAPIWorkflow(t *testing.T) {
	h, exec := setupTestHandler()
	base := "/databases/10/migrations"
	view := callAPI(t, h, "GET", base, 2, "", 200)
	var history struct {
		CanEdit    bool                  `json:"can_edit"`
		Migrations []migration.Migration `json:"migrations"`
	}
	if err := json.Unmarshal(view.Body.Bytes(), &history); err != nil {
		t.Fatal(err)
	}
	if history.CanEdit || history.Migrations == nil || len(history.Migrations) != 0 {
		t.Fatalf("unexpected history: %+v", history)
	}
	preview := callAPI(t, h, "POST", base, 3, `{"title":"Products","sql":"SELECT 1; SELECT 2;","action":"preview"}`, 200)
	var result migration.PreviewResult
	if err := json.Unmarshal(preview.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.StatementCount != 2 || exec.calls != 0 {
		t.Fatalf("preview: %+v, executions %d", result, exec.calls)
	}
	created := callAPI(t, h, "POST", base, 3, `{"title":"Products","sql":"SELECT 1;"}`, 201)
	var draft migration.Migration
	if err := json.Unmarshal(created.Body.Bytes(), &draft); err != nil {
		t.Fatal(err)
	}
	path := created.Header().Get("Location")
	if draft.ID != 1 || draft.Status != migration.StatusDraft || path != base+"/1" {
		t.Fatalf("unexpected draft: %+v, location %s", draft, path)
	}
	callAPI(t, h, "GET", path, 2, "", 200)
	callAPI(t, h, "POST", path, 3, `{"title":"Updated","sql":"SELECT 2;","action":"save"}`, 200)
	callAPI(t, h, "POST", path+"/execute", 2, `{"confirm":true}`, 403)
	callAPI(t, h, "POST", path+"/execute", 3, `{}`, 400)
	applied := callAPI(t, h, "POST", path+"/execute", 3, `{"confirm":true}`, 200)
	var done migration.Migration
	if err := json.Unmarshal(applied.Body.Bytes(), &done); err != nil {
		t.Fatal(err)
	}
	if done.Status != migration.StatusApplied || done.Version == nil || *done.Version != 1 || exec.calls != 1 {
		t.Fatalf("execution: %+v, calls %d", done, exec.calls)
	}
	callAPI(t, h, "POST", path, 3, `{"title":"Updated","sql":"SELECT 3;"}`, 409)
	callAPI(t, h, "POST", path+"/execute", 3, `{"confirm":true}`, 409)
	if exec.calls != 1 {
		t.Fatal("executed twice")
	}
}

func TestMigrationAPIErrors(t *testing.T) {
	for _, tt := range []struct {
		name, method, path, body string
		user                     int64
		status                   int
	}{
		{"anonymous", "GET", "", "", 0, 401},
		{"no grant", "GET", "", "", 1, 403},
		{"view cannot create", "POST", "", `{"title":"Denied","sql":"SELECT 1;"}`, 2, 403},
		{"invalid database", "GET", "/databases/bad/migrations", "", 3, 400},
		{"invalid migration", "GET", "/bad", "", 3, 400},
		{"missing migration", "GET", "/99", "", 3, 404},
		{"malformed JSON", "POST", "", `{`, 3, 400},
		{"multiple JSON values", "POST", "", `{} {}`, 3, 400},
		{"unknown field", "POST", "", `{"typo":true}`, 3, 400},
		{"unknown action", "POST", "", `{"action":"oops"}`, 3, 400},
		{"empty title", "POST", "", `{"title":"","sql":"SELECT 1;"}`, 3, 400},
		{"form body", "POST", "", "title=hello", 3, 400},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h, exec := setupTestHandler()
			path := "/databases/10/migrations" + tt.path
			if strings.HasPrefix(tt.path, "/databases/") {
				path = tt.path
			}
			callAPI(t, h, tt.method, path, tt.user, tt.body, tt.status)
			if exec.calls != 0 {
				t.Fatal("error executed SQL")
			}
		})
	}
	h, _ := setupTestHandler()
	req := requestAsUser("POST", "/databases/10/migrations", access.User{ID: 3}, "title=hello")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 415 {
		t.Fatalf("form request: %d", rec.Code)
	}
}

func TestMigrationExecutionFailure(t *testing.T) {
	h, exec := setupTestHandler()
	exec.err = errors.New("execution failed")
	created := callAPI(t, h, "POST", "/databases/10/migrations", 3, `{"title":"Failure","sql":"SELECT 1;"}`, 201)
	path := created.Header().Get("Location")
	rec := callAPI(t, h, "POST", path+"/execute", 3, `{"confirm":true}`, 422)
	var failed migration.Migration
	if err := json.Unmarshal(rec.Body.Bytes(), &failed); err != nil {
		t.Fatal(err)
	}
	if failed.Status != migration.StatusFailed || failed.ErrorMessage == nil {
		t.Fatalf("missing failure record: %+v", failed)
	}
	callAPI(t, h, "GET", path, 2, "", 200)
}

func TestMigrationJSONStillRequiresCSRF(t *testing.T) {
	h, _ := setupTestHandler()
	protected := csrf.Protect(csrf.PreSessionCookie)(h)
	for _, token := range []string{"", "wrong", "expected"} {
		req := requestAsUser("POST", "/databases/10/migrations", access.User{ID: 3}, `{"title":"Safe","sql":"SELECT 1;"}`)
		req = req.WithContext(csrf.ContextWithToken(req.Context(), "expected"))
		req.Header.Set(csrf.Header, token)
		rec := httptest.NewRecorder()
		protected.ServeHTTP(rec, req)
		want := 403
		if token == "expected" {
			want = 201
		}
		if rec.Code != want {
			t.Fatalf("token %q: status %d, want %d", token, rec.Code, want)
		}
	}
}

type planExecutor struct{ *stubExecutor }

func (e *planExecutor) AnalyzePlan(_ context.Context, _ int64, sql string, analyze bool) (migration.ExecutionPlanReport, error) {
	return migration.ExecutionPlanReport{Analyze: analyze, Statements: []migration.StatementPlan{{SQL: sql, IsExplainable: true}}}, nil
}

func TestMigrationExplanationAPI(t *testing.T) {
	h, exec := setupTestHandler()
	base := "/databases/10/migrations"
	created := callAPI(t, h, "POST", base, 3, `{"title":"Plan","sql":"SELECT 1;"}`, 201)
	path := created.Header().Get("Location")
	rec := callAPI(t, h, "POST", path+"/explain", 3, `{"analyze":true}`, 200)
	var report migration.ExecutionPlanReport
	if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if !report.Analyze || len(report.Statements) != 1 || report.Statements[0].SQL != "SELECT 1;" {
		t.Fatalf("unexpected plan: %+v", report)
	}
	callAPI(t, h, "POST", path+"/explain", 2, `{}`, 403)
	callAPI(t, h, "POST", path+"/explain", 3, `{"analyze":"yes"}`, 400)
	callAPI(t, h, "POST", path, 3, `{"title":"Plan","sql":"SELECT 2;","action":"explain_analyze"}`, 200)
	detail := callAPI(t, h, "GET", path, 2, "", 200)
	var draft migration.Migration
	if err := json.Unmarshal(detail.Body.Bytes(), &draft); err != nil {
		t.Fatal(err)
	}
	if draft.SQL != "SELECT 1;" || draft.Status != migration.StatusDraft || exec.calls != 0 {
		t.Fatalf("explanation changed draft or executed migration: %+v", draft)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, requestAsUser("GET", path+"/edit", access.User{ID: 3}, ""))
	if rec.Code != 404 {
		t.Fatalf("removed editor route: status %d", rec.Code)
	}
}

package handlers_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/access"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/admin"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/web/handlers"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/web/templates"
)

const actorUserID = 1

type adminCalls struct {
	created      []string
	createdPass  []string
	createdAdmin []bool
	active       []string
	reset        []string
	groupsMade   []string
	groupsGone   []int64
	added        []string
	removed      []string
	grants       []string
	ungranted    []string
}

type fakeAdminSvc struct {
	handlers.AdminService

	calls adminCalls
	err   error

	users      []admin.User
	groups     []admin.Group
	group      admin.Group
	members    []admin.User
	candidates []admin.User
	grants     []admin.Grant
	databases  []admin.Database
	getErr     error
	listErr    error
}

func (f *fakeAdminSvc) ListUsers(context.Context) ([]admin.User, error) { return f.users, f.listErr }
func (f *fakeAdminSvc) ListGroups(context.Context) ([]admin.Group, error) {
	return f.groups, f.listErr
}
func (f *fakeAdminSvc) GetGroup(context.Context, int64) (admin.Group, error) {
	return f.group, f.getErr
}
func (f *fakeAdminSvc) ListMembers(context.Context, int64) ([]admin.User, error) {
	return f.members, nil
}
func (f *fakeAdminSvc) ListNonMembers(context.Context, int64) ([]admin.User, error) {
	return f.candidates, nil
}
func (f *fakeAdminSvc) ListGrants(context.Context, int64) ([]admin.Grant, error) {
	return f.grants, nil
}
func (f *fakeAdminSvc) ListDatabases(context.Context) ([]admin.Database, error) {
	return f.databases, nil
}

func (f *fakeAdminSvc) CreateUser(_ context.Context, username, password string, isAdmin bool) error {
	f.calls.created = append(f.calls.created, username)
	f.calls.createdPass = append(f.calls.createdPass, password)
	f.calls.createdAdmin = append(f.calls.createdAdmin, isAdmin)
	return f.err
}

func (f *fakeAdminSvc) SetUserActive(_ context.Context, actor, userID int64, active bool) error {
	f.calls.active = append(f.calls.active, key(actor, userID, active))
	return f.err
}

func (f *fakeAdminSvc) ResetPassword(_ context.Context, userID int64, password string) error {
	f.calls.reset = append(f.calls.reset, key(userID, password))
	return f.err
}

func (f *fakeAdminSvc) CreateGroup(_ context.Context, name string) error {
	f.calls.groupsMade = append(f.calls.groupsMade, name)
	return f.err
}

func (f *fakeAdminSvc) DeleteGroup(_ context.Context, _, groupID int64) error {
	f.calls.groupsGone = append(f.calls.groupsGone, groupID)
	return f.err
}

func (f *fakeAdminSvc) AddMember(_ context.Context, actor, groupID, userID int64) error {
	f.calls.added = append(f.calls.added, key(actor, groupID, userID))
	return f.err
}

func (f *fakeAdminSvc) RemoveMember(_ context.Context, actor, groupID, userID int64) error {
	f.calls.removed = append(f.calls.removed, key(actor, groupID, userID))
	return f.err
}

func (f *fakeAdminSvc) SetGrant(_ context.Context, actor, groupID, dbID int64, level access.Level) error {
	f.calls.grants = append(f.calls.grants, key(actor, groupID, dbID, level))
	return f.err
}

func (f *fakeAdminSvc) RemoveGrant(_ context.Context, actor, groupID, dbID int64) error {
	f.calls.ungranted = append(f.calls.ungranted, key(actor, groupID, dbID))
	return f.err
}

func key(parts ...any) string {
	var b strings.Builder
	for i, p := range parts {
		if i > 0 {
			b.WriteString("|")
		}
		switch v := p.(type) {
		case string:
			b.WriteString(v)
		case int64:
			b.WriteString(strconv.FormatInt(v, 10))
		case bool:
			b.WriteString(strconv.FormatBool(v))
		case access.Level:
			b.WriteString(v.String())
		}
	}
	return b.String()
}

type adminRig struct {
	h   *handlers.AdminHandler
	svc *fakeAdminSvc
}

func newAdminRig(t *testing.T) adminRig {
	t.Helper()
	tmpl, err := templates.New()
	if err != nil {
		t.Fatal(err)
	}
	svc := &fakeAdminSvc{}
	return adminRig{h: handlers.NewAdminHandler(svc, tmpl), svc: svc}
}

func adminRequest(method, target string, form url.Values, pathValues map[string]string) *http.Request {
	var req *http.Request
	if form != nil {
		req = httptest.NewRequest(method, target, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		req = httptest.NewRequest(method, target, nil)
	}
	for k, v := range pathValues {
		req.SetPathValue(k, v)
	}
	ctx := access.ContextWithUser(req.Context(), access.User{ID: actorUserID, Username: "root", IsPlatformAdmin: true})
	return req.WithContext(ctx)
}

func serve(handler http.HandlerFunc, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	handler(rec, req)
	return rec
}

func TestAdmin_UsersPage(t *testing.T) {
	rig := newAdminRig(t)
	rig.svc.users = []admin.User{
		{ID: actorUserID, Username: "root", IsPlatformAdmin: true, IsActive: true},
		{ID: 2, Username: "bob", IsActive: true},
		{ID: 3, Username: "carol", IsActive: false},
	}

	rec := serve(rig.h.UsersPage, adminRequest(http.MethodGet, "/admin/users", nil, nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"root", "bob", "carol", "Platform admin", "Inactive", "Activate"} {
		if !strings.Contains(body, want) {
			t.Errorf("page missing %q", want)
		}
	}
	if !strings.Contains(body, `hx-post="/admin/users/2/active"`) {
		t.Error("no deactivate form for bob")
	}
	if strings.Count(body, "disabled") != 1 {
		t.Errorf("want exactly one disabled button (the acting admin's own row), body has %d", strings.Count(body, "disabled"))
	}
	if !strings.Contains(body, "<html") {
		t.Error("a normal page load must render the full layout")
	}
}

func TestAdmin_CreateUser(t *testing.T) {
	rig := newAdminRig(t)
	rec := serve(rig.h.CreateUser, adminRequest(http.MethodPost, "/admin/users", url.Values{
		"username": {"dave"}, "password": {"a-long-enough-password"}, "is_admin": {"1"},
	}, nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := rig.svc.calls.created; len(got) != 1 || got[0] != "dave" {
		t.Errorf("created = %v", got)
	}
	if !rig.svc.calls.createdAdmin[0] {
		t.Error("platform-admin checkbox was ignored")
	}
	body := rec.Body.String()
	if !strings.Contains(body, "User created.") {
		t.Error("success notice missing")
	}
	if strings.Contains(body, "a-long-enough-password") {
		t.Error("the submitted password was echoed back")
	}
}

func TestAdmin_CreateUser_CheckboxUnticked(t *testing.T) {
	rig := newAdminRig(t)
	serve(rig.h.CreateUser, adminRequest(http.MethodPost, "/admin/users", url.Values{
		"username": {"dave"}, "password": {"a-long-enough-password"},
	}, nil))

	if rig.svc.calls.createdAdmin[0] {
		t.Error("a missing checkbox must not create a platform admin")
	}
}

func TestAdmin_DomainErrorsAreShownNotThrown(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"validation", &admin.ValidationError{Msg: "Password must be 12 to 128 characters."}, "Password must be 12 to 128 characters."},
		{"username taken", admin.ErrUsernameTaken, "That username is already taken."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rig := newAdminRig(t)
			rig.svc.err = tt.err

			rec := serve(rig.h.CreateUser, adminRequest(http.MethodPost, "/admin/users", url.Values{"username": {"x"}}, nil))

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d; htmx only swaps successful responses, so a form error must be a 200 fragment", rec.Code)
			}
			if !strings.Contains(rec.Body.String(), tt.want) {
				t.Errorf("error message %q missing from %s", tt.want, rec.Body.String())
			}
		})
	}
}

func TestAdmin_InfrastructureErrorsAre500WithoutDetails(t *testing.T) {
	rig := newAdminRig(t)
	rig.svc.err = errors.New("connection to 10.0.0.5 refused")

	rec := serve(rig.h.CreateUser, adminRequest(http.MethodPost, "/admin/users", url.Values{"username": {"x"}}, nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "10.0.0.5") {
		t.Error("internal error detail leaked in the response")
	}
}

func TestAdmin_NotFoundMapsTo404(t *testing.T) {
	rig := newAdminRig(t)
	rig.svc.err = admin.ErrNotFound

	rec := serve(rig.h.ResetPassword, adminRequest(http.MethodPost, "/admin/users/99/password",
		url.Values{"password": {"a-long-enough-password"}}, map[string]string{"id": "99"}))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestAdmin_SetUserActive(t *testing.T) {
	tests := []struct {
		name       string
		id, active string
		wantStatus int
		wantCall   string
		wantNotice string
	}{
		{"deactivate", "2", "false", http.StatusOK, "1|2|false", "User deactivated."},
		{"activate", "2", "true", http.StatusOK, "1|2|true", "User activated."},
		{"bad boolean", "2", "maybe", http.StatusBadRequest, "", ""},
		{"bad id", "abc", "true", http.StatusNotFound, "", ""},
		{"zero id", "0", "true", http.StatusNotFound, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rig := newAdminRig(t)
			rec := serve(rig.h.SetUserActive, adminRequest(http.MethodPost, "/admin/users/"+tt.id+"/active",
				url.Values{"active": {tt.active}}, map[string]string{"id": tt.id}))

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if tt.wantCall == "" {
				if len(rig.svc.calls.active) != 0 {
					t.Errorf("service called for a bad request: %v", rig.svc.calls.active)
				}
				return
			}
			if got := rig.svc.calls.active; len(got) != 1 || got[0] != tt.wantCall {
				t.Errorf("service call = %v, want %q (actor|user|active)", got, tt.wantCall)
			}
			if !strings.Contains(rec.Body.String(), tt.wantNotice) {
				t.Errorf("notice %q missing", tt.wantNotice)
			}
		})
	}
}

func TestAdmin_SelfDeactivationShowsMessage(t *testing.T) {
	rig := newAdminRig(t)
	rig.svc.err = admin.ErrSelfDeactivate

	rec := serve(rig.h.SetUserActive, adminRequest(http.MethodPost, "/admin/users/1/active",
		url.Values{"active": {"false"}}, map[string]string{"id": "1"}))

	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "cannot deactivate your own account") {
		t.Errorf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestAdmin_ResetPassword(t *testing.T) {
	rig := newAdminRig(t)
	rec := serve(rig.h.ResetPassword, adminRequest(http.MethodPost, "/admin/users/2/password",
		url.Values{"password": {"brand-new-password"}}, map[string]string{"id": "2"}))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := rig.svc.calls.reset; len(got) != 1 || got[0] != "2|brand-new-password" {
		t.Errorf("reset calls = %v", got)
	}
	if strings.Contains(rec.Body.String(), "brand-new-password") {
		t.Error("the new password was echoed back")
	}
}

func TestAdmin_GroupsPageAndCreateAndDelete(t *testing.T) {
	rig := newAdminRig(t)
	rig.svc.groups = []admin.Group{{ID: 4, Name: "analysts", MemberCount: 3}}

	rec := serve(rig.h.GroupsPage, adminRequest(http.MethodGet, "/admin/groups", nil, nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `href="/admin/groups/4"`) {
		t.Fatalf("groups page: status = %d", rec.Code)
	}

	rec = serve(rig.h.CreateGroup, adminRequest(http.MethodPost, "/admin/groups", url.Values{"name": {"devs"}}, nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Group created.") {
		t.Errorf("create: status = %d", rec.Code)
	}
	if got := rig.svc.calls.groupsMade; len(got) != 1 || got[0] != "devs" {
		t.Errorf("groups created = %v", got)
	}

	rig.svc.err = admin.ErrGroupNameTaken
	rec = serve(rig.h.CreateGroup, adminRequest(http.MethodPost, "/admin/groups", url.Values{"name": {"devs"}}, nil))
	if !strings.Contains(rec.Body.String(), "already exists") {
		t.Error("duplicate group name message missing")
	}

	rig.svc.err = nil
	rec = serve(rig.h.DeleteGroup, adminRequest(http.MethodPost, "/admin/groups/4/delete", nil, map[string]string{"id": "4"}))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Group deleted.") {
		t.Errorf("delete: status = %d", rec.Code)
	}
	if got := rig.svc.calls.groupsGone; len(got) != 1 || got[0] != 4 {
		t.Errorf("groups deleted = %v", got)
	}
}

func TestAdmin_GroupNamesAreEscaped(t *testing.T) {
	rig := newAdminRig(t)
	evil := `<img src=x onerror=alert(1)>`
	rig.svc.groups = []admin.Group{{ID: 4, Name: evil}}
	rig.svc.group = admin.Group{ID: 4, Name: evil}
	rig.svc.members = []admin.User{{ID: 2, Username: `<b>bob</b>`, IsActive: true}}
	rig.svc.grants = []admin.Grant{{DatabaseID: 9, DatabaseName: `<i>db</i>`, Level: access.LevelView}}

	pages := map[string]*httptest.ResponseRecorder{
		"groups": serve(rig.h.GroupsPage, adminRequest(http.MethodGet, "/admin/groups", nil, nil)),
		"group":  serve(rig.h.GroupPage, adminRequest(http.MethodGet, "/admin/groups/4", nil, map[string]string{"id": "4"})),
	}
	for name, rec := range pages {
		body := rec.Body.String()
		for _, raw := range []string{evil, "<b>bob</b>", "<i>db</i>"} {
			if strings.Contains(body, raw) {
				t.Errorf("%s page contains unescaped %q", name, raw)
			}
		}
	}
	if !strings.Contains(pages["group"].Body.String(), "&lt;img src=x onerror=alert(1)&gt;") {
		t.Error("the group name should appear escaped")
	}
}

func TestAdmin_GroupPage(t *testing.T) {
	rig := newAdminRig(t)
	rig.svc.group = admin.Group{ID: 4, Name: "analysts"}
	rig.svc.members = []admin.User{{ID: 2, Username: "bob", IsActive: true}}
	rig.svc.candidates = []admin.User{{ID: 3, Username: "carol", IsActive: true}}
	rig.svc.grants = []admin.Grant{{DatabaseID: 9, DatabaseName: "sales", Level: access.LevelEdit}}
	rig.svc.databases = []admin.Database{{ID: 9, Name: "sales"}, {ID: 10, Name: "hr"}}

	rec := serve(rig.h.GroupPage, adminRequest(http.MethodGet, "/admin/groups/4", nil, map[string]string{"id": "4"}))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"analysts", "bob", "carol", "sales", "View + Edit", "hr",
		`hx-post="/admin/groups/4/members/2/remove"`,
		`hx-post="/admin/groups/4/grants/9/remove"`,
		`hx-post="/admin/groups/4/members"`,
		`hx-post="/admin/groups/4/grants"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page missing %q", want)
		}
	}
}

func TestAdmin_GroupPage_UnknownOrBadID(t *testing.T) {
	rig := newAdminRig(t)
	rig.svc.getErr = admin.ErrNotFound

	rec := serve(rig.h.GroupPage, adminRequest(http.MethodGet, "/admin/groups/77", nil, map[string]string{"id": "77"}))
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown group: status = %d, want 404", rec.Code)
	}

	rec = serve(rig.h.GroupPage, adminRequest(http.MethodGet, "/admin/groups/x", nil, map[string]string{"id": "x"}))
	if rec.Code != http.StatusNotFound {
		t.Errorf("bad id: status = %d, want 404", rec.Code)
	}
}

func TestAdmin_Members(t *testing.T) {
	rig := newAdminRig(t)
	rig.svc.members = []admin.User{{ID: 3, Username: "carol", IsActive: true}}
	path := map[string]string{"id": "4"}

	rec := serve(rig.h.AddMember, adminRequest(http.MethodPost, "/admin/groups/4/members", url.Values{"user_id": {"3"}}, path))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Member added.") {
		t.Errorf("add: status = %d", rec.Code)
	}
	if got := rig.svc.calls.added; len(got) != 1 || got[0] != "1|4|3" {
		t.Errorf("add calls = %v, want actor|group|user", got)
	}
	if strings.Contains(rec.Body.String(), "<html") {
		t.Error("members fragment must not include the page layout")
	}

	rec = serve(rig.h.AddMember, adminRequest(http.MethodPost, "/admin/groups/4/members", url.Values{"user_id": {""}}, path))
	if !strings.Contains(rec.Body.String(), "Choose a user.") || len(rig.svc.calls.added) != 1 {
		t.Error("a missing user must show a message without calling the service")
	}

	rec = serve(rig.h.RemoveMember, adminRequest(http.MethodPost, "/admin/groups/4/members/3/remove", nil,
		map[string]string{"id": "4", "userID": "3"}))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Member removed.") {
		t.Errorf("remove: status = %d", rec.Code)
	}
	if got := rig.svc.calls.removed; len(got) != 1 || got[0] != "1|4|3" {
		t.Errorf("remove calls = %v", got)
	}
}

func TestAdmin_Grants(t *testing.T) {
	rig := newAdminRig(t)
	rig.svc.databases = []admin.Database{{ID: 9, Name: "sales"}}
	path := map[string]string{"id": "4"}

	rec := serve(rig.h.SetGrant, adminRequest(http.MethodPost, "/admin/groups/4/grants",
		url.Values{"database_id": {"9"}, "level": {"edit"}}, path))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Access saved.") {
		t.Errorf("grant: status = %d", rec.Code)
	}
	if got := rig.svc.calls.grants; len(got) != 1 || got[0] != "1|4|9|edit" {
		t.Errorf("grant calls = %v, want actor|group|db|level", got)
	}

	for name, form := range map[string]url.Values{
		"no database":   {"level": {"view"}},
		"bad database":  {"database_id": {"abc"}, "level": {"view"}},
		"no level":      {"database_id": {"9"}},
		"unknown level": {"database_id": {"9"}, "level": {"owner"}},
		"none level":    {"database_id": {"9"}, "level": {"none"}},
	} {
		t.Run(name, func(t *testing.T) {
			before := len(rig.svc.calls.grants)
			rec := serve(rig.h.SetGrant, adminRequest(http.MethodPost, "/admin/groups/4/grants", form, path))

			if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `class="error"`) {
				t.Errorf("status = %d, want an inline error", rec.Code)
			}
			if len(rig.svc.calls.grants) != before {
				t.Error("an invalid grant reached the service")
			}
		})
	}

	rec = serve(rig.h.RemoveGrant, adminRequest(http.MethodPost, "/admin/groups/4/grants/9/remove", nil,
		map[string]string{"id": "4", "dbID": "9"}))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Access removed.") {
		t.Errorf("remove: status = %d", rec.Code)
	}
	if got := rig.svc.calls.ungranted; len(got) != 1 || got[0] != "1|4|9" {
		t.Errorf("remove calls = %v", got)
	}
}

func TestAdmin_ActorComesFromTheSessionNotTheForm(t *testing.T) {
	rig := newAdminRig(t)

	serve(rig.h.SetUserActive, adminRequest(http.MethodPost, "/admin/users/2/active",
		url.Values{"active": {"false"}, "actor": {"2"}, "actor_id": {"2"}}, map[string]string{"id": "2"}))

	if got := rig.svc.calls.active; len(got) != 1 || !strings.HasPrefix(got[0], "1|") {
		t.Errorf("calls = %v; the actor must be the signed-in admin (1), never a form value", got)
	}
}

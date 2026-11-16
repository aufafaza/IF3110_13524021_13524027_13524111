package access_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/access"
)

func TestDatabaseAccessHTTPResponses(t *testing.T) {
	for _, tt := range []struct {
		name   string
		user   *access.User
		id     string
		level  access.Level
		err    error
		status int
	}{
		{"anonymous", nil, "10", access.LevelEdit, nil, http.StatusUnauthorized},
		{"zero user", &access.User{}, "10", access.LevelEdit, nil, http.StatusUnauthorized},
		{"invalid database", member, "bad", access.LevelEdit, nil, http.StatusBadRequest},
		{"zero database", member, "0", access.LevelEdit, nil, http.StatusBadRequest},
		{"negative database", member, "-1", access.LevelEdit, nil, http.StatusBadRequest},
		{"missing database", member, "", access.LevelEdit, nil, http.StatusBadRequest},
		{"no grant", member, "10", access.LevelNone, nil, http.StatusForbidden},
		{"view cannot edit", member, "10", access.LevelView, nil, http.StatusForbidden},
		{"admin needs grant", admin, "10", access.LevelNone, nil, http.StatusForbidden},
		{"checker failure", member, "10", access.LevelEdit, errors.New("database unavailable"), http.StatusInternalServerError},
		{"edit allowed", member, "10", access.LevelEdit, nil, http.StatusOK},
	} {
		t.Run(tt.name, func(t *testing.T) {
			checker := &fakeChecker{level: tt.level, err: tt.err}
			got := run(access.RequireDBAccess(checker, access.LevelEdit), tt.user, tt.id)
			if got.status != tt.status || got.called != (tt.status == http.StatusOK) || got.loc != "" {
				t.Fatalf("got %+v, want status %d without redirect", got, tt.status)
			}
		})
	}
}

func TestDatabaseAccessUsesContextUser(t *testing.T) {
	checker := &fakeChecker{level: access.LevelView}
	got := run(access.RequireDBAccess(checker, access.LevelView), member, "10")
	if got.status != http.StatusOK || !got.called || checker.gotUser != member.ID || checker.gotDB != 10 {
		t.Fatalf("got %+v, checked user %d database %d", got, checker.gotUser, checker.gotDB)
	}
}

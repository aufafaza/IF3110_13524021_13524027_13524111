package admin_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/access"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/admin"
)

type fakeStore struct {
	admin.Store

	createdUsers []createdUser
	createErr    error
	setActiveErr error
	setHashErr   error
	setHashFor   int64
	setHash      string

	members      map[int64][]int64
	addErr       error
	removeErr    error
	setGrantErr  error
	removeGrErr  error
	deleteErr    error
	memberIDsErr error

	deletedGroups []int64
	setGrants     []grantCall
	removedGrants []grantCall
	activeCalls   []activeCall
	order         []string
}

type createdUser struct {
	username, hash string
	admin          bool
}

type activeCall struct {
	userID int64
	active bool
}

type grantCall struct {
	groupID, databaseID int64
	level               access.Level
}

func (f *fakeStore) CreateUser(_ context.Context, username, hash string, isAdmin bool) (int64, error) {
	if f.createErr != nil {
		return 0, f.createErr
	}
	f.createdUsers = append(f.createdUsers, createdUser{username, hash, isAdmin})
	return int64(len(f.createdUsers)), nil
}

func (f *fakeStore) SetUserActive(_ context.Context, userID int64, active bool) error {
	f.order = append(f.order, "store.setactive")
	f.activeCalls = append(f.activeCalls, activeCall{userID, active})
	return f.setActiveErr
}

func (f *fakeStore) SetPasswordHash(_ context.Context, userID int64, hash string) error {
	f.setHashFor, f.setHash = userID, hash
	return f.setHashErr
}

func (f *fakeStore) CreateGroup(_ context.Context, name string) (int64, error) {
	return 1, f.createErr
}

func (f *fakeStore) DeleteGroup(_ context.Context, groupID int64) error {
	f.order = append(f.order, "store.deletegroup")
	f.deletedGroups = append(f.deletedGroups, groupID)
	return f.deleteErr
}

func (f *fakeStore) MemberIDs(_ context.Context, groupID int64) ([]int64, error) {
	f.order = append(f.order, "store.memberids")
	return f.members[groupID], f.memberIDsErr
}

func (f *fakeStore) AddMember(context.Context, int64, int64) error    { return f.addErr }
func (f *fakeStore) RemoveMember(context.Context, int64, int64) error { return f.removeErr }

func (f *fakeStore) SetGrant(_ context.Context, g, d int64, l access.Level) error {
	f.setGrants = append(f.setGrants, grantCall{g, d, l})
	return f.setGrantErr
}

func (f *fakeStore) RemoveGrant(_ context.Context, g, d int64) error {
	f.removedGrants = append(f.removedGrants, grantCall{groupID: g, databaseID: d})
	return f.removeGrErr
}

type fakeHasher struct{ err error }

func (h fakeHasher) Hash(password string) (string, error) { return "hashed:" + password, h.err }
func (h fakeHasher) Verify(string, string) (bool, error)  { return false, nil }

type fakeRevoker struct {
	revoked []int64
	err     error
	order   *[]string
}

func (r *fakeRevoker) RevokeAllForUser(_ context.Context, userID int64) error {
	if r.order != nil {
		*r.order = append(*r.order, "revoke")
	}
	r.revoked = append(r.revoked, userID)
	return r.err
}

func newService(store *fakeStore) (*admin.Service, *fakeRevoker) {
	rev := &fakeRevoker{order: &store.order}
	return admin.NewService(store, fakeHasher{}, rev), rev
}

func wantValidation(t *testing.T, err error) *admin.ValidationError {
	t.Helper()
	var ve *admin.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("want a ValidationError, got %v", err)
	}
	return ve
}

const goodPassword = "correct horse battery"

func TestCreateUser_Success(t *testing.T) {
	store := &fakeStore{}
	svc, _ := newService(store)

	if err := svc.CreateUser(context.Background(), "  alice.b  ", goodPassword, true); err != nil {
		t.Fatal(err)
	}
	if len(store.createdUsers) != 1 {
		t.Fatalf("created %d users, want 1", len(store.createdUsers))
	}
	got := store.createdUsers[0]
	if got.username != "alice.b" {
		t.Errorf("username = %q, want trimmed alice.b", got.username)
	}
	if got.hash != "hashed:"+goodPassword {
		t.Errorf("stored %q; the password must be hashed before storing", got.hash)
	}
	if !got.admin {
		t.Error("platform admin flag was not passed through")
	}
}

func TestCreateUser_Validation(t *testing.T) {
	tests := []struct {
		name, username, password string
	}{
		{"username too short", "ab", goodPassword},
		{"username too long", strings.Repeat("a", 65), goodPassword},
		{"username with space", "alice bob", goodPassword},
		{"username with angle bracket", "<script>", goodPassword},
		{"empty username", "   ", goodPassword},
		{"password too short", "alice", "short"},
		{"password too long", "alice", strings.Repeat("p", 129)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeStore{}
			svc, _ := newService(store)

			wantValidation(t, svc.CreateUser(context.Background(), tt.username, tt.password, false))
			if len(store.createdUsers) != 0 {
				t.Error("user was stored despite failing validation")
			}
		})
	}
}

func TestCreateUser_StoreErrorsPassThrough(t *testing.T) {
	store := &fakeStore{createErr: admin.ErrUsernameTaken}
	svc, _ := newService(store)

	err := svc.CreateUser(context.Background(), "alice", goodPassword, false)
	if !errors.Is(err, admin.ErrUsernameTaken) {
		t.Fatalf("got %v, want ErrUsernameTaken", err)
	}
}

func TestSetUserActive_DeactivateRevokesSessions(t *testing.T) {
	store := &fakeStore{}
	svc, rev := newService(store)

	if err := svc.SetUserActive(context.Background(), 1, 2, false); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(rev.revoked, []int64{2}) {
		t.Errorf("revoked = %v, want [2]", rev.revoked)
	}
	if !slices.Equal(store.order, []string{"store.setactive", "revoke"}) {
		t.Errorf("order = %v, want the DB change before the revoke", store.order)
	}
}

func TestSetUserActive_ActivateDoesNotRevoke(t *testing.T) {
	store := &fakeStore{}
	svc, rev := newService(store)

	if err := svc.SetUserActive(context.Background(), 1, 2, true); err != nil {
		t.Fatal(err)
	}
	if len(rev.revoked) != 0 {
		t.Errorf("revoked = %v, want none", rev.revoked)
	}
}

func TestSetUserActive_CannotDeactivateSelf(t *testing.T) {
	store := &fakeStore{}
	svc, rev := newService(store)

	err := svc.SetUserActive(context.Background(), 5, 5, false)
	if !errors.Is(err, admin.ErrSelfDeactivate) {
		t.Fatalf("got %v, want ErrSelfDeactivate", err)
	}
	if len(store.activeCalls) != 0 || len(rev.revoked) != 0 {
		t.Error("self-deactivation reached the store or revoked sessions")
	}
}

func TestSetUserActive_RevokeFailureIsReported(t *testing.T) {
	store := &fakeStore{}
	svc, rev := newService(store)
	rev.err = errors.New("db down")

	err := svc.SetUserActive(context.Background(), 1, 2, false)
	if !errors.Is(err, rev.err) {
		t.Fatalf("revoke failure swallowed: %v", err)
	}
}

func TestResetPassword(t *testing.T) {
	store := &fakeStore{}
	svc, rev := newService(store)

	if err := svc.ResetPassword(context.Background(), 9, goodPassword); err != nil {
		t.Fatal(err)
	}
	if store.setHashFor != 9 || store.setHash != "hashed:"+goodPassword {
		t.Errorf("stored hash for %d = %q", store.setHashFor, store.setHash)
	}
	if !slices.Equal(rev.revoked, []int64{9}) {
		t.Errorf("revoked = %v, want [9]; old sessions must not survive a reset", rev.revoked)
	}
}

func TestResetPassword_Validation(t *testing.T) {
	store := &fakeStore{}
	svc, rev := newService(store)

	wantValidation(t, svc.ResetPassword(context.Background(), 9, "short"))
	if store.setHash != "" || len(rev.revoked) != 0 {
		t.Error("an invalid password changed state")
	}
}

func TestResetPassword_NotFoundDoesNotRevoke(t *testing.T) {
	store := &fakeStore{setHashErr: admin.ErrNotFound}
	svc, rev := newService(store)

	err := svc.ResetPassword(context.Background(), 404, goodPassword)
	if !errors.Is(err, admin.ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
	if len(rev.revoked) != 0 {
		t.Error("sessions revoked for a user that does not exist")
	}
}

func TestCreateGroup(t *testing.T) {
	svc, _ := newService(&fakeStore{})
	if err := svc.CreateGroup(context.Background(), "  analysts "); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"", "   ", strings.Repeat("g", 65), "bad\x00name"} {
		wantValidation(t, svc.CreateGroup(context.Background(), name))
	}
}

func TestAddAndRemoveMember_RevokeTheAffectedUser(t *testing.T) {
	tests := []struct {
		name string
		call func(*admin.Service) error
	}{
		{"add", func(s *admin.Service) error { return s.AddMember(context.Background(), 1, 10, 2) }},
		{"remove", func(s *admin.Service) error { return s.RemoveMember(context.Background(), 1, 10, 2) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, rev := newService(&fakeStore{})
			if err := tt.call(svc); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(rev.revoked, []int64{2}) {
				t.Errorf("revoked = %v, want [2]", rev.revoked)
			}
		})
	}
}

func TestMembership_ActorsOwnSessionIsKept(t *testing.T) {
	svc, rev := newService(&fakeStore{})

	if err := svc.AddMember(context.Background(), 7, 10, 7); err != nil {
		t.Fatal(err)
	}
	if len(rev.revoked) != 0 {
		t.Errorf("revoked = %v; an admin editing their own membership must stay signed in", rev.revoked)
	}
}

func TestMembership_StoreFailureDoesNotRevoke(t *testing.T) {
	store := &fakeStore{addErr: admin.ErrNotFound}
	svc, rev := newService(store)

	err := svc.AddMember(context.Background(), 1, 10, 2)
	if !errors.Is(err, admin.ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
	if len(rev.revoked) != 0 {
		t.Error("sessions revoked although nothing changed")
	}
}

func TestSetGrant(t *testing.T) {
	store := &fakeStore{members: map[int64][]int64{10: {2, 3, 7}}}
	svc, rev := newService(store)

	if err := svc.SetGrant(context.Background(), 7, 10, 55, access.LevelEdit); err != nil {
		t.Fatal(err)
	}
	if len(store.setGrants) != 1 || store.setGrants[0] != (grantCall{10, 55, access.LevelEdit}) {
		t.Errorf("setGrants = %+v", store.setGrants)
	}
	if !slices.Equal(rev.revoked, []int64{2, 3}) {
		t.Errorf("revoked = %v, want members 2 and 3 but not the acting admin 7", rev.revoked)
	}
}

func TestSetGrant_RejectsNoneLevel(t *testing.T) {
	store := &fakeStore{}
	svc, _ := newService(store)

	wantValidation(t, svc.SetGrant(context.Background(), 1, 10, 55, access.LevelNone))
	if len(store.setGrants) != 0 {
		t.Error("a LevelNone grant reached the store")
	}
}

func TestRemoveGrant_RevokesMembers(t *testing.T) {
	store := &fakeStore{members: map[int64][]int64{10: {2, 3}}}
	svc, rev := newService(store)

	if err := svc.RemoveGrant(context.Background(), 1, 10, 55); err != nil {
		t.Fatal(err)
	}
	if len(store.removedGrants) != 1 {
		t.Fatalf("removedGrants = %+v", store.removedGrants)
	}
	if !slices.Equal(rev.revoked, []int64{2, 3}) {
		t.Errorf("revoked = %v, want [2 3]", rev.revoked)
	}
}

func TestDeleteGroup_RevokesMembersCapturedBeforeDeletion(t *testing.T) {
	store := &fakeStore{members: map[int64][]int64{10: {2, 3}}}
	svc, rev := newService(store)

	if err := svc.DeleteGroup(context.Background(), 1, 10); err != nil {
		t.Fatal(err)
	}
	wantOrder := []string{"store.memberids", "store.deletegroup", "revoke", "revoke"}
	if !slices.Equal(store.order, wantOrder) {
		t.Errorf("order = %v, want %v (members must be read before the cascade removes them)", store.order, wantOrder)
	}
	if !slices.Equal(rev.revoked, []int64{2, 3}) {
		t.Errorf("revoked = %v, want [2 3]", rev.revoked)
	}
}

func TestDeleteGroup_NotFoundDoesNotRevoke(t *testing.T) {
	store := &fakeStore{members: map[int64][]int64{10: {2}}, deleteErr: admin.ErrNotFound}
	svc, rev := newService(store)

	err := svc.DeleteGroup(context.Background(), 1, 10)
	if !errors.Is(err, admin.ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
	if len(rev.revoked) != 0 {
		t.Error("sessions revoked although the group was not deleted")
	}
}

package auth_test

import (
	"context"
	"errors"
	"testing"

	// "github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/access"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/auth"
)

type fakeStore struct {
	users map[string]auth.StoredUser
	err   error
}

func (f *fakeStore) FindByUsername(ctx context.Context, name string) (auth.StoredUser, error) {
	if f.err != nil {
		return auth.StoredUser{}, f.err
	}

	u, ok := f.users[name]
	if !ok {
		return auth.StoredUser{}, auth.ErrNotFound
	}
	return u, nil
}


func (f *fakeStore) FindByID(context.Context, int64) (auth.StoredUser, error) { panic("unused") }
func (f *fakeStore) Create(context.Context, auth.CreateUserParams) (int64, error)      { panic("unused") }

type fakeHasher struct {
	verifyCalls int
}

func (h *fakeHasher) Hash(pw string) (string, error) { return "hash:" + pw, nil }

func (h *fakeHasher) Verify(pw, enc string) (bool, error) {
	h.verifyCalls++
	return enc == "hash:"+pw, nil
}

func newService(t *testing.T) (*auth.Service, *fakeHasher) {
	t.Helper()
	store := &fakeStore{users: map[string]auth.StoredUser{
		"alice":    {ID: 1, Username: "alice", PasswordHash: "hash:secret", IsActive: true},
		"inactive": {ID: 2, Username: "inactive", PasswordHash: "hash:secret", IsActive: false},
		"admin":    {ID: 3, Username: "admin", PasswordHash: "hash:secret", IsActive: true, IsPlatformAdmin: true},
	}}
	h := &fakeHasher{}
	svc, err := auth.NewService(store, h)
	if err != nil {
		t.Fatal(err)
	}
	return svc, h
}

func TestLoginSuccess(t *testing.T){ 
	svc, _ := newService(t) 
	u, err := svc.Login(context.Background(), "admin", "secret") 
	if err != nil { 
		t.Fatal(err) 
	} 
	if u.ID != 3 || u.Username != "admin" || !u.IsPlatformAdmin { 
t.Errorf("undexpected user: %+v", u)
	}
} 

func TestLoginFalsePassword(t *testing.T){ 
	svc, _ := newService(t) 
	_, err := svc.Login(context.Background(), "alice", "notsecret") 
	if !errors.Is(err, auth.ErrInvalidCredentials){ 
			t.Fatalf("unexpected error, got %v", err) 
	} 
}

func TestLoginUnknownUser(t *testing.T){ 
	svc, h := newService(t) 
	svc.Login(context.Background(), "alice", "notsecret") 
	if h.verifyCalls != 1 { 
		t.Errorf("verifyCalls = %d, want 1", h.verifyCalls)
	}
} 

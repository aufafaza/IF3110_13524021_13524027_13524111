package admin

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/access"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/auth"
)

const (
	minUsernameLen = 3
	maxUsernameLen = 64
	minPasswordLen = 12
	maxPasswordLen = 128
	maxGroupName   = 64
)

var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

type SessionRevoker interface {
	RevokeAllForUser(ctx context.Context, userID int64) error
}

type Service struct {
	store    Store
	hasher   auth.PasswordHasher
	sessions SessionRevoker
}

func NewService(store Store, hasher auth.PasswordHasher, sessions SessionRevoker) *Service {
	return &Service{store: store, hasher: hasher, sessions: sessions}
}

func invalid(format string, args ...any) error {
	return &ValidationError{Msg: fmt.Sprintf(format, args...)}
}

func validateUsername(username string) error {
	if n := utf8.RuneCountInString(username); n < minUsernameLen || n > maxUsernameLen {
		return invalid("Username must be %d to %d characters.", minUsernameLen, maxUsernameLen)
	}
	if !usernamePattern.MatchString(username) {
		return invalid("Username may only contain letters, digits, dot, dash and underscore.")
	}
	return nil
}

func validatePassword(password string) error {
	if len(password) < minPasswordLen || len(password) > maxPasswordLen {
		return invalid("Password must be %d to %d characters.", minPasswordLen, maxPasswordLen)
	}
	return nil
}

func validateGroupName(name string) error {
	if name == "" || utf8.RuneCountInString(name) > maxGroupName {
		return invalid("Group name must be 1 to %d characters.", maxGroupName)
	}
	if strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return invalid("Group name must not contain control characters.")
	}
	return nil
}

func (s *Service) revoke(ctx context.Context, actorID int64, userIDs ...int64) error {
	for _, id := range userIDs {
		if id == actorID {
			continue
		}
		if err := s.sessions.RevokeAllForUser(ctx, id); err != nil {
			return fmt.Errorf("admin: revoke sessions of user %d: %w", id, err)
		}
	}
	return nil
}

func (s *Service) ListUsers(ctx context.Context) ([]User, error) {
	return s.store.ListUsers(ctx)
}

func (s *Service) CreateUser(ctx context.Context, username, password string, isPlatformAdmin bool) error {
	username = strings.TrimSpace(username)
	if err := validateUsername(username); err != nil {
		return err
	}
	if err := validatePassword(password); err != nil {
		return err
	}
	hash, err := s.hasher.Hash(password)
	if err != nil {
		return fmt.Errorf("admin: create user: %w", err)
	}
	if _, err := s.store.CreateUser(ctx, username, hash, isPlatformAdmin); err != nil {
		return err
	}
	return nil
}

func (s *Service) SetUserActive(ctx context.Context, actorID, userID int64, active bool) error {
	if !active && actorID == userID {
		return ErrSelfDeactivate
	}
	if err := s.store.SetUserActive(ctx, userID, active); err != nil {
		return err
	}
	if !active {
		return s.revoke(ctx, actorID, userID)
	}
	return nil
}

func (s *Service) ResetPassword(ctx context.Context, userID int64, newPassword string) error {
	if err := validatePassword(newPassword); err != nil {
		return err
	}
	hash, err := s.hasher.Hash(newPassword)
	if err != nil {
		return fmt.Errorf("admin: reset password: %w", err)
	}
	if err := s.store.SetPasswordHash(ctx, userID, hash); err != nil {
		return err
	}
	return s.revoke(ctx, 0, userID)
}

func (s *Service) ListGroups(ctx context.Context) ([]Group, error) {
	return s.store.ListGroups(ctx)
}

func (s *Service) GetGroup(ctx context.Context, groupID int64) (Group, error) {
	return s.store.GetGroup(ctx, groupID)
}

func (s *Service) CreateGroup(ctx context.Context, name string) error {
	name = strings.TrimSpace(name)
	if err := validateGroupName(name); err != nil {
		return err
	}
	_, err := s.store.CreateGroup(ctx, name)
	return err
}

func (s *Service) DeleteGroup(ctx context.Context, actorID, groupID int64) error {
	members, err := s.store.MemberIDs(ctx, groupID)
	if err != nil {
		return err
	}
	if err := s.store.DeleteGroup(ctx, groupID); err != nil {
		return err
	}
	return s.revoke(ctx, actorID, members...)
}

func (s *Service) ListMembers(ctx context.Context, groupID int64) ([]User, error) {
	return s.store.ListMembers(ctx, groupID)
}

func (s *Service) ListNonMembers(ctx context.Context, groupID int64) ([]User, error) {
	return s.store.ListNonMembers(ctx, groupID)
}

func (s *Service) AddMember(ctx context.Context, actorID, groupID, userID int64) error {
	if err := s.store.AddMember(ctx, groupID, userID); err != nil {
		return err
	}
	return s.revoke(ctx, actorID, userID)
}

func (s *Service) RemoveMember(ctx context.Context, actorID, groupID, userID int64) error {
	if err := s.store.RemoveMember(ctx, groupID, userID); err != nil {
		return err
	}
	return s.revoke(ctx, actorID, userID)
}

func (s *Service) ListDatabases(ctx context.Context) ([]Database, error) {
	return s.store.ListDatabases(ctx)
}

func (s *Service) ListGrants(ctx context.Context, groupID int64) ([]Grant, error) {
	return s.store.ListGrants(ctx, groupID)
}

func (s *Service) SetGrant(ctx context.Context, actorID, groupID, databaseID int64, level access.Level) error {
	if level != access.LevelView && level != access.LevelEdit {
		return invalid("Choose an access level.")
	}
	if err := s.store.SetGrant(ctx, groupID, databaseID, level); err != nil {
		return err
	}
	return s.revokeGroupMembers(ctx, actorID, groupID)
}

func (s *Service) RemoveGrant(ctx context.Context, actorID, groupID, databaseID int64) error {
	if err := s.store.RemoveGrant(ctx, groupID, databaseID); err != nil {
		return err
	}
	return s.revokeGroupMembers(ctx, actorID, groupID)
}

func (s *Service) revokeGroupMembers(ctx context.Context, actorID, groupID int64) error {
	members, err := s.store.MemberIDs(ctx, groupID)
	if err != nil {
		return err
	}
	return s.revoke(ctx, actorID, members...)
}

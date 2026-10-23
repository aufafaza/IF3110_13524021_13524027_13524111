package admin

import (
	"errors"
	"time"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/access"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/auth"
)

type User struct {
	ID              int64
	Username        string
	IsPlatformAdmin bool
	IsActive        bool
	CreatedAt       time.Time
}

type Group struct {
	ID          int64
	Name        string
	MemberCount int
}

type Database struct {
	ID   int64
	Name string
}

type Grant struct {
	DatabaseID   int64
	DatabaseName string
	Level        access.Level
}

var (
	ErrNotFound       = errors.New("admin: not found")
	ErrUsernameTaken  = auth.ErrUsernameTaken
	ErrGroupNameTaken = errors.New("admin: group name is taken")
	ErrSelfDeactivate = errors.New("admin: you cannot deactivate your own account")
)

type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

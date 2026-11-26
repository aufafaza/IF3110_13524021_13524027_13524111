package admin

import (
	"errors"
	"time"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/access"
	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/auth"
)

type User struct {
	ID              int64     `json:"id"`
	Username        string    `json:"username"`
	IsPlatformAdmin bool      `json:"is_platform_admin"`
	IsActive        bool      `json:"is_active"`
	CreatedAt       time.Time `json:"created_at"`
}

type Group struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	MemberCount  int    `json:"member_count"`
	LeaderUserID *int64 `json:"leader_user_id,omitempty"`
}

type Database struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type Grant struct {
	DatabaseID   int64        `json:"database_id"`
	DatabaseName string       `json:"database_name"`
	Level        access.Level `json:"level"`
}

var (
	ErrNotFound       = errors.New("admin: not found")
	ErrUsernameTaken  = auth.ErrUsernameTaken
	ErrGroupNameTaken = errors.New("admin: group name is taken")
	ErrSelfDeactivate = errors.New("admin: you cannot deactivate your own account")
)

type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

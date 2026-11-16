package access

import (
	"context"
	"errors"
)

type Level int

const (
	LevelNone Level = iota
	LevelView
	LevelEdit
)

var (
	ErrUnauthenticated = errors.New("access: not authenticated")
	ErrForbidden       = errors.New("access: forbidden")
)

type User struct {
	ID              int64
	Username        string
	IsPlatformAdmin bool
}

type DatabaseChecker interface {
	EffectiveLevel(ctx context.Context, userID, databaseID int64) (Level, error)
}

type Checker interface {
	DatabaseChecker
	IsPlatformAdmin(ctx context.Context, userID int64) (bool, error)
}

type ctxKey struct{}

func ContextWithUser(ctx context.Context, u User) context.Context {
	return context.WithValue(ctx, ctxKey{}, u)
}

func UserFromContext(ctx context.Context) (User, bool) {
	u, ok := ctx.Value(ctxKey{}).(User)
	return u, ok
}

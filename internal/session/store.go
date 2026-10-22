package session

import (
	"context"
	"errors"
	"time"
)

// pointers indicate nullable
type Session struct { 
	IDHash []byte
	PrevIDHash []byte 
	PrevValidUntil *time.Time 
	UserID		int64 
	CRSFSecret 	[]byte 
	CreatedAt time.Time 
	LastSeenAt time.Time 
	RotatedAt time.Time 
	AbsoluteExpiresAt time.Time 
	RevokedAt *time.Time 	
} 

var ( 
	ErrNotFound = errors.New("session: session not found") 	
) 


type Store interface {  
        Create(ctx context.Context, s Session) error
        GetByIDHash(ctx context.Context, hash []byte) (Session, error)
        Touch(ctx context.Context, idHash []byte, now time.Time) error
        Rotate(ctx context.Context, oldHash, newHash []byte, now, graceUntil time.Time) error
        Revoke(ctx context.Context, idHash []byte, now time.Time) error
        RevokeAllForUser(ctx context.Context, userID int64, now time.Time) error
} 

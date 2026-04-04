// Package auth handles OpenID Connect authentication,
// session management, and request authorization middleware.
package auth

import "time"

// User represents the claims extracted from a verified OIDC ID token.
// This is the canonical identity object used throughout the application.
type User struct {
	// Subject is the stable, unique identifier from the OIDC provider (the "sub" claim).
	Subject string
	// Email is the user's email address.
	Email string
	// Name is the display name from the OIDC provider.
	Name string
}

// Session is the server-side session record persisted in the database.
type Session struct {
	ID        string `gorm:"primarykey"`
	UserSub   string `gorm:"index;not null"`
	UserEmail string
	UserName  string
	ExpiresAt time.Time `gorm:"index"`
	CreatedAt time.Time
}

// IsExpired returns true when the session has passed its expiry time.
func (s *Session) IsExpired() bool {
	return time.Now().After(s.ExpiresAt)
}

// SessionDuration is how long a session remains valid after creation.
const SessionDuration = 24 * time.Hour * 7 // 7 days

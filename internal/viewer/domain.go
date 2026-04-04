// Package viewer manages shared links that give unauthenticated visitors
// access to a specific live stream or archive video.
package viewer

import (
	"time"

	"gorm.io/gorm"
)

// SharedLink is the model for a shareable URL token.
// Exactly one of StreamID or ArchiveID must be non-empty.
type SharedLink struct {
	ID string `gorm:"primarykey" json:"id"`
	// Token is the auto-generated random identifier embedded in the public URL.
	// Example: /watch/a1b2c3d4e5f6a1b2c3d4
	Token string `gorm:"uniqueIndex;not null" json:"token"`
	// Slug is an optional human-readable alias for the link.
	// When set, /watch/<slug> resolves to the same content as /watch/<token>.
	// Must be unique across all links. Example: /watch/my-stream
	Slug *string `gorm:"uniqueIndex" json:"slug,omitempty"`

	// StreamID links to a live stream (nullable).
	StreamID *string `json:"stream_id,omitempty"`
	// ArchiveID links to an archive video (nullable).
	ArchiveID *string `json:"archive_id,omitempty"`

	// PasswordHash is a bcrypt hash of the required password, or empty for public access.
	PasswordHash string `json:"-"`
	// HasPassword tells the client whether to show a password prompt.
	HasPassword bool `gorm:"-" json:"has_password"`

	// IsDefault marks the link that was automatically created when the stream
	// was first set up. Default links cannot be deleted.
	IsDefault bool `gorm:"not null;default:false" json:"is_default"`

	// CreatedBy is the OIDC subject of the user who created the link.
	CreatedBy string     `gorm:"not null" json:"-"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

// IsExpired returns true if the link has a non-nil expiry in the past.
func (l *SharedLink) IsExpired() bool {
	return l.ExpiresAt != nil && time.Now().After(*l.ExpiresAt)
}

// AfterFind is a GORM hook that populates the virtual HasPassword field.
func (l *SharedLink) AfterFind(_ *gorm.DB) error {
	l.HasPassword = l.PasswordHash != ""
	return nil
}

package viewer

import (
	"errors"
	"regexp"
	"time"

	"github.com/c0d3d3v/streamer-2/internal/infra/db"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

// ErrNotFound is returned when a link token does not exist.
var ErrNotFound = errors.New("shared link not found")

// ErrExpired is returned when a valid link has passed its expiry time.
var ErrExpired = errors.New("shared link has expired")

// ErrBadPassword is returned when the supplied password does not match.
var ErrBadPassword = errors.New("incorrect password")

// ErrSlugTaken is returned when the requested slug is already in use.
var ErrSlugTaken = errors.New("slug is already taken")

// ErrSlugInvalid is returned when the slug contains disallowed characters.
var ErrSlugInvalid = errors.New("slug may only contain letters, numbers, and hyphens")

// ErrIsDefault is returned when attempting to delete the auto-created default link.
var ErrIsDefault = errors.New("the default link cannot be deleted")

// slugPattern allows lowercase letters, digits, and hyphens (1–64 chars).
var slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9\-]{0,62}[a-z0-9]$|^[a-z0-9]$`)

// CreateInput holds the parameters for creating a shared link.
type CreateInput struct {
	StreamID  *string
	ArchiveID *string
	Slug      string     // optional vanity alias; empty means no slug
	Password  string     // plain text; will be hashed before storage
	ExpiresAt *time.Time
	CreatedBy string
	IsDefault bool // marks the automatically-created link; prevents deletion
}

// Create generates a new shared link and persists it.
func Create(input CreateInput) (*SharedLink, error) {
	token, err := randomToken()
	if err != nil {
		return nil, err
	}

	link := &SharedLink{
		ID:        uuid.NewString(),
		Token:     token,
		StreamID:  input.StreamID,
		ArchiveID: input.ArchiveID,
		CreatedBy: input.CreatedBy,
		ExpiresAt: input.ExpiresAt,
		IsDefault: input.IsDefault,
	}

	if input.Slug != "" {
		if !slugPattern.MatchString(input.Slug) {
			return nil, ErrSlugInvalid
		}
		// Check uniqueness before insert so we can return a clear error.
		var count int64
		db.DB.Model(&SharedLink{}).Where("slug = ?", input.Slug).Count(&count)
		if count > 0 {
			return nil, ErrSlugTaken
		}
		link.Slug = &input.Slug
	}

	if input.Password != "" {
		hash, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
		if err != nil {
			return nil, err
		}
		link.PasswordHash = string(hash)
	}

	if err := db.DB.Create(link).Error; err != nil {
		return nil, err
	}
	return link, nil
}

// GetByIdentifier loads a link by its token or slug.
// The token is checked first; if not found the slug is tried.
// Returns ErrNotFound or ErrExpired.
func GetByIdentifier(identifier string) (*SharedLink, error) {
	var link SharedLink
	err := db.DB.First(&link, "token = ? OR slug = ?", identifier, identifier).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if link.IsExpired() {
		return nil, ErrExpired
	}
	return &link, nil
}

// GetByToken loads a link by its URL token. Returns ErrNotFound or ErrExpired.
// Kept for backwards compatibility; prefer GetByIdentifier for new call sites.
func GetByToken(token string) (*SharedLink, error) {
	return GetByIdentifier(token)
}

// GetLinkForTarget returns the most-recently-created link by createdBy for the
// given stream or archive. Returns ErrNotFound when no link exists yet.
func GetLinkForTarget(streamID, archiveID *string, createdBy string) (*SharedLink, error) {
	var link SharedLink
	q := db.DB.Where("created_by = ?", createdBy)
	if streamID != nil {
		q = q.Where("stream_id = ?", *streamID)
	} else if archiveID != nil {
		q = q.Where("archive_id = ?", *archiveID)
	} else {
		return nil, ErrNotFound
	}
	err := q.Order("created_at desc").First(&link).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return &link, err
}

// ListByOwner returns all shared links created by a given user.
func ListByOwner(createdBy string) ([]SharedLink, error) {
	var links []SharedLink
	err := db.DB.Where("created_by = ?", createdBy).Order("created_at desc").Find(&links).Error
	return links, err
}

// ListForTarget returns all links for a specific stream or archive owned by createdBy.
// When both IDs are provided the results include links matching either (OR).
func ListForTarget(streamID, archiveID *string, createdBy string) ([]SharedLink, error) {
	q := db.DB.Where("created_by = ?", createdBy)
	switch {
	case streamID != nil && archiveID != nil:
		q = q.Where("stream_id = ? OR archive_id = ?", *streamID, *archiveID)
	case streamID != nil:
		q = q.Where("stream_id = ?", *streamID)
	case archiveID != nil:
		q = q.Where("archive_id = ?", *archiveID)
	}
	var links []SharedLink
	err := q.Order("created_at desc").Find(&links).Error
	return links, err
}

// Delete removes a shared link. Only the creator may delete it.
// Returns ErrIsDefault if the link is the auto-created default link.
func Delete(id, createdBy string) error {
	// Check if the link is the default before attempting deletion.
	var link SharedLink
	if err := db.DB.First(&link, "id = ? AND created_by = ?", id, createdBy).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrNotFound
		}
		return err
	}
	if link.IsDefault {
		return ErrIsDefault
	}
	return db.DB.Delete(&link).Error
}

// DeleteForStream removes all shared links pointing to the given stream ID.
func DeleteForStream(streamID string) error {
	return db.DB.Delete(&SharedLink{}, "stream_id = ?", streamID).Error
}

// DeleteForArchive removes all shared links pointing to the given archive ID.
func DeleteForArchive(archiveID string) error {
	return db.DB.Delete(&SharedLink{}, "archive_id = ?", archiveID).Error
}

// CheckPassword verifies a password against the stored bcrypt hash.
func CheckPassword(link *SharedLink, password string) error {
	if link.PasswordHash == "" {
		return nil // no password required
	}
	if err := bcrypt.CompareHashAndPassword([]byte(link.PasswordHash), []byte(password)); err != nil {
		return ErrBadPassword
	}
	return nil
}

// randomToken generates a URL-safe random token for shared link URLs.
func randomToken() (string, error) {
	// 16 bytes → 22-character base64 string; short enough for URLs.
	b := make([]byte, 16)
	_, err := uuid.New().MarshalBinary() // use uuid as entropy source via crypto/rand
	if err != nil {
		return "", err
	}
	copy(b, uuid.New().String()[:16])
	// Use a fresh UUID's hex representation, stripping dashes, for simplicity.
	raw := uuid.NewString()
	// Remove dashes and take 20 chars: e.g. "a1b2c3d4e5f6a1b2c3d4"
	var clean []byte
	for _, ch := range raw {
		if ch != '-' {
			clean = append(clean, byte(ch))
		}
		if len(clean) == 20 {
			break
		}
	}
	return string(clean), nil
}

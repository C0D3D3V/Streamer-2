package stream

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/c0d3d3v/streamer-2/internal/config"
	"github.com/c0d3d3v/streamer-2/internal/infra/db"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ErrNotFound is returned when a stream does not exist.
var ErrNotFound = errors.New("stream not found")

// ErrForbidden is returned when the caller does not own the stream.
var ErrForbidden = errors.New("forbidden")

// ErrInvalidTransition is returned when a status change is not allowed.
var ErrInvalidTransition = errors.New("invalid status transition")

// CreateInput holds the fields required to create a new stream.
type CreateInput struct {
	Title       string
	ScheduledAt *time.Time
	OwnerID     string
}

// Create persists a new stream record and returns it.
func Create(input CreateInput) (*Stream, error) {
	s := &Stream{
		ID:          uuid.NewString(),
		Title:       input.Title,
		ScheduledAt: input.ScheduledAt,
		OwnerID:     input.OwnerID,
		Status:      StatusScheduled,
	}
	if err := db.DB.Create(s).Error; err != nil {
		return nil, err
	}
	return s, nil
}

// GetByID loads a stream by its UUID. Returns ErrNotFound if absent.
func GetByID(id string) (*Stream, error) {
	var s Stream
	err := db.DB.First(&s, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return &s, err
}

// ListByOwner returns all streams owned by a given user, newest first.
func ListByOwner(ownerID string) ([]Stream, error) {
	var streams []Stream
	err := db.DB.Where("owner_id = ?", ownerID).Order("created_at desc").Find(&streams).Error
	return streams, err
}

// Start transitions a stream from Scheduled → Live, creates the HLS directory,
// and records the start time, tier, and aspect ratio. Returns ErrInvalidTransition if already live.
func Start(id, ownerID string, rotation int, tier Tier, aspectRatio float64) (*Stream, error) {
	if !ValidTiers[tier] {
		return nil, fmt.Errorf("invalid tier %q", tier)
	}

	s, err := GetByID(id)
	if err != nil {
		return nil, err
	}
	if s.OwnerID != ownerID {
		return nil, ErrForbidden
	}
	if s.Status != StatusScheduled {
		return nil, ErrInvalidTransition
	}

	// Create the DASH output directory for this stream.
	liveDir := filepath.Join(config.Get().App.DataDir, "dash", id)
	if err := os.MkdirAll(liveDir, 0o775); err != nil {
		return nil, fmt.Errorf("create DASH dir: %w", err)
	}

	now := time.Now()
	s.Status = StatusLive
	s.StartedAt = &now
	s.LiveDir = liveDir
	s.Rotation = rotation
	s.Tier = tier
	s.AspectRatio = aspectRatio

	if err := db.DB.Save(s).Error; err != nil {
		return nil, err
	}
	return s, nil
}

// Stop transitions a stream from Live → Ended and records the end time.
// The actual MP4 finalization is handled asynchronously by the archive package.
func Stop(id, ownerID string) (*Stream, error) {
	s, err := GetByID(id)
	if err != nil {
		return nil, err
	}
	if s.OwnerID != ownerID {
		return nil, ErrForbidden
	}
	if s.Status != StatusLive {
		return nil, ErrInvalidTransition
	}

	now := time.Now()
	s.Status = StatusEnded
	s.EndedAt = &now

	if err := db.DB.Save(s).Error; err != nil {
		return nil, err
	}
	return s, nil
}

// Delete removes the stream record. Only the owner may delete.
// HLS files are cleaned up by the caller (handler or service layer).
func Delete(id, ownerID string) error {
	s, err := GetByID(id)
	if err != nil {
		return err
	}
	if s.OwnerID != ownerID {
		return ErrForbidden
	}
	return db.DB.Delete(s).Error
}

// MarkFailed sets the stream status to Failed, e.g. when the ingest pipeline
// encounters an unrecoverable error.
func MarkFailed(id string) {
	db.DB.Model(&Stream{}).Where("id = ?", id).Update("status", StatusFailed)
}

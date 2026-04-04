// Package stream models live streams, their lifecycle, and the HLS recording pipeline.
package stream

import "time"

// Tier represents the resolution tier chosen by the streamer at go-live time.
// The tier defines the long side of the output; the short side is derived from
// the camera's native aspect ratio so no forced 16:9 cropping occurs.
type Tier string

const (
	TierQHD Tier = "QHD" // long side 2560
	TierFHD Tier = "FHD" // long side 1920
	TierHD  Tier = "HD"  // long side 1280
)

// ValidTiers is the allowlist of accepted tier values.
var ValidTiers = map[Tier]bool{
	TierQHD: true,
	TierFHD: true,
	TierHD:  true,
}

// Status represents the lifecycle state of a stream.
type Status string

const (
	// StatusScheduled means the stream has been created but not yet started.
	StatusScheduled Status = "scheduled"
	// StatusLive means the streamer is actively sending data.
	StatusLive Status = "live"
	// StatusEnded means the stream has finished and is being (or has been)
	// finalized into an archive video.
	StatusEnded Status = "ended"
	// StatusFailed means an unrecoverable error occurred during recording.
	StatusFailed Status = "failed"
)

// Stream is the entity for a live/scheduled stream.
type Stream struct {
	ID          string     `gorm:"primarykey"                 json:"id"`
	Title       string     `gorm:"not null"                   json:"title"`
	Status      Status     `gorm:"not null;default:scheduled" json:"status"`
	// Tier is the resolution tier set at go-live time (QHD/FHD/HD). Empty until the
	// stream starts; the short side is computed from AspectRatio.
	Tier        Tier       `gorm:"default:''"                 json:"tier"`
	// AspectRatio is the camera's native long/short ratio (e.g. 1.7778 for 16:9),
	// recorded at go-live time so the archive knows the exact frame dimensions.
	AspectRatio float64    `gorm:"default:0"                  json:"aspect_ratio"`
	ScheduledAt *time.Time `                                   json:"scheduled_at,omitempty"`
	StartedAt   *time.Time `                                   json:"started_at,omitempty"`
	EndedAt     *time.Time `                                   json:"ended_at,omitempty"`
	// OwnerID is the OIDC subject of the user who created the stream.
	OwnerID string `gorm:"not null;index" json:"owner_id"`
	// LiveDir is the filesystem path where HLS segments and the manifest are written.
	LiveDir string `json:"-"`
	// Rotation is the clockwise degrees the streamer applied (0, 90, 180, 270).
	// Written as rotation metadata into the fMP4 init segment and the archive MP4.
	Rotation  int       `gorm:"default:0" json:"rotation"`
	CreatedAt time.Time `                  json:"created_at"`
}

// IsLive returns true when the stream is actively broadcasting.
func (s *Stream) IsLive() bool { return s.Status == StatusLive }

// IsScheduled returns true when the stream is awaiting its start.
func (s *Stream) IsScheduled() bool { return s.Status == StatusScheduled }

// ScheduledInFuture returns true when the stream has a future scheduled time.
func (s *Stream) ScheduledInFuture() bool {
	return s.ScheduledAt != nil && time.Now().Before(*s.ScheduledAt)
}

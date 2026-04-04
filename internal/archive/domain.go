// Package archive manages completed stream recordings.
package archive

import "time"

// Archive is the GORM model for a completed stream recording.
type Archive struct {
	ID string `gorm:"primarykey" json:"id"`
	// StreamID links back to the source stream.
	StreamID string `gorm:"uniqueIndex;not null" json:"stream_id"`
	// OwnerID is the OIDC subject of the user who owns this archive.
	OwnerID string `gorm:"default:'';index"     json:"-"`
	Title   string `gorm:"not null"             json:"title"`
	// FilePath is the absolute path to the finalised video file on disk.
	FilePath      string     `json:"-"`
	FileSizeBytes int64      `json:"file_size_bytes"`
	DurationSecs  int        `json:"duration_secs"`
	FinalizedAt   *time.Time `json:"finalized_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}

package archive

import (
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/c0d3d3v/streamer-2/internal/config"
	"github.com/c0d3d3v/streamer-2/internal/infra/db"
	"github.com/c0d3d3v/streamer-2/internal/websocket"
	"github.com/google/uuid"
)

// streamInfo contains the minimum stream data needed for finalization.
// Using a struct instead of importing stream.Stream avoids circular imports.
type streamInfo struct {
	ID      string
	Title   string
	LiveDir  string
	OwnerID string
}

// Finalize concatenates all fMP4 segments in a stream's live directory into a
// single MP4 file using ffmpeg, then creates an Archive record in the database.
//
// This is called asynchronously from the stream stop handler so that the HTTP
// response is immediate and the potentially slow ffmpeg work runs in the background.
func Finalize(id, title, liveDir, ownerID string) {
	info := streamInfo{ID: id, Title: title, LiveDir: liveDir, OwnerID: ownerID}
	if err := finalize(info); err != nil {
		log.Printf("archive[%s]: finalization failed: %v", id, err)
		return
	}
}

func finalize(s streamInfo) error {
	if s.LiveDir == "" {
		return fmt.Errorf("stream %s has no live directory", s.ID)
	}

	// Collect the fMP4 init segments and all media chunk segments.
	// FFmpeg's DASH muxer writes separate streams for video and audio:
	//   init-stream0.m4s       – video init (ftyp + moov)
	//   init-stream1.m4s       – audio init (ftyp + moov)
	//   chunk-stream0-NNNNN.m4s – video fragments (moof + mdat)
	//   chunk-stream1-NNNNN.m4s – audio fragments (moof + mdat)
	//
	// We concatenate each stream separately and pass them as two inputs to
	// ffmpeg so it can remux both into a single MP4 with audio+video.
	videoChunks, err := filepath.Glob(filepath.Join(s.LiveDir, "chunk-stream0-*.m4s"))
	if err != nil {
		return fmt.Errorf("glob video segments: %w", err)
	}
	sort.Strings(videoChunks)

	audioChunks, err := filepath.Glob(filepath.Join(s.LiveDir, "chunk-stream1-*.m4s"))
	if err != nil {
		return fmt.Errorf("glob audio segments: %w", err)
	}
	sort.Strings(audioChunks)

	if len(videoChunks) == 0 {
		return fmt.Errorf("no DASH video segments found in %s", s.LiveDir)
	}

	// Build concatenated fMP4 for video (init-stream0 + all video chunks).
	tmpVideo, err := buildConcatFile(
		filepath.Join(s.LiveDir, "init-stream0.m4s"),
		videoChunks,
	)
	if err != nil {
		return fmt.Errorf("concat video: %w", err)
	}
	defer os.Remove(tmpVideo)

	// Build concatenated fMP4 for audio (init-stream1 + all audio chunks).
	// Audio is optional: if there are no audio segments we produce a silent MP4.
	var tmpAudio string
	if len(audioChunks) > 0 {
		tmpAudio, err = buildConcatFile(
			filepath.Join(s.LiveDir, "init-stream1.m4s"),
			audioChunks,
		)
		if err != nil {
			return fmt.Errorf("concat audio: %w", err)
		}
		defer os.Remove(tmpAudio)
	}

	// Create the output directory for archives.
	outDir := filepath.Join(config.Get().App.DataDir, "archives")
	if err := os.MkdirAll(outDir, 0o775); err != nil {
		return fmt.Errorf("create archive dir: %w", err)
	}

	outPath := filepath.Join(outDir, s.ID+".mp4")

	// Build the ffmpeg argument list for fMP4 → regular MP4 finalization.
	// We stream-copy both tracks (lossless, fast) and move the moov atom to
	// the front (+faststart) for progressive browser playback.
	ffArgs := []string{"-y", "-i", tmpVideo}
	if tmpAudio != "" {
		ffArgs = append(ffArgs, "-i", tmpAudio)
	}
	ffArgs = append(ffArgs,
		"-c:v", "copy",
		"-c:a", "copy",
		"-movflags", "+faststart",
		outPath,
	)

	cmd := exec.Command("ffmpeg", ffArgs...)
	cmd.Stderr = os.Stderr

	log.Printf("archive[%s]: starting MP4 finalization → %s", s.ID, outPath)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ffmpeg remux: %w", err)
	}

	// Get file stats for the archive record.
	stat, err := os.Stat(outPath)
	if err != nil {
		return fmt.Errorf("stat output file: %w", err)
	}

	// Probe duration with ffprobe (best-effort; 0 if ffprobe is unavailable).
	duration := probeDuration(outPath)

	now := time.Now()
	archive := &Archive{
		ID:            uuid.NewString(),
		StreamID:      s.ID,
		OwnerID:       s.OwnerID,
		Title:         s.Title,
		FilePath:      outPath,
		FileSizeBytes: stat.Size(),
		DurationSecs:  duration,
		FinalizedAt:   &now,
	}
	if err := db.DB.Create(archive).Error; err != nil {
		return fmt.Errorf("save archive record: %w", err)
	}

	log.Printf("archive[%s]: finalized, size=%d bytes, duration=%ds", s.ID, stat.Size(), duration)

	// Notify viewers that the archive is ready.
	websocket.Hub.Broadcast(s.ID, websocket.Message{
		Type:    "stream.ended",
		Payload: map[string]string{"archive_id": archive.ID},
	})

	// Optionally clean up the raw DASH segments to save disk space.
	if !config.Get().App.KeepSegmentsAfterFinalization {
		if err := os.RemoveAll(s.LiveDir); err != nil {
			log.Printf("archive[%s]: warning: failed to remove live dir: %v", s.ID, err)
		}
	}

	return nil
}

// buildConcatFile concatenates initPath + chunks into a single temp fMP4 file
// and returns its path. The caller is responsible for removing the temp file.
func buildConcatFile(initPath string, chunks []string) (string, error) {
	tmp, err := os.CreateTemp("", "streamer-archive-*.m4s")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()

	for _, path := range append([]string{initPath}, chunks...) {
		f, err := os.Open(path)
		if err != nil {
			tmp.Close()
			os.Remove(tmpPath)
			return "", fmt.Errorf("open %s: %w", path, err)
		}
		_, copyErr := io.Copy(tmp, f)
		f.Close()
		if copyErr != nil {
			tmp.Close()
			os.Remove(tmpPath)
			return "", fmt.Errorf("copy %s: %w", path, copyErr)
		}
	}
	tmp.Close()
	return tmpPath, nil
}

// probeDuration uses ffprobe to get the total duration of the MP4 in seconds.
// Returns 0 if ffprobe is not installed or fails.
func probeDuration(path string) int {
	out, err := exec.Command("ffprobe",
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		path,
	).Output()
	if err != nil {
		return 0
	}
	var d float64
	fmt.Sscanf(strings.TrimSpace(string(out)), "%f", &d)
	return int(d)
}

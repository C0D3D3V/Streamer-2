// Package live manages the live-streaming ingest pipeline using CMAF fMP4.
//
// Flow:
//  1. The streamer's browser captures camera/mic via getUserMedia and encodes
//     chunks with MediaRecorder (WebM/H.264+Opus), posting each ~2-second chunk
//     to POST /api/streams/{id}/ingest.
//  2. For each active stream, this package maintains one long-running ffmpeg
//     child process. WebM bytes are piped into ffmpeg's stdin; ffmpeg outputs
//     CMAF fMP4 segments to disk.
//  3. An init segment (init-stream0.m4s) is written once at the start.
//  4. Media segments (chunk-stream0-NNNNN.m4s) are written every SegmentDuration seconds.
//  5. A DASH manifest (manifest.mpd) and an HLS master playlist (master.m3u8)
//     are both rewritten after each new segment. Both reference the same .m4s
//     files so there is no extra transcoding cost.
//  6. Viewers fetch the manifest and segments over plain HTTP.
package live

import (
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	goffmpeg "github.com/c0d3d3v/streamer-2/internal/ffmpeg"
)

// SegmentDuration is the target segment length in seconds.
const SegmentDuration = 2

// Manager runs one ffmpeg process per active stream and writes CMAF fMP4
// segments together with a DASH manifest and an HLS master playlist.
type Manager struct {
	streamID string
	mediaDir string
	// rotation is the clockwise degrees to embed as rotation metadata (0, 90, 180, 270).
	// The value is written into the fMP4 init segment's tkhd transformation matrix so
	// players automatically display the correct orientation without re-encoding.
	rotation int

	mu          sync.Mutex
	ffmpegCmd   *exec.Cmd
	ffmpegStdin io.WriteCloser
	done        chan struct{}
}

// New creates a Manager for the given stream. mediaDir must already exist.
// rotation must be 0, 90, 180, or 270; any other value is treated as 0.
func New(streamID, mediaDir string, rotation int) *Manager {
	return &Manager{
		streamID: streamID,
		mediaDir: mediaDir,
		rotation: rotation,
		done:     make(chan struct{}),
	}
}

// Start launches the ffmpeg subprocess. ffmpeg reads WebM from stdin and writes
// CMAF fMP4 segments plus DASH and HLS manifests to disk.
//
// FFmpeg DASH muxer options used:
//
//	-seg_duration 2      – 2-second segments (lower latency, faster error recovery)
//	-window_size 0       – keep ALL segments (required for archive finalization)
//	-streaming 1         – update manifests after each segment (live mode)
//	-use_template 1      – SegmentTemplate for compact MPD
//	-use_timeline 1      – SegmentTimeline for robust variable-duration handling
//	-index_correction 1  – handle timestamp discontinuities from browser WebM
//	-hls_playlist 1      – also write master.m3u8 for Safari/iOS native HLS
//
// Note: -ldash is intentionally omitted. LL-DASH requires the HTTP server to
// deliver partial (in-progress) segments via chunked transfer encoding, which
// this server does not support. Using -ldash with static file serving causes
// the MPD to advertise availabilityTimeOffset attributes that DASH.js acts on
// by requesting segments before they are fully written, producing 404 errors.
func (m *Manager) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	manifestPath := filepath.Join(m.mediaDir, "manifest.mpd")

	// Build the argument list.
	// LiveDecodeArgs initialises the hardware device (if any) before the input.
	args := goffmpeg.LiveDecodeArgs()
	if m.rotation != 0 {
		// -display_rotation is an input option: it must appear before -i.
		// It takes counter-clockwise degrees, which matches our rotation value.
		args = append(args, "-display_rotation:v:0", fmt.Sprintf("%d", -m.rotation))
	}
	args = append(args,
		"-f", "webm",
		"-i", "pipe:0",
		// -r 30 is critical: the browser's WebM container uses a 1ms timebase
		// (reported as "1k tbn/tbr"). Without an explicit fps, ffmpeg infers
		// 1000 fps and duplicates thousands of frames.
		"-r", "30",
		// Force a keyframe every SegmentDuration seconds so the muxer can
		// split video at the target interval.
		"-force_key_frames", fmt.Sprintf("expr:gte(t,n_forced*%d)", SegmentDuration),
	)

	// LiveVideoArgs returns the scale filter + encoder for the active backend.
	args = append(args, goffmpeg.LiveVideoArgs()...)
	args = append(args,
		// Explicit stream mapping: include both video (stream 0) and audio
		// (stream 1). Without this the DASH muxer may silently drop audio.
		"-map", "0:v:0",
		"-map", "0:a:0",
		// Transcode audio to AAC. Opus is non-standard in fMP4 and has poor
		// player support. AAC is universally supported in MPEG-DASH and HLS.
		"-c:a", "aac",
		"-ar", "48000", // normalise sample rate (browser WebM may vary)
		"-ac", "2", // stereo
		// DASH/CMAF muxer options
		"-f", "dash",
		"-seg_duration", fmt.Sprintf("%d", SegmentDuration),
		"-window_size", "0", // unlimited: keep all segments for archiving
		"-streaming", "1", // live manifest updates after each segment
		"-use_template", "1", // SegmentTemplate (efficient MPD)
		"-use_timeline", "1", // SegmentTimeline (robust timestamp handling)
		"-index_correction", "1", // fix timestamp gaps from browser chunks
		"-hls_playlist", "1", // also emit master.m3u8 for Safari/iOS
		"-hls_master_name", "master.m3u8",
		manifestPath,
	)

	cmd := exec.Command("ffmpeg", args...)
	cmd.Stderr = os.Stderr // log ffmpeg diagnostics to application stderr

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("create ffmpeg stdin pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start ffmpeg: %w", err)
	}

	m.ffmpegCmd = cmd
	m.ffmpegStdin = stdin

	go func() {
		if err := cmd.Wait(); err != nil {
			log.Printf("live[%s]: ffmpeg exited with error: %v", m.streamID, err)
		} else {
			log.Printf("live[%s]: ffmpeg exited cleanly", m.streamID)
		}
		close(m.done)
	}()

	log.Printf("live[%s]: ffmpeg started, writing segments to %s", m.streamID, m.mediaDir)
	return nil
}

// Write pipes a WebM chunk from the ingest request body into ffmpeg's stdin.
// Each call corresponds to one MediaRecorder ondataavailable event (~2s of video).
func (m *Manager) Write(data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.ffmpegStdin == nil {
		return fmt.Errorf("live manager not started")
	}

	_, err := m.ffmpegStdin.Write(data)
	return err
}

// Stop closes ffmpeg's stdin, which signals it to flush and exit cleanly.
// It waits up to 10 seconds for ffmpeg to finish writing the final segment.
func (m *Manager) Stop() error {
	m.mu.Lock()
	if m.ffmpegStdin != nil {
		m.ffmpegStdin.Close()
	}
	m.mu.Unlock()

	select {
	case <-m.done:
		return nil
	case <-time.After(10 * time.Second):
		log.Printf("live[%s]: ffmpeg did not exit in time, killing", m.streamID)
		m.mu.Lock()
		if m.ffmpegCmd != nil && m.ffmpegCmd.Process != nil {
			m.ffmpegCmd.Process.Kill()
		}
		m.mu.Unlock()
		return fmt.Errorf("ffmpeg did not exit cleanly")
	}
}

// Done returns a channel that is closed when ffmpeg has exited.
func (m *Manager) Done() <-chan struct{} {
	return m.done
}

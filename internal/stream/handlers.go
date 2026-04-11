package stream

import (
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/c0d3d3v/streamer-2/internal/auth"
	"github.com/c0d3d3v/streamer-2/internal/stream/live"
	"github.com/c0d3d3v/streamer-2/internal/viewer"
	"github.com/c0d3d3v/streamer-2/internal/websocket"
	"github.com/gin-gonic/gin"
)

func init() {
	// .m4s (CMAF fMP4 segment) is not in the default MIME database on most
	// Linux systems. Register it so http.ServeFile sends the correct
	// Content-Type that iOS Safari requires for native HLS segment playback.
	err := mime.AddExtensionType(".m4s", "video/mp4")
	if err != nil {
		fmt.Println("could not register MIME type for .m4s: ", err)
	}
}

// createRequest is the JSON body for POST /api/streams.
type createRequest struct {
	Title       string     `json:"title"        binding:"required"`
	ScheduledAt *time.Time `json:"scheduled_at"`
}

// HandleCreate creates a new scheduled or immediate stream.
// POST /api/streams
func HandleCreate(c *gin.Context) {
	user := auth.CurrentUser(c)

	var req createRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	s, err := Create(CreateInput{
		Title:       req.Title,
		ScheduledAt: req.ScheduledAt,
		OwnerID:     user.Subject,
	})
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Create a default public share link so the user can immediately share
	// the stream without having to open the Links panel first.
	if _, err := viewer.Create(viewer.CreateInput{
		StreamID:  &s.ID,
		CreatedBy: user.Subject,
		IsDefault: true,
	}); err != nil {
		log.Printf("stream[%s]: failed to create default share link: %v", s.ID, err)
	}

	c.JSON(http.StatusCreated, s)
}

// HandleList returns all streams owned by the authenticated user.
// GET /api/streams
func HandleList(c *gin.Context) {
	user := auth.CurrentUser(c)
	streams, err := ListByOwner(user.Subject)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database error"})
		return
	}
	c.JSON(http.StatusOK, streams)
}

// HandleGet returns a single stream by ID.
// GET /api/streams/:id
func HandleGet(c *gin.Context) {
	s, err := GetByID(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "stream not found"})
		return
	}
	c.JSON(http.StatusOK, s)
}

// startRequest is the JSON body for POST /api/streams/:id/start.
type startRequest struct {
	// Rotation is the clockwise degrees the streamer has rotated the camera:
	// 0, 90, 180, or 270. The value is written as rotation metadata into the
	// fMP4 init segment so viewers automatically display the correct orientation.
	Rotation int `json:"rotation"`
	// Tier is the resolution tier selected by the streamer (QHD, FHD, HD).
	Tier Tier `json:"tier" binding:"required"`
	// AspectRatio is the camera's native long/short ratio (e.g. 1.7778 for 16:9).
	AspectRatio float64 `json:"aspect_ratio" binding:"required"`
}

// HandleStart transitions a stream to live and starts the HLS pipeline.
// POST /api/streams/:id/start
func HandleStart(c *gin.Context) {
	user := auth.CurrentUser(c)

	var req startRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Rotation != 0 && req.Rotation != 90 && req.Rotation != 180 && req.Rotation != 270 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "rotation must be 0, 90, 180, or 270"})
		return
	}

	s, err := Start(c.Param("id"), user.Subject, req.Rotation, req.Tier, req.AspectRatio)
	if err != nil {
		status := http.StatusInternalServerError
		if err == ErrNotFound {
			status = http.StatusNotFound
		} else if err == ErrForbidden {
			status = http.StatusForbidden
		} else if err == ErrInvalidTransition {
			status = http.StatusConflict
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}

	// Start the ffmpeg DASH manager for this stream.
	// The manager writes the manifest.mpd and fMP4 segments to s.LiveDir.
	// ffmpeg writes the first manifest after ~2 seconds of input; DASH.js
	// retries (10×, 1s apart) so brief 404s on the manifest are tolerated.
	mgr := live.New(s.ID, s.LiveDir, req.Rotation)
	if err := mgr.Start(); err != nil {
		MarkFailed(s.ID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to start DASH pipeline"})
		return
	}
	Register(s.ID, mgr)
	startWatchdog()

	// Notify any waiting viewers via WebSocket that the stream has gone live.
	websocket.Hub.Broadcast(s.ID, websocket.Message{
		Type: "stream.started",
		Payload: map[string]string{
			"dash_url": "/live/" + s.ID + "/manifest.mpd",
			"hls_url":  "/live/" + s.ID + "/master.m3u8",
		},
	})

	c.JSON(http.StatusOK, s)
}

// HandleIngest receives a raw WebM chunk from the streamer's browser and
// pipes it into the active ffmpeg process.
// POST /api/streams/:id/ingest
//
// The client (MediaRecorder) posts binary blobs with Content-Type application/octet-stream.
// Each blob is typically 2-4 seconds of encoded video+audio.
func HandleIngest(c *gin.Context) {
	user := auth.CurrentUser(c)
	streamID := c.Param("id")

	// Verify ownership before accepting any data.
	s, err := GetByID(streamID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "stream not found"})
		return
	}
	if s.OwnerID != user.Subject {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}
	if !s.IsLive() {
		c.JSON(http.StatusConflict, gin.H{"error": "stream is not live"})
		return
	}

	mgr := GetManager(streamID)
	if mgr == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "no active DASH manager for this stream"})
		return
	}

	// Read the entire chunk. Chunks are small (a few hundred KB at most) so
	// reading into memory is fine. A size limit guards against abuse.
	const maxChunkSize = 10 << 20 // 10 MB
	data, err := io.ReadAll(io.LimitReader(c.Request.Body, maxChunkSize))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read chunk"})
		return
	}

	if err := mgr.Write(data); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to write to HLS pipeline"})
		return
	}

	TouchLastSeen(streamID)
	c.Status(http.StatusNoContent)
}

// HandleStop ends a live stream and triggers MP4 finalization.
// POST /api/streams/:id/stop
func HandleStop(c *gin.Context) {
	user := auth.CurrentUser(c)
	streamID := c.Param("id")

	s, err := Stop(streamID, user.Subject)
	if err != nil {
		status := http.StatusInternalServerError
		if err == ErrNotFound {
			status = http.StatusNotFound
		} else if err == ErrForbidden {
			status = http.StatusForbidden
		} else if err == ErrInvalidTransition {
			status = http.StatusConflict
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}

	// Stop the HLS pipeline (closes ffmpeg stdin → ffmpeg flushes final segment).
	StopManager(streamID)

	// Trigger MP4 finalization asynchronously so the HTTP response is immediate.
	// The archive package handles the actual ffmpeg concat work.
	go finalizeArchive(s)

	websocket.Hub.Broadcast(streamID, websocket.Message{
		Type:    "stream.ended",
		Payload: map[string]string{"stream_id": streamID},
	})

	c.JSON(http.StatusOK, s)
}

// HandleDelete deletes a stream and its HLS files. The archive is deleted
// separately via the archive handlers.
// DELETE /api/streams/:id
func HandleDelete(c *gin.Context) {
	user := auth.CurrentUser(c)
	streamID := c.Param("id")

	s, err := GetByID(streamID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "stream not found"})
		return
	}

	if err := Delete(streamID, user.Subject); err != nil {
		status := http.StatusInternalServerError
		if err == ErrForbidden {
			status = http.StatusForbidden
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}

	// Delete all shared links for this stream.
	_ = viewer.DeleteForStream(streamID)

	// Clean up HLS segments from disk.
	if s.LiveDir != "" {
		if err := os.RemoveAll(s.LiveDir); err != nil {
			// Non-fatal: log and continue.
			_ = err
		}
	}

	c.Status(http.StatusNoContent)
}

// batchDeleteRequest is the JSON body for POST /api/streams/batch-delete.
type batchDeleteRequest struct {
	IDs []string `json:"ids" binding:"required"`
}

// HandleBatchDelete deletes multiple streams and their HLS files.
// POST /api/streams/batch-delete
func HandleBatchDelete(c *gin.Context) {
	user := auth.CurrentUser(c)

	var req batchDeleteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if len(req.IDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no IDs provided"})
		return
	}

	for _, streamID := range req.IDs {
		s, err := GetByID(streamID)
		if err != nil {
			// Skip not found streams
			continue
		}

		if err := Delete(streamID, user.Subject); err != nil {
			// Skip on permission error, log and continue
			if err == ErrForbidden {
				continue
			}
		}

		// Delete all shared links for this stream.
		_ = viewer.DeleteForStream(streamID)

		// Clean up HLS segments from disk.
		if s.LiveDir != "" {
			if err := os.RemoveAll(s.LiveDir); err != nil {
				// Non-fatal: log and continue.
				_ = err
			}
		}
	}

	c.Status(http.StatusNoContent)
}

// HandleServeMedia serves DASH manifests (.mpd), HLS playlists (.m3u8), and
// shared CMAF fMP4 segment files (.m4s) from the stream's media directory.
// GET /live/:streamId/*filepath
func HandleServeMedia(c *gin.Context) {
	streamID := c.Param("streamId")
	filePath := c.Param("filepath")

	s, err := GetByID(streamID)
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}

	if s.LiveDir == "" {
		c.Status(http.StatusNotFound)
		return
	}

	fullPath := filepath.Join(s.LiveDir, filepath.Clean(filePath))

	// Security: ensure the resolved path is still inside the DASH directory.
	// This prevents path traversal attacks (e.g. /../../../etc/passwd).
	absDashDir, _ := filepath.Abs(s.LiveDir)
	absPath, _ := filepath.Abs(fullPath)
	if absPath != absDashDir && !strings.HasPrefix(absPath, absDashDir+string(filepath.Separator)) {
		c.Status(http.StatusForbidden)
		return
	}

	// MPD and HLS playlists are rewritten after every segment — always fetch
	// the latest version from the server.
	if strings.HasSuffix(filePath, ".mpd") {
		data, err := os.ReadFile(fullPath)
		if err != nil {
			c.Status(http.StatusNotFound)
			return
		}
		addNoCacheHeaders(c)
		c.Data(http.StatusOK, "application/dash+xml", data)
		return
	}

	if strings.HasSuffix(filePath, ".m3u8") {
		data, err := os.ReadFile(fullPath)
		if err != nil {
			c.Status(http.StatusNotFound)
			return
		}
		addNoCacheHeaders(c)
		c.Data(http.StatusOK, "application/vnd.apple.mpegurl", data)
		return
	}

	// fMP4 segments (.m4s) are immutable once written — safe to cache.
	c.Header("Access-Control-Allow-Origin", "*")
	c.File(fullPath)
}

func addNoCacheHeaders(c *gin.Context) {
	c.Header("Cache-Control", "no-store, no-cache, must-revalidate")
	c.Header("Pragma", "no-cache")
	c.Header("Access-Control-Allow-Origin", "*")
}

// finalizeArchive is called asynchronously after a stream ends. It delegates
// to the archive package to run ffmpeg and create the MP4 file.
// Defined as a variable so it can be replaced in tests.
var finalizeArchive = func(s *Stream) {
	// Import cycle is avoided by calling the archive package from main/router.
	// This function is overridden by wire-up code in cmd/server/main.go.
}

// SetFinalizeFunc allows cmd / server to inject the archive finalizer without
// creating an import cycle.
func SetFinalizeFunc(fn func(*Stream)) {
	finalizeArchive = fn
}

// watchdogOnce ensures the ingest watchdog goroutine is started only once.
var watchdogOnce sync.Once

// startWatchdog starts a background goroutine that polls for streams that
// have not received any ingest data for longer than IngestTimeout and stops
// them automatically. This handles the case where the streamer closes the
// browser tab without pressing "End Stream".
func startWatchdog() {
	watchdogOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(10 * time.Second)
			defer ticker.Stop()
			for range ticker.C {
				for _, id := range timedOutStreams() {
					log.Printf("stream[%s]: no ingest data for %s, auto-stopping", id, IngestTimeout)
					internalStopStream(id)
				}
			}
		}()
	})
}

// internalStopStream stops a live stream without an HTTP context. It performs
// the same steps as HandleStop: marks the stream as ended, stops the ffmpeg
// pipeline, triggers archive finalization, and notifies viewers via WebSocket.
func internalStopStream(streamID string) {
	s, err := GetByID(streamID)
	if err != nil {
		return
	}
	stopped, err := Stop(streamID, s.OwnerID)
	if err != nil {
		// Already ended or not found — nothing to do.
		return
	}
	StopManager(streamID)
	go finalizeArchive(stopped)
	websocket.Hub.Broadcast(streamID, websocket.Message{
		Type:    "stream.ended",
		Payload: map[string]string{"stream_id": streamID},
	})
}

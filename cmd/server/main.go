// Command server is the single binary that serves both the Go API and the
// embedded React frontend.
//
// Startup sequence:
//  1. Load config from CONFIG_PATH (default: config.yaml)
//  2. Initialize the database and run GORM AutoMigrate
//  3. If setup is complete, initialize the OIDC provider
//  4. Register all HTTP routes and start the HTTP server
//
// On the first run, steps 2–3 use safe defaults so the setup wizard is reachable.
package main

import (
	"context"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/c0d3d3v/streamer-2/internal/archive"
	"github.com/c0d3d3v/streamer-2/internal/auth"
	"github.com/c0d3d3v/streamer-2/internal/config"
	"github.com/c0d3d3v/streamer-2/internal/infra/db"
	"github.com/c0d3d3v/streamer-2/internal/stream"
	"github.com/c0d3d3v/streamer-2/internal/viewer"
	"github.com/c0d3d3v/streamer-2/internal/websocket"
	"github.com/c0d3d3v/streamer-2/internal/webui"
	"github.com/gin-gonic/gin"
)

func main() {
	// Configuration
	configPath := envOrDefault("CONFIG_PATH", "/data/config.yaml")
	cfg, err := config.Load(configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	// Override DataDir from the environment if provided (useful in Docker).
	// Also, derive the SQLite DSN from DATA_DIR when it is still the default
	// relative path ("data/streamer.db"), so the database ends up inside the
	// mounted volume rather than the read-only /app directory.
	if dataDir := os.Getenv("DATA_DIR"); dataDir != "" {
		if err := config.Update(func(c *config.Config) {
			c.App.DataDir = dataDir
			if c.Database.Driver == "sqlite" || c.Database.Driver == "" {
				if c.Database.DSN == "" || c.Database.DSN == "/data/streamer.db" {
					// Only update if it is still the default path.
					c.Database.DSN = filepath.Join(dataDir, "streamer.db")
				}
			}
		}); err != nil {
			log.Fatalf("update data dir: %v", err)
		}
		cfg = config.Get()
	}

	// Database
	if err := db.Init(
		&auth.Session{},
		&stream.Stream{},
		&viewer.SharedLink{},
		&archive.Archive{},
	); err != nil {
		log.Fatalf("init database: %v", err)
	}

	// OIDC provider (only if setup is complete)
	if config.IsSetupComplete() {
		if err := auth.InitOIDC(context.Background()); err != nil {
			log.Printf("warning: OIDC init failed (%v) – login will be unavailable", err)
		}
	}

	// Inject archive finalizer into stream package
	// This breaks the stream→archive import cycle: stream.SetFinalizeFunc is
	// called here (in main) where both packages are already imported.
	stream.SetFinalizeFunc(func(s *stream.Stream) {
		archive.Finalize(s.ID, s.Title, s.LiveDir, s.OwnerID)
	})

	// Background tasks
	go pruneSessionsPeriodically()

	// HTTP router
	router := buildRouter()

	addr := cfg.Server.Host + ":" + cfg.Server.Port
	log.Printf("streamer starting on %s", addr)
	if err := router.Run(addr); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

// buildRouter wires all routes and middleware.
func buildRouter() *gin.Engine {
	// Use release mode unless GIN_MODE is explicitly set in the environment.
	if os.Getenv("GIN_MODE") == "" {
		gin.SetMode(gin.ReleaseMode)
	}
	r := gin.Default()

	// Setup-only routes (accessible even before setup is complete)
	r.GET("/api/setup/status", config.HandleSetupStatus)
	r.POST("/api/setup/complete", config.HandleSetupComplete)

	// Auth routes
	r.GET("/auth/login", auth.HandleLogin)
	r.GET("/auth/callback", auth.HandleCallback)
	r.POST("/auth/logout", auth.HandleLogout)

	// Authenticated API routes
	api := r.Group("/api", auth.RequireAuth)
	{
		api.GET("/me", auth.HandleMe)

		// Streams
		api.POST("/streams", stream.HandleCreate)
		api.GET("/streams", stream.HandleList)
		api.POST("/streams/batch-delete", stream.HandleBatchDelete)
		api.GET("/streams/:id", stream.HandleGet)
		api.DELETE("/streams/:id", stream.HandleDelete)
		api.POST("/streams/:id/start", stream.HandleStart)
		api.POST("/streams/:id/ingest", stream.HandleIngest)
		api.POST("/streams/:id/stop", stream.HandleStop)

		// Shared links management
		api.POST("/links", viewer.HandleCreateLink)
		api.GET("/links", viewer.HandleListLinks)
		api.GET("/links/for", viewer.HandleGetLinkForTarget)
		api.DELETE("/links/:id", viewer.HandleDeleteLink)

		// Archive
		api.GET("/archive", archive.HandleList)
		api.POST("/archive/batch-delete", archive.HandleBatchDelete)
		api.GET("/archive/:id", archive.HandleGet)
		api.DELETE("/archive/:id", archive.HandleDelete)
	}

	// Live media serving — no auth middleware.
	// Stream IDs are UUIDs (unguessable), and these URLs are only revealed
	// to clients who already passed the shared-link flow.
	// Serves manifest.mpd (DASH), master.m3u8 (HLS), and shared .m4s segments.
	r.GET("/live/:streamId/*filepath", stream.HandleServeMedia)

	// Public viewer routes
	r.GET("/api/watch/:token/info", viewer.HandleWatchInfo)
	r.POST("/api/watch/:token/auth", viewer.HandleWatchAuth)
	r.GET("/api/watch/:token/video", viewer.HandleWatchVideo)

	// WebSocket
	r.GET("/ws/stream/:streamId", websocket.HandleWS)

	// Serve embedded React SPA
	// The React app handles its own client-side routing. All non-API paths
	// fall through to the SPA so that direct navigation / refresh works.
	distFS, err := fs.Sub(webui.FS, "dist")
	if err != nil {
		log.Fatalf("embed web/dist: %v", err)
	}
	staticHandler := http.FileServer(http.FS(distFS))

	r.NoRoute(func(c *gin.Context) {
		// Try to serve a real file (JS, CSS, images) first.
		// If the file doesn't exist, serve index.html so the React router takes over.
		path := c.Request.URL.Path
		f, err := distFS.Open(path[1:]) // strip leading "/"
		if err == nil {
			f.Close()
			staticHandler.ServeHTTP(c.Writer, c.Request)
			return
		}
		// Fall back to index.html for client-side routing.
		c.Request.URL.Path = "/"
		staticHandler.ServeHTTP(c.Writer, c.Request)
	})

	return r
}

// pruneSessionsPeriodically deletes expired sessions from the database once
// per hour. This prevents unbounded table growth without requiring a cron job.
func pruneSessionsPeriodically() {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for range ticker.C {
		auth.PruneExpiredSessions()
	}
}

// envOrDefault returns the value of an environment variable or a default.
func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

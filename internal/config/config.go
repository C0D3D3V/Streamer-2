// Package config handles application configuration loading, saving.
package config

import (
	"os"
	"sync"

	"gopkg.in/yaml.v3"
)

// Config is the root configuration structure.
type Config struct {
	// Server holds HTTP server settings.
	Server ServerConfig `yaml:"server"`

	// Database holds the GORM driver and DSN.
	Database DatabaseConfig `yaml:"database"`

	// OIDC holds the OAuth2 / OpenID Connect client settings.
	// These are populated during the first-run setup wizard.
	OIDC OIDCConfig `yaml:"oidc"`

	// App holds general application settings configurable after setup.
	App AppConfig `yaml:"app"`
}

// ServerConfig controls the HTTP listener.
type ServerConfig struct {
	// Host is the bind address, e.g. "0.0.0.0" or "127.0.0.1".
	Host string `yaml:"host"`
	// Port is the TCP port to listen on.
	Port string `yaml:"port"`
	// ExternalURL is the public-facing base URL (used for OAuth2 redirect URIs).
	// Example: "https://stream.example.com"
	ExternalURL string `yaml:"external_url"`
}

// DatabaseConfig selects the GORM driver and connection string.
type DatabaseConfig struct {
	// Driver is either "sqlite" or "postgres".
	Driver string `yaml:"driver"`
	// DSN is the data source name.
	//   sqlite:   "data/streamer.db"
	//   postgres: "host=localhost user=streamer password=secret dbname=streamer sslmode=disable"
	DSN string `yaml:"dsn"`
}

// OIDCConfig holds the OpenID Connect client credentials.
type OIDCConfig struct {
	// IssuerURL is the base URL of the Authelia instance.
	// Example: "https://auth.example.com"
	IssuerURL string `yaml:"issuer_url"`
	// ClientID is the OIDC client identifier registered in Authelia.
	ClientID string `yaml:"client_id"`
	// ClientSecret is the OIDC client secret registered in Authelia.
	ClientSecret string `yaml:"client_secret"`
}

// RedirectURL returns the OAuth2 callback URL derived from the external URL.
func (o *OIDCConfig) RedirectURL(externalURL string) string {
	return externalURL + "/auth/callback"
}

// AppConfig holds general settings adjustable by logged-in users after setup.
type AppConfig struct {
	// SessionSecret is a random string used to sign session cookies.
	// Auto-generated on the first run if empty.
	SessionSecret string `yaml:"session_secret"`
	// SetupLocked is set to true automatically after the first successful
	// OAuth login. Once locked, the setup wizard is disabled.
	SetupLocked bool `yaml:"setup_locked"`
	// DataDir is the root directory for HLS segments, recordings, and video archives.
	DataDir string `yaml:"data_dir"`
	// KeepSegmentsAfterFinalization controls whether raw .ts segments are
	// deleted after the video archive is created. Default: false (delete them).
	KeepSegmentsAfterFinalization bool `yaml:"keep_segments_after_finalization"`
	// MaxStreamDurationMinutes is a safety limit. 0 means unlimited.
	MaxStreamDurationMinutes int `yaml:"max_stream_duration_minutes"`
	// HWAccel selects the hardware acceleration backend for ffmpeg operations.
	// Supported values:
	//   ""      / "none"  – software only (default, works everywhere)
	//   "auto"            – Detect hardware acceleration automatically
	//   "qsv"             – Intel Quick Sync Video (requires Intel GPU + drivers)
	//   "nvenc"           – NVIDIA hardware encoder (requires NVIDIA GPU + drivers)
	//   "vaapi"           – VA-API, generic Linux hardware accel (AMD, Intel)
	//
	// When set, the archive finalizer re-encodes video using the hardware
	// encoder instead of stream-copying.
	HWAccel string `yaml:"hwaccel"`
	// LiveProxyURL is the base URL of an nginx caching proxy that serves live
	// stream files (DASH/HLS segments) to viewers. When set, manifest and
	// segment URLs sent to viewers point to this host instead of the origin.
	// Leave empty to serve live files directly from this server (default).
	// Example: "https://tube-live.vogt.casa:8888"
	LiveProxyURL string `yaml:"live_proxy_url"`
}

// manager is the package-level singleton that holds the loaded config and its
// file path so that Save() does not need a path argument at the call site.
var manager struct {
	mu       sync.RWMutex
	cfg      *Config
	filePath string
}

// Load reads and parses the YAML file at the path. It merges the file values on
// top of sane defaults so that missing keys fall back gracefully.
func Load(path string) (*Config, error) {
	cfg := defaults()

	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	// File may not exist yet on the first run – that is fine; defaults are used.
	if err == nil {
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, err
		}
	}

	manager.mu.Lock()
	manager.cfg = cfg
	manager.filePath = path
	manager.mu.Unlock()

	return cfg, nil
}

// Save serializes the current in-memory config back to the YAML file.
// It is safe to call from multiple goroutines.
func Save() error {
	manager.mu.RLock()
	cfg := manager.cfg
	path := manager.filePath
	manager.mu.RUnlock()

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// Get returns a pointer to the loaded config. The caller must not modify the
// returned value concurrently; use Update for mutations.
func Get() *Config {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.cfg
}

// Update applies a mutation function to the config and saves it to disk.
// Example:
//
//	config.Update(func(c *config.Config) {
//	    c.OIDC.ClientID = "new-id"
//	})
func Update(fn func(*Config)) error {
	manager.mu.Lock()
	fn(manager.cfg)
	manager.mu.Unlock()
	return Save()
}

// IsSetupComplete returns true when the OIDC issuer has been configured,
// which is the last required step in the setup wizard.
func IsSetupComplete() bool {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.cfg.OIDC.IssuerURL != "" &&
		manager.cfg.OIDC.ClientID != "" &&
		manager.cfg.OIDC.ClientSecret != ""
}

// IsSetupLocked returns true after the first successful OAuth login.
// A locked setup means the wizard endpoints are disabled.
func IsSetupLocked() bool {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.cfg.App.SetupLocked
}

// LockSetup disables the setup wizard by setting SetupLocked=true
// and persisting the change to config.yaml. It is a no-op if already locked.
func LockSetup() error {
	if IsSetupLocked() {
		return nil
	}
	return Update(func(c *Config) {
		c.App.SetupLocked = true
	})
}

// LiveBaseURL returns the base URL used to construct live stream file URLs
// (DASH manifests, HLS playlists, segments). When a LiveProxyURL is configured
// it is returned; otherwise an empty string is returned and callers should use
// relative paths so the origin server serves the files directly.
func (a *AppConfig) LiveBaseURL() string {
	return a.LiveProxyURL
}

// defaults returns a Config pre-populated with sane out-of-the-box values.
func defaults() *Config {
	return &Config{
		Server: ServerConfig{
			Host: "0.0.0.0",
			Port: "8080",
		},
		Database: DatabaseConfig{
			Driver: "sqlite",
			DSN:    "/data/streamer.db",
		},
		OIDC: OIDCConfig{},
		App: AppConfig{
			DataDir:                       "/data",
			KeepSegmentsAfterFinalization: false,
			MaxStreamDurationMinutes:      0,
		},
	}
}

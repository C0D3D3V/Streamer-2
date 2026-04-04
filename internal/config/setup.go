package config

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"

	"github.com/gin-gonic/gin"
)

// SetupRequest is the JSON body for POST /api/setup/complete.
type SetupRequest struct {
	IssuerURL    string `json:"issuer_url"    binding:"required"`
	ClientID     string `json:"client_id"     binding:"required"`
	ClientSecret string `json:"client_secret" binding:"required"`
	ExternalURL  string `json:"external_url"  binding:"required"`
}

// HandleSetupStatus returns whether the first-run setup has been completed
// and whether the setup wizard has been permanently locked.
// GET /api/setup/status
func HandleSetupStatus(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"setup_complete": IsSetupComplete(),
		"setup_locked":   IsSetupLocked(),
	})
}

// HandleSetupComplete receives the OIDC credentials from the setup wizard,
// persists them to config.yaml, and generates a session secret if needed.
// POST /api/setup/complete
func HandleSetupComplete(c *gin.Context) {
	// Once the first OAuth login succeeds, the wizard is permanently locked.
	if IsSetupLocked() {
		c.JSON(http.StatusForbidden, gin.H{"error": "setup is already complete and locked"})
		return
	}

	var req SetupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	err := Update(func(cfg *Config) {
		cfg.OIDC.IssuerURL = req.IssuerURL
		cfg.OIDC.ClientID = req.ClientID
		cfg.OIDC.ClientSecret = req.ClientSecret
		cfg.Server.ExternalURL = req.ExternalURL
		// RedirectURL and scopes are derived at runtime — not stored in config.

		// Generate a session secret on first setup if not already set.
		if cfg.App.SessionSecret == "" {
			cfg.App.SessionSecret = generateSecret(32)
		}
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save configuration"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "setup complete"})
}

// generateSecret returns a cryptographically random hex string of n bytes.
func generateSecret(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("failed to generate random secret: " + err.Error())
	}
	return hex.EncodeToString(b)
}

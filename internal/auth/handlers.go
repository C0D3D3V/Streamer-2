package auth

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/c0d3d3v/streamer-2/internal/config"
)

const (
	// pkceVerifierCookie temporarily stores the PKCE code_verifier between the
	// login redirect and the callback. It is deleted after use.
	pkceVerifierCookie = "pkce_verifier"
	// stateCookie temporarily stores the OAuth2 state nonce.
	stateCookie = "oauth_state"
)

// HandleLogin starts the OIDC authorization flow by redirecting the browser
// to the Authelia authorization endpoint.
// GET /auth/login
func HandleLogin(c *gin.Context) {
	url, state, codeVerifier, err := AuthCodeURL()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not build auth URL"})
		return
	}

	// Store state and PKCE verifier in short-lived cookies so the callback
	// handler can retrieve them. SameSite=Lax is intentional: the callback is
	// a cross-site redirect from Authelia, and Lax allows that.
	c.SetCookie(stateCookie, state, 300, "/", "", false, true)
	c.SetCookie(pkceVerifierCookie, codeVerifier, 300, "/", "", false, true)

	c.Redirect(http.StatusFound, url)
}

// HandleCallback handles the redirect back from Authelia after the user
// authenticates. It validates state, exchanges the code, creates a session,
// and redirects to the dashboard.
// GET /auth/callback
func HandleCallback(c *gin.Context) {
	// Validate the state parameter to prevent CSRF.
	storedState, err := c.Cookie(stateCookie)
	if err != nil || storedState != c.Query("state") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid state parameter"})
		return
	}

	codeVerifier, err := c.Cookie(pkceVerifierCookie)
	if err != nil || codeVerifier == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing PKCE verifier"})
		return
	}

	// Clear the temporary PKCE / state cookies.
	c.SetCookie(stateCookie, "", -1, "/", "", false, true)
	c.SetCookie(pkceVerifierCookie, "", -1, "/", "", false, true)

	code := c.Query("code")
	if code == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing authorization code"})
		return
	}

	user, err := Exchange(c.Request.Context(), code, codeVerifier)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication failed"})
		return
	}

	sessionID, err := CreateSession(user)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not create session"})
		return
	}

	// Set the session cookie. HttpOnly prevents JS access; this is a first-party
	// cookie so SameSite=Lax is appropriate.
	c.SetCookie(SessionCookieName, sessionID, int(SessionDuration.Seconds()), "/", "", false, true)

	// Lock the setup wizard after the first successful login.
	// This is the "test" that OAuth works — once it passes, setup is sealed.
	_ = config.LockSetup()

	c.Redirect(http.StatusFound, "/")
}

// HandleLogout clears the session cookie and deletes the server-side session.
// POST /auth/logout
func HandleLogout(c *gin.Context) {
	sessionID, err := c.Cookie(SessionCookieName)
	if err == nil && sessionID != "" {
		DeleteSession(sessionID)
	}
	c.SetCookie(SessionCookieName, "", -1, "/", "", false, true)
	c.JSON(http.StatusOK, gin.H{"message": "logged out"})
}

// HandleMe returns the currently authenticated user's profile.
// GET /api/me
func HandleMe(c *gin.Context) {
	user := CurrentUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"subject": user.Subject,
		"email":   user.Email,
		"name":    user.Name,
	})
}

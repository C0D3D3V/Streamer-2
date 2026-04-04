package auth

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

const (
	// SessionCookieName is the name of the HTTP-only session cookie.
	SessionCookieName = "streamer_session"
	// ContextUserKey is the key used to store the authenticated User in the
	// Gin context so that handlers can retrieve it with CurrentUser(c).
	ContextUserKey = "auth_user"
)

// RequireAuth is a Gin middleware that rejects unauthenticated requests with
// 401. On successful authentication via cookie, it stores the User in the context under ContextUserKey.
func RequireAuth(c *gin.Context) {
	user := userFromContext(c)
	if user == nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}
	c.Set(ContextUserKey, user)
	c.Next()
}

// CurrentUser extracts the authenticated User from the Gin context.
// Returns nil if called outside a RequireAuth-protected route.
func CurrentUser(c *gin.Context) *User {
	val, exists := c.Get(ContextUserKey)
	if !exists {
		return nil
	}
	user, _ := val.(*User)
	return user
}

// LoadUser is a soft version of RequireAuth: it loads the user into context if
// a valid session exists but does NOT abort unauthenticated requests. Useful
// for routes that serve both authenticated and anonymous visitors.
func LoadUser(c *gin.Context) {
	user := userFromContext(c)
	if user != nil {
		c.Set(ContextUserKey, user)
	}
	c.Next()
}

// userFromContext reads the session cookie and resolves the associated User.
func userFromContext(c *gin.Context) *User {
	sessionID, err := c.Cookie(SessionCookieName)
	if err != nil || sessionID == "" {
		return nil
	}
	return GetSession(sessionID)
}

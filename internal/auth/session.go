package auth

import (
	"time"

	"github.com/c0d3d3v/streamer-2/internal/infra/db"
	"github.com/google/uuid"
)

// CreateSession persists a new session for the given user and returns the
// session ID that must be stored in the client's cookie.
func CreateSession(user *User) (string, error) {
	sess := &Session{
		ID:        uuid.NewString(),
		UserSub:   user.Subject,
		UserEmail: user.Email,
		UserName:  user.Name,
		ExpiresAt: time.Now().Add(SessionDuration),
	}
	if err := db.DB.Create(sess).Error; err != nil {
		return "", err
	}
	return sess.ID, nil
}

// GetSession loads and validates a session by its ID. Returns nil when the
// session does not exist or has expired.
func GetSession(id string) *User {
	var sess Session
	if err := db.DB.First(&sess, "id = ?", id).Error; err != nil {
		return nil
	}
	if sess.IsExpired() {
		// Clean up lazily – no need to block on deletion.
		go db.DB.Delete(&sess)
		return nil
	}
	return &User{
		Subject: sess.UserSub,
		Email:   sess.UserEmail,
		Name:    sess.UserName,
	}
}

// DeleteSession removes a session from the database (logout).
func DeleteSession(id string) {
	db.DB.Delete(&Session{}, "id = ?", id)
}

// PruneExpiredSessions deletes all sessions whose ExpiresAt has passed.
// Call this periodically (e.g. once per hour) to keep the table small.
func PruneExpiredSessions() {
	db.DB.Delete(&Session{}, "expires_at < ?", time.Now())
}

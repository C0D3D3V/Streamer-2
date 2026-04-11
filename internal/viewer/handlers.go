package viewer

import (
	"net/http"
	"path/filepath"
	"time"

	"github.com/c0d3d3v/streamer-2/internal/auth"
	"github.com/c0d3d3v/streamer-2/internal/config"
	"github.com/c0d3d3v/streamer-2/internal/infra/db"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

// viewerTokenExpiry is how long a password-authenticated viewer JWT is valid.
const viewerTokenExpiry = time.Hour

// HandleCreateLink creates a new shared link for a stream or archive.
// POST /api/links
func HandleCreateLink(c *gin.Context) {
	user := auth.CurrentUser(c)

	var req struct {
		StreamID  *string    `json:"stream_id"`
		ArchiveID *string    `json:"archive_id"`
		Slug      string     `json:"slug"`
		Password  string     `json:"password"`
		ExpiresAt *time.Time `json:"expires_at"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.StreamID == nil && req.ArchiveID == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "stream_id or archive_id is required"})
		return
	}

	link, err := Create(CreateInput{
		StreamID:  req.StreamID,
		ArchiveID: req.ArchiveID,
		Slug:      req.Slug,
		Password:  req.Password,
		ExpiresAt: req.ExpiresAt,
		CreatedBy: user.Subject,
	})
	if err != nil {
		if err == ErrSlugTaken {
			c.JSON(http.StatusConflict, gin.H{"error": "that short name is already taken"})
			return
		}
		if err == ErrSlugInvalid {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create link"})
		return
	}
	c.JSON(http.StatusCreated, link)
}

// HandleListLinks returns shared links created by the authenticated user.
// Optional query params ?stream_id=X or ?archive_id=X filter to a specific target.
// GET /api/links
func HandleListLinks(c *gin.Context) {
	user := auth.CurrentUser(c)
	streamID := c.Query("stream_id")
	archiveID := c.Query("archive_id")

	var links []SharedLink
	var err error
	var sID, aID *string
	if streamID != "" {
		sID = &streamID
	}
	if archiveID != "" {
		aID = &archiveID
	}
	if sID != nil || aID != nil {
		links, err = ListForTarget(sID, aID, user.Subject)
	} else {
		links, err = ListByOwner(user.Subject)
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database error"})
		return
	}
	c.JSON(http.StatusOK, links)
}

// HandleGetLinkForTarget returns the existing link for a stream or archive,
// or 404 if none has been created yet.
// GET /api/links/for?stream_id=X  or  ?archive_id=X
func HandleGetLinkForTarget(c *gin.Context) {
	user := auth.CurrentUser(c)
	streamID := c.Query("stream_id")
	archiveID := c.Query("archive_id")

	var sID, aID *string
	if streamID != "" {
		sID = &streamID
	} else if archiveID != "" {
		aID = &archiveID
	} else {
		c.JSON(http.StatusBadRequest, gin.H{"error": "stream_id or archive_id is required"})
		return
	}

	link, err := GetLinkForTarget(sID, aID, user.Subject)
	if err != nil {
		if err == ErrNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "no link found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database error"})
		return
	}
	c.JSON(http.StatusOK, link)
}

// HandleDeleteLink removes a shared link.
// DELETE /api/links/:id
func HandleDeleteLink(c *gin.Context) {
	user := auth.CurrentUser(c)
	if err := Delete(c.Param("id"), user.Subject); err != nil {
		if err == ErrNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "link not found"})
			return
		}
		if err == ErrIsDefault {
			c.JSON(http.StatusForbidden, gin.H{"error": "the default link cannot be deleted"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete link"})
		return
	}
	c.Status(http.StatusNoContent)
}

// HandleWatchInfo returns stream or archive metadata for a shared link.
// The response indicates whether a password is required.
// GET /api/watch/:token/info
func HandleWatchInfo(c *gin.Context) {
	link, err := GetByToken(c.Param("token"))
	if err != nil {
		code := http.StatusNotFound
		if err == ErrExpired {
			code = http.StatusGone
		}
		c.JSON(code, gin.H{"error": err.Error()})
		return
	}

	// If the link requires a password, return minimal info and let the client
	// show the password gate before revealing stream details.
	if link.HasPassword && !hasViewerAccess(c, link) {
		c.JSON(http.StatusOK, gin.H{
			"requires_password": true,
			"stream_title":      getStreamTitle(link),
		})
		return
	}

	// Issue a viewer_token cookie so the viewer can fetch DASH segments.
	// For password-protected links this was already checked above; for public
	// links we issue it here so requireStreamAccess passes for media requests.
	if !hasViewerAccess(c, link) {
		tokenString, err := issueViewerJWT(link)
		if err == nil {
			c.SetCookie("viewer_token", tokenString, int(viewerTokenExpiry.Seconds()), "/", "", false, true)
		}
	}

	info, err := buildWatchInfo(link)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load stream info"})
		return
	}
	c.JSON(http.StatusOK, info)
}

// HandleWatchAuth validates the password for a protected link and returns a
// short-lived viewer JWT stored in a cookie.
// POST /api/watch/:token/auth
func HandleWatchAuth(c *gin.Context) {
	link, err := GetByToken(c.Param("token"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "link not found"})
		return
	}

	var req struct {
		Password string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := CheckPassword(link, req.Password); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "incorrect password"})
		return
	}

	tokenString, err := issueViewerJWT(link)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to issue token"})
		return
	}

	// Store the viewer JWT in a short-lived cookie.
	c.SetCookie("viewer_token", tokenString, int(viewerTokenExpiry.Seconds()), "/", "", false, true)

	info, err := buildWatchInfo(link)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load stream info"})
		return
	}
	c.JSON(http.StatusOK, info)
}

// HandleWatchVideo serves the archive video file for a shared link.
// Supports HTTP Range requests for seeking; add ?download=1 for a download prompt.
// GET /api/watch/:token/video
func HandleWatchVideo(c *gin.Context) {
	link, err := GetByToken(c.Param("token"))
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	if link.HasPassword && !hasViewerAccess(c, link) {
		c.Status(http.StatusForbidden)
		return
	}

	// Resolve archive: either directly from the link or via the stream.
	archiveID, filePath, title := resolveArchive(link)
	if archiveID == "" || filePath == "" {
		c.Status(http.StatusNotFound)
		return
	}

	if c.Query("download") == "1" {
		ext := filepath.Ext(filePath)
		c.Header("Content-Disposition", `attachment; filename="`+title+ext+`"`)
	}
	// Content-Type is auto-detected from the file extension.
	c.File(filePath)
}

// resolveArchive finds the archive linked to a shared link, returning
// (archiveID, filePath, title). Returns empty strings if not found.
func resolveArchive(link *SharedLink) (string, string, string) {
	var row struct {
		ID       string `gorm:"column:id"`
		FilePath string `gorm:"column:file_path"`
		Title    string `gorm:"column:title"`
	}
	q := db.DB.Table("archives").Select("id, file_path, title")
	if link.ArchiveID != nil {
		q = q.Where("id = ?", *link.ArchiveID)
	} else if link.StreamID != nil {
		q = q.Where("stream_id = ?", *link.StreamID).Order("created_at desc").Limit(1)
	} else {
		return "", "", ""
	}
	q.Scan(&row)
	return row.ID, row.FilePath, row.Title
}

// hasViewerAccess checks whether the request carries a valid viewer JWT for
// the given link.
func hasViewerAccess(c *gin.Context, link *SharedLink) bool {
	tokenStr, err := c.Cookie("viewer_token")
	if err != nil || tokenStr == "" {
		return false
	}
	return validateViewerJWT(tokenStr, link.ID)
}

// issueViewerJWT creates a signed JWT that grants access to a specific link.
func issueViewerJWT(link *SharedLink) (string, error) {
	claims := jwt.MapClaims{
		"link_id": link.ID,
		"exp":     time.Now().Add(viewerTokenExpiry).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(config.Get().App.SessionSecret))
}

// validateViewerJWT checks that a JWT is valid and grants access to linkID.
func validateViewerJWT(tokenStr, linkID string) bool {
	token, err := jwt.Parse(tokenStr, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, jwt.ErrSignatureInvalid
		}
		return []byte(config.Get().App.SessionSecret), nil
	})
	if err != nil || !token.Valid {
		return false
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return false
	}
	return claims["link_id"] == linkID
}

// ValidateStreamAccess checks that tokenStr is a valid viewer JWT whose
// associated link grants access to streamID. This is used by the DASH serving
// middleware to verify cross-origin DASH requests from viewers.
func ValidateStreamAccess(tokenStr, streamID string) bool {
	token, err := jwt.Parse(tokenStr, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, jwt.ErrSignatureInvalid
		}
		return []byte(config.Get().App.SessionSecret), nil
	})
	if err != nil || !token.Valid {
		return false
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return false
	}
	linkID, ok := claims["link_id"].(string)
	if !ok || linkID == "" {
		return false
	}

	// Load the link and verify it grants access to this specific stream.
	var link SharedLink
	if err := db.DB.First(&link, "id = ?", linkID).Error; err != nil {
		return false
	}
	if link.IsExpired() {
		return false
	}
	return link.StreamID != nil && *link.StreamID == streamID
}

// getStreamTitle returns the title of the stream or archive linked by link.
func getStreamTitle(link *SharedLink) string {
	if link.StreamID != nil {
		var title string
		db.DB.Table("streams").Select("title").Where("id = ?", *link.StreamID).Scan(&title)
		return title
	}
	if link.ArchiveID != nil {
		var title string
		db.DB.Table("archives").Select("title").Where("id = ?", *link.ArchiveID).Scan(&title)
		return title
	}
	return ""
}

// buildWatchInfo queries the relevant stream or archive and returns a unified
// info object for the watch page.
func buildWatchInfo(link *SharedLink) (gin.H, error) {
	info := gin.H{
		"link_id":    link.ID,
		"stream_id":  link.StreamID,
		"archive_id": link.ArchiveID,
	}

	if link.StreamID != nil {
		var row struct {
			Status      string     `gorm:"column:status"`
			Tier        string     `gorm:"column:tier"`
			ScheduledAt *time.Time `gorm:"column:scheduled_at"`
			Title       string     `gorm:"column:title"`
			Rotation    int        `gorm:"column:rotation"`
		}
		if err := db.DB.Table("streams").
			Select("status, tier, scheduled_at, title, rotation").
			Where("id = ?", *link.StreamID).
			Scan(&row).Error; err == nil {
			info["stream_status"] = row.Status
			info["stream_quality"] = row.Tier
			info["stream_scheduled_at"] = row.ScheduledAt
			info["stream_title"] = row.Title
			info["stream_rotation"] = row.Rotation
			if row.Status == "live" {
				info["dash_url"] = "/live/" + *link.StreamID + "/manifest.mpd"
				info["hls_url"] = "/live/" + *link.StreamID + "/master.m3u8"
			}
			// For ended streams, include the archive URL if finalization is done.
			if row.Status == "ended" {
				archiveID, _, _ := resolveArchive(link)
				if archiveID != "" {
					info["archive_id"] = archiveID
					info["archive_url"] = "/api/watch/" + link.Token + "/video"
				}
			}
		}
	} else if link.ArchiveID != nil {
		var title string
		db.DB.Table("archives").Select("title").Where("id = ?", *link.ArchiveID).Scan(&title)
		info["stream_status"] = "ended"
		info["stream_title"] = title
		info["archive_url"] = "/api/watch/" + link.Token + "/video"
	}

	return info, nil
}

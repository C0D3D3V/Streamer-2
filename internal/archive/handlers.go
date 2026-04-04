package archive

import (
	"errors"
	"net/http"
	"os"

	"github.com/c0d3d3v/streamer-2/internal/auth"
	"github.com/c0d3d3v/streamer-2/internal/infra/db"
	streamPkg "github.com/c0d3d3v/streamer-2/internal/stream"
	"github.com/c0d3d3v/streamer-2/internal/viewer"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// HandleList returns all finalised archives. Only logged-in users can see the
// archive. Individual items can be shared via the viewer package.
// GET /api/archive
func HandleList(c *gin.Context) {
	var archives []Archive
	if err := db.DB.Order("created_at desc").Find(&archives).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database error"})
		return
	}
	c.JSON(http.StatusOK, archives)
}

// HandleGet returns a single archive by ID.
// GET /api/archive/:id
func HandleGet(c *gin.Context) {
	archive, err := getByID(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "archive not found"})
		return
	}
	c.JSON(http.StatusOK, archive)
}

// HandleDelete removes an archive record and its MKV file from disk.
// Also deletes the associated stream record and its HLS/DASH files.
// DELETE /api/archive/:id
func HandleDelete(c *gin.Context) {
	user := auth.CurrentUser(c)
	archiveID := c.Param("id")

	archive, err := getByID(archiveID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "archive not found"})
		return
	}

	if archive.OwnerID != user.Subject {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}

	// Fetch the stream to get its HLS directory before deleting it.
	stream, err := streamPkg.GetByID(archive.StreamID)
	var liveDir string
	if err == nil {
		liveDir = stream.LiveDir
	}

	// Delete the database record first, then the file (order matters: if the
	// file removal fails we can retry; if DB deletion fails we keep the file).
	if err := db.DB.Delete(archive).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database error"})
		return
	}

	// Also delete the associated stream record.
	if err := streamPkg.Delete(archive.StreamID, user.Subject); err != nil {
		// Log but don't fail the response — archive is already deleted.
		// Non-fatal: the stream may have already been deleted.
		_ = err
	}

	// Delete all shared links for this stream and archive.
	_ = viewer.DeleteForStream(archive.StreamID)
	_ = viewer.DeleteForArchive(archiveID)

	// Clean up HLS/DASH segments from disk.
	if liveDir != "" {
		if err := os.RemoveAll(liveDir); err != nil {
			// Non-fatal: log and continue.
			_ = err
		}
	}

	if archive.FilePath != "" {
		os.Remove(archive.FilePath) // best-effort; log if it fails
	}

	c.Status(http.StatusNoContent)
}

// batchDeleteRequest is the JSON body for POST /api/archive/batch-delete.
type batchDeleteRequest struct {
	IDs []string `json:"ids" binding:"required"`
}

// HandleBatchDelete removes multiple archive records and their files from disk.
// Also deletes the associated stream records and their HLS/DASH files.
// POST /api/archive/batch-delete
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

	for _, archiveID := range req.IDs {
		archive, err := getByID(archiveID)
		if err != nil {
			// Skip not found archives
			continue
		}

		if archive.OwnerID != user.Subject {
			// Skip forbidden archives
			continue
		}

		// Fetch the stream to get its HLS directory before deleting it.
		stream, err := streamPkg.GetByID(archive.StreamID)
		var liveDir string
		if err == nil {
			liveDir = stream.LiveDir
		}

		// Delete the database record first, then the file.
		if err := db.DB.Delete(archive).Error; err != nil {
			// Skip on DB error, log and continue
			continue
		}

		// Also delete the associated stream record.
		if err := streamPkg.Delete(archive.StreamID, user.Subject); err != nil {
			// Continue on error — archive is already deleted.
			_ = err
		}

		// Delete all shared links for this stream and archive.
		_ = viewer.DeleteForStream(archive.StreamID)
		_ = viewer.DeleteForArchive(archive.ID)

		// Clean up HLS/DASH segments from disk.
		if liveDir != "" {
			if err := os.RemoveAll(liveDir); err != nil {
				// Non-fatal: log and continue.
				_ = err
			}
		}

		if archive.FilePath != "" {
			os.Remove(archive.FilePath) // best-effort; log if it fails
		}
	}

	c.Status(http.StatusNoContent)
}

// getByID is a helper that loads an archive or returns a not-found error.
func getByID(id string) (*Archive, error) {
	var a Archive
	err := db.DB.First(&a, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, gorm.ErrRecordNotFound
	}
	return &a, err
}

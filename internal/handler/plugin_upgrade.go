package handler

import (
	"io"
	"net/http"
	"strings"

	pluginhost "github.com/Tencent/WeKnora/internal/plugin"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func (h *PluginHandler) Upgrade(c *gin.Context) {
	if h == nil || h.manager == nil || h.db == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "plugin upgrade service is unavailable"})
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, pluginhost.MaxPluginArchiveBytes+(1<<20))
	defer func() {
		if c.Request.MultipartForm != nil {
			_ = c.Request.MultipartForm.RemoveAll()
		}
	}()
	file, _, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "a ZIP file up to 64 MB is required"})
		return
	}
	defer file.Close()
	archive, err := io.ReadAll(io.LimitReader(file, pluginhost.MaxPluginArchiveBytes+1))
	if err != nil || int64(len(archive)) > pluginhost.MaxPluginArchiveBytes {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "cannot read plugin ZIP; maximum size is 64 MB"})
		return
	}
	var renamed int64
	manifest, err := h.manager.UpgradeArchive(c.Param("plugin_id"), archive, func(old, next *pluginhost.Manifest) error {
		return h.db.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
			var migrationErr error
			renamed, migrationErr = renamePluginDataSources(tx, old, next)
			return migrationErr
		})
	})
	if err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	status, _ := h.manager.Status(manifest.Metadata.ID)
	c.JSON(http.StatusOK, gin.H{"plugin": status, "renamed_data_sources": renamed})
}

// Exact old-default matching is the legacy naming contract. Only the name
// column changes; encrypted credentials, cursors and sync state are untouched.
func renamePluginDataSources(tx *gorm.DB, old, next *pluginhost.Manifest) (int64, error) {
	var renamed int64
	for _, before := range old.Spec.ExtensionPoints {
		if before.Type != pluginhost.ExtensionDatasource {
			continue
		}
		oldName := strings.TrimSpace(before.Name)
		if oldName == "" {
			oldName = old.Metadata.Name
		}
		for _, after := range next.Spec.ExtensionPoints {
			if after.ID != before.ID || after.Type != before.Type {
				continue
			}
			newName := strings.TrimSpace(after.Name)
			if newName == "" {
				newName = next.Metadata.Name
			}
			if oldName == newName {
				continue
			}
			result := tx.Model(&types.DataSource{}).Where("type = ? AND name = ?", before.ID, oldName).UpdateColumn("name", newName)
			if result.Error != nil {
				return 0, result.Error
			}
			renamed += result.RowsAffected
		}
	}
	return renamed, nil
}

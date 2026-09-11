package router

import (
	"github.com/Tencent/WeKnora/internal/handler"
	"github.com/gin-gonic/gin"
)

// RegisterPluginAdminRoutes mounts process-wide plugin state behind the same
// SystemAdmin guard used by other platform runtime inspection endpoints.
func RegisterPluginAdminRoutes(r *gin.RouterGroup, pluginHandler *handler.PluginHandler, g *rbacGuards) {
	if pluginHandler == nil {
		return
	}
	admin := r.Group("/system/admin/plugins", g.SystemAdmin())
	admin.GET("", pluginHandler.List)
	admin.POST("", pluginHandler.Install)
	admin.POST("/:plugin_id/upgrade", pluginHandler.Upgrade)
	admin.POST("/:plugin_id/enable", pluginHandler.Enable)
	admin.POST("/:plugin_id/disable", pluginHandler.Disable)
}

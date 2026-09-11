package handler

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/infrastructure/docparser"
	infra_web_search "github.com/Tencent/WeKnora/internal/infrastructure/web_search"
	"github.com/Tencent/WeKnora/internal/models/provider"
	pluginhost "github.com/Tencent/WeKnora/internal/plugin"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// PluginHandler exposes the process-wide plugin manager to system
// administrators.
type PluginHandler struct {
	mu                sync.Mutex
	manager           *pluginhost.Manager
	registry          *datasource.ConnectorRegistry
	webSearchRegistry *infra_web_search.Registry
	retrievalRegistry interfaces.ExternalRetrieveEngineRegistry
	db                *gorm.DB
}

func NewPluginHandlerWithUpgrades(manager *pluginhost.Manager, registry *datasource.ConnectorRegistry, webSearchRegistry *infra_web_search.Registry, retrievalRegistry interfaces.ExternalRetrieveEngineRegistry, db *gorm.DB) *PluginHandler {
	h := NewPluginHandler(manager, registry, webSearchRegistry, retrievalRegistry)
	h.db = db
	return h
}

func NewPluginHandler(manager *pluginhost.Manager, registry *datasource.ConnectorRegistry, webSearchRegistry *infra_web_search.Registry, retrievalRegistry interfaces.ExternalRetrieveEngineRegistry) *PluginHandler {
	return &PluginHandler{manager: manager, registry: registry, webSearchRegistry: webSearchRegistry, retrievalRegistry: retrievalRegistry}
}

// List returns deterministic plugin status ordered by plugin ID.
func (h *PluginHandler) List(c *gin.Context) {
	if h == nil || h.manager == nil {
		c.JSON(http.StatusOK, []pluginhost.Status{})
		return
	}
	c.JSON(http.StatusOK, h.manager.Statuses())
}

// Install accepts one administrator-supplied ZIP package. The router applies
// the system-administrator guard before this handler is reached.
func (h *PluginHandler) Install(c *gin.Context) {
	if h == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "plugin manager is unavailable"})
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.manager == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "plugin manager is unavailable"})
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, pluginhost.MaxPluginArchiveBytes+(1<<20))
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": fmt.Sprintf("plugin bundle cannot exceed %d MB", pluginhost.MaxPluginArchiveBytes>>20)})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": "file is required"})
		return
	}
	defer func() { _ = file.Close() }()
	if header.Size > pluginhost.MaxPluginArchiveBytes {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": fmt.Sprintf("plugin bundle cannot exceed %d MB", pluginhost.MaxPluginArchiveBytes>>20)})
		return
	}
	archive, err := io.ReadAll(io.LimitReader(file, pluginhost.MaxPluginArchiveBytes+1))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read plugin bundle"})
		return
	}
	if int64(len(archive)) > pluginhost.MaxPluginArchiveBytes {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": fmt.Sprintf("plugin bundle cannot exceed %d MB", pluginhost.MaxPluginArchiveBytes>>20)})
		return
	}
	manifest, err := h.manager.InstallArchive(archive, pluginhost.DefaultInstallDir())
	if err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	status, _ := h.manager.Status(manifest.Metadata.ID)
	c.JSON(http.StatusCreated, status)
}

func (h *PluginHandler) Disable(c *gin.Context) {
	h.mu.Lock()
	defer h.mu.Unlock()
	pluginID := c.Param("plugin_id")
	if h.manager.IsBuiltin(pluginID) {
		if _, err := h.manager.Disable(pluginID); err != nil {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		status, _ := h.manager.Status(pluginID)
		c.JSON(http.StatusOK, status)
		return
	}
	connectorTypes := h.manager.DatasourceTypes(pluginID)
	webSearchTypes := h.manager.WebSearchTypes(pluginID)
	documentParserTypes := h.manager.DocumentParserTypes(pluginID)
	modelProviderTypes := h.manager.ModelProviderTypes(pluginID)
	retrievalEngineTypes := h.manager.RetrievalEngineTypes(pluginID)
	if len(connectorTypes) == 0 && len(webSearchTypes) == 0 && len(documentParserTypes) == 0 && len(modelProviderTypes) == 0 && len(retrievalEngineTypes) == 0 {
		c.JSON(http.StatusConflict, gin.H{"error": "plugin is not running"})
		return
	}
	if len(connectorTypes) > 0 {
		if err := h.registry.UnregisterAll(connectorTypes); err != nil {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
	}
	for _, connectorType := range connectorTypes {
		datasource.UnregisterConnectorMetadata(connectorType)
	}
	for _, providerType := range webSearchTypes {
		h.webSearchRegistry.Unregister(providerType)
	}
	for _, engineName := range documentParserTypes {
		if err := docparser.UnregisterExternalEngine(engineName); err != nil {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
	}
	for _, providerName := range modelProviderTypes {
		if err := provider.UnregisterExternal(provider.ProviderName(providerName)); err != nil {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
	}
	for _, engineType := range retrievalEngineTypes {
		engine := types.RetrieverEngineType(engineType)
		if err := h.retrievalRegistry.UnregisterExternal(engine); err != nil {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		types.UnregisterExternalRetrieverEngine(engine)
	}
	if _, err := h.manager.Disable(pluginID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	status, _ := h.manager.Status(pluginID)
	c.JSON(http.StatusOK, status)
}

func (h *PluginHandler) Enable(c *gin.Context) {
	h.mu.Lock()
	defer h.mu.Unlock()
	pluginID := c.Param("plugin_id")
	if state, ok := h.manager.Status(pluginID); ok && state.HTTPPolicyDigest != "" && !state.HTTPApproved {
		var approval struct {
			Digest string `json:"http_policy_digest"`
		}
		if err := c.ShouldBindJSON(&approval); err != nil {
			c.JSON(http.StatusForbidden, gin.H{"error": "review and approve HTTP permissions before enabling"})
			return
		}
		if err := h.manager.ApproveHTTP(pluginID, approval.Digest); err != nil {
			c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
			return
		}
	}
	loaded, err := h.manager.Enable(c.Request.Context(), pluginID)
	if err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	var registered []string
	for _, external := range loaded.Datasources {
		if err := h.registry.Register(external.Connector); err != nil {
			h.rollbackEnable(pluginID, registered, nil, nil, nil, nil)
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		if err := datasource.RegisterConnectorMetadata(external.Metadata); err != nil {
			_ = h.registry.Unregister(external.Metadata.Type)
			h.rollbackEnable(pluginID, registered, nil, nil, nil, nil)
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		registered = append(registered, external.Metadata.Type)
	}
	var webRegistered []string
	for _, external := range loaded.WebSearches {
		if err := h.webSearchRegistry.RegisterExternal(external.Point.ID, external.Connector.Factory(), external.TypeInfo()); err != nil {
			h.rollbackEnable(pluginID, registered, webRegistered, nil, nil, nil)
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		webRegistered = append(webRegistered, external.Point.ID)
	}
	var parserRegistered []string
	for _, external := range loaded.DocumentParsers {
		if err := docparser.RegisterExternalEngine(external.Connector); err != nil {
			h.rollbackEnable(pluginID, registered, webRegistered, parserRegistered, nil, nil)
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		parserRegistered = append(parserRegistered, external.Point.ID)
	}
	var modelRegistered []string
	for _, external := range loaded.ModelProviders {
		if err := provider.RegisterExternal(external.Connector); err != nil {
			h.rollbackEnable(pluginID, registered, webRegistered, parserRegistered, modelRegistered, nil)
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		modelRegistered = append(modelRegistered, external.Point.ID)
	}
	var retrievalRegistered []string
	for _, external := range loaded.RetrievalEngines {
		if err := types.RegisterExternalRetrieverEngine(external.Connector.EngineType(), external.Connector.Support()); err != nil {
			h.rollbackEnable(pluginID, registered, webRegistered, parserRegistered, modelRegistered, retrievalRegistered)
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		if err := h.retrievalRegistry.RegisterExternal(external.Connector); err != nil {
			types.UnregisterExternalRetrieverEngine(external.Connector.EngineType())
			h.rollbackEnable(pluginID, registered, webRegistered, parserRegistered, modelRegistered, retrievalRegistered)
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		retrievalRegistered = append(retrievalRegistered, external.Point.ID)
	}
	status, _ := h.manager.Status(pluginID)
	c.JSON(http.StatusOK, status)
}

func (h *PluginHandler) rollbackEnable(pluginID string, connectorTypes, webSearchTypes, documentParserTypes, modelProviderTypes, retrievalEngineTypes []string) {
	for _, connectorType := range connectorTypes {
		_ = h.registry.Unregister(connectorType)
		datasource.UnregisterConnectorMetadata(connectorType)
	}
	for _, providerType := range webSearchTypes {
		h.webSearchRegistry.Unregister(providerType)
	}
	for _, engineName := range documentParserTypes {
		_ = docparser.UnregisterExternalEngine(engineName)
	}
	for _, providerName := range modelProviderTypes {
		_ = provider.UnregisterExternal(provider.ProviderName(providerName))
	}
	for _, engineType := range retrievalEngineTypes {
		engine := types.RetrieverEngineType(engineType)
		_ = h.retrievalRegistry.UnregisterExternal(engine)
		types.UnregisterExternalRetrieverEngine(engine)
	}
	_, _ = h.manager.Disable(pluginID)
}

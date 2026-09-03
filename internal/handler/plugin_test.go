package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/datasource"
	infra_web_search "github.com/Tencent/WeKnora/internal/infrastructure/web_search"
	pluginhost "github.com/Tencent/WeKnora/internal/plugin"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestPluginHandlerListReturnsArray(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/plugins", NewPluginHandler(
		pluginhost.NewManager("0.7.2", nil), datasource.NewConnectorRegistry(), infra_web_search.NewRegistry(), nil,
	).List)

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/plugins", nil))
	require.Equal(t, http.StatusOK, response.Code)
	require.JSONEq(t, `[]`, response.Body.String())
}

func TestPluginHandlerDisablesAndEnablesBuiltin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	manager := pluginhost.NewManager("0.7.2", nil)
	running := false
	require.NoError(t, manager.RegisterBuiltin(context.Background(), pluginhost.BuiltinRegistration{
		PluginID: "builtin.datasource.test", Name: "Test",
		ExtensionType: pluginhost.ExtensionDatasource, ExtensionID: "test",
		Enable:  func(context.Context) error { running = true; return nil },
		Disable: func() error { running = false; return nil },
	}))
	handler := NewPluginHandler(manager, datasource.NewConnectorRegistry(), infra_web_search.NewRegistry(), nil)
	router := gin.New()
	router.POST("/plugins/:plugin_id/disable", handler.Disable)
	router.POST("/plugins/:plugin_id/enable", handler.Enable)

	disable := httptest.NewRecorder()
	router.ServeHTTP(disable, httptest.NewRequest(http.MethodPost, "/plugins/builtin.datasource.test/disable", nil))
	require.Equal(t, http.StatusOK, disable.Code)
	require.False(t, running)

	enable := httptest.NewRecorder()
	router.ServeHTTP(enable, httptest.NewRequest(http.MethodPost, "/plugins/builtin.datasource.test/enable", nil))
	require.Equal(t, http.StatusOK, enable.Code)
	require.True(t, running)
}

func TestPluginHandlerDisableRejectsPluginThatIsNotRunning(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := NewPluginHandler(pluginhost.NewManager("0.7.2", nil), datasource.NewConnectorRegistry(), infra_web_search.NewRegistry(), nil)
	router := gin.New()
	router.POST("/plugins/:plugin_id/disable", handler.Disable)

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/plugins/missing/disable", nil))
	require.Equal(t, http.StatusConflict, response.Code)
}

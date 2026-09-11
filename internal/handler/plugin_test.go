package handler

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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

func TestPluginHandlerInstallsUploadedBundleDisabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("WEKNORA_PLUGIN_INSTALL_DIR", t.TempDir())
	manager := pluginhost.NewManager("0.7.2", nil)
	handler := NewPluginHandler(manager, datasource.NewConnectorRegistry(), infra_web_search.NewRegistry(), nil)
	router := gin.New()
	router.POST("/plugins", handler.Install)

	var archive bytes.Buffer
	zipWriter := zip.NewWriter(&archive)
	manifest, err := zipWriter.Create("plugin.yaml")
	require.NoError(t, err)
	_, err = manifest.Write([]byte(`apiVersion: plugins.weknora.io/v1alpha1
kind: Plugin
metadata: {id: io.weknora.handler-upload, name: Handler Upload, version: 1.0.0}
spec:
  enabled: false
  extensionPoints: [{type: datasource, id: handler_upload, protocolVersion: v1}]
  runtime: {type: oci, image: "example/handler-upload:1.0.0"}
  permissions: {network: {outbound: false}, filesystem: {}}
`))
	require.NoError(t, err)
	require.NoError(t, zipWriter.Close())

	var requestBody bytes.Buffer
	multipartWriter := multipart.NewWriter(&requestBody)
	part, err := multipartWriter.CreateFormFile("file", "plugin.zip")
	require.NoError(t, err)
	_, err = part.Write(archive.Bytes())
	require.NoError(t, err)
	require.NoError(t, multipartWriter.Close())

	request := httptest.NewRequest(http.MethodPost, "/plugins", &requestBody)
	request.Header.Set("Content-Type", multipartWriter.FormDataContentType())
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
	var status pluginhost.Status
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &status))
	require.Equal(t, "io.weknora.handler-upload", status.PluginID)
	require.Equal(t, pluginhost.StateDisabled, status.State)
	require.False(t, status.CheckedAt.IsZero())
	require.FileExists(t, filepath.Join(pluginhost.DefaultInstallDir(), "io.weknora.handler-upload", "plugin.yaml"))
}

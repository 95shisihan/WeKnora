package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/datasource"
	infra_web_search "github.com/Tencent/WeKnora/internal/infrastructure/web_search"
	pluginhost "github.com/Tencent/WeKnora/internal/plugin"
	"github.com/Tencent/WeKnora/plugin/sdk/hosthttp"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestPluginHandlerRequiresExactHTTPApproval(t *testing.T) {
	gin.SetMode(gin.TestMode)
	m := pluginhost.NewManager("0.7.2", nil)
	defer m.Close()
	manifest := &pluginhost.Manifest{Metadata: pluginhost.Metadata{ID: "test.http", Name: "HTTP", Version: "1.0.0"}, Spec: pluginhost.Spec{ExtensionPoints: []pluginhost.ExtensionPoint{{Type: pluginhost.ExtensionDatasource}}, Permissions: pluginhost.Permissions{Network: pluginhost.NetworkPermission{HTTP: &hosthttp.Policy{Rules: []hosthttp.Rule{{Hosts: []string{"example.com"}, Methods: []string{"GET"}}}}}}}}
	require.NoError(t, m.Load(context.Background(), []*pluginhost.Manifest{manifest}))
	h := NewPluginHandler(m, datasource.NewConnectorRegistry(), infra_web_search.NewRegistry(), nil)
	r := gin.New()
	r.POST("/plugins/:plugin_id/enable", h.Enable)
	for _, body := range []string{"", `{}`, `{"http_policy_digest":"stale"}`} {
		response := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/plugins/test.http/enable", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(response, req)
		require.Equal(t, http.StatusForbidden, response.Code)
		require.Empty(t, m.Datasources())
	}
}

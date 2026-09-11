package plugin

import (
	"context"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/models/provider"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	pluginproto "github.com/Tencent/WeKnora/plugin/proto"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
)

type testModelProviderServer struct {
	pluginproto.UnimplementedModelProviderPluginServer
}

func (testModelProviderServer) GetInfo(context.Context, *pluginproto.ModelProviderInfoRequest) (*pluginproto.ModelProviderInfoResponse, error) {
	return &pluginproto.ModelProviderInfoResponse{Id: "dev.example.model", Version: "1.0.0", ProtocolVersion: "v1", ProviderName: "example_model", Transport: "openai_compatible", ModelTypes: []string{"KnowledgeQA"}}, nil
}
func (testModelProviderServer) ValidateConfig(context.Context, *pluginproto.ModelProviderValidateRequest) (*pluginproto.ModelProviderValidateResponse, error) {
	return &pluginproto.ModelProviderValidateResponse{}, nil
}

func TestManagerLoadsAndDisablesModelProviderPlugin(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	grpcServer := grpc.NewServer()
	pluginproto.RegisterModelProviderPluginServer(grpcServer, testModelProviderServer{})
	healthServer := health.NewServer()
	healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)
	grpc_health_v1.RegisterHealthServer(grpcServer, healthServer)
	go func() { _ = grpcServer.Serve(listener) }()
	t.Cleanup(grpcServer.Stop)

	manager := NewManager("0.7.2", nil)
	defer manager.Close()
	manifest := &Manifest{
		Metadata: Metadata{ID: "dev.example.model", Name: "Example Model", Version: "1.0.0"},
		Spec: Spec{
			ExtensionPoints: []ExtensionPoint{{Type: ExtensionModelProvider, ID: "example_model", ProtocolVersion: "v1"}},
			Runtime:         Runtime{Type: "grpc", Address: listener.Addr().String(), StartupTimeout: 2 * time.Second},
			Permissions:     Permissions{Network: NetworkPermission{Outbound: true}},
		},
	}
	require.NoError(t, manager.Load(context.Background(), []*Manifest{manifest}))
	require.Equal(t, []string{"example_model"}, manager.ModelProviderTypes(manifest.Metadata.ID))
	require.NoError(t, provider.RegisterExternal(manager.ModelProviders()[0].Connector))
	defer provider.UnregisterExternal("example_model")
	// A subsequently discovered disabled declaration must not shadow an active
	// legacy provider with the same name.
	provider.ReserveInference("example_model")
	legacyHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"legacy still works"},"finish_reason":"stop"}],"usage":{"total_tokens":1}}`))
	}))
	defer legacyHTTP.Close()
	legacy, err := chat.NewRemoteChat(&chat.ChatConfig{Provider: "example_model", ModelName: "legacy", BaseURL: legacyHTTP.URL + "/v1", APIKey: "test"})
	require.NoError(t, err)
	answer, err := legacy.Chat(context.Background(), []chat.Message{{Role: "user", Content: "hello"}}, nil)
	require.NoError(t, err)
	require.Equal(t, "legacy still works", answer.Content)
	disabled, err := manager.Disable(manifest.Metadata.ID)
	require.NoError(t, err)
	require.Equal(t, []string{"example_model"}, disabled.ModelProviderTypes)
}

func TestDisabledModelPluginNeverFallsBackAtStartup(t *testing.T) {
	manager := NewManager("0.7.2", nil)
	defer manager.Close()
	disabled := false
	manifest := &Manifest{Metadata: Metadata{ID: "test.disabled-model", Version: "1"}, Spec: Spec{Enabled: &disabled, ExtensionPoints: []ExtensionPoint{{Type: ExtensionModelProvider, ID: "disabled_inference"}}}}
	require.NoError(t, manager.Load(context.Background(), []*Manifest{manifest}))
	_, err := chat.NewRemoteChat(&chat.ChatConfig{Provider: "disabled_inference", BaseURL: "http://127.0.0.1:1", APIKey: "must-not-go-to-host-http"})
	require.ErrorContains(t, err, "disabled or unavailable")
}

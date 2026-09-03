package plugin

import (
	"context"
	"net"
	"testing"
	"time"

	pluginproto "github.com/Tencent/WeKnora/plugin/proto"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
)

type testWebSearchServer struct {
	pluginproto.UnimplementedWebSearchPluginServer
}

func (testWebSearchServer) GetInfo(context.Context, *pluginproto.WebSearchInfoRequest) (*pluginproto.WebSearchInfoResponse, error) {
	return &pluginproto.WebSearchInfoResponse{Id: "dev.example.search", Version: "1.0.0", ProtocolVersion: "v1", ProviderType: "example_search"}, nil
}

func (testWebSearchServer) Search(context.Context, *pluginproto.WebSearchRequest) (*pluginproto.WebSearchResponse, error) {
	return &pluginproto.WebSearchResponse{}, nil
}

func TestManagerLoadsAndDisablesWebSearchPlugin(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	grpcServer := grpc.NewServer()
	pluginproto.RegisterWebSearchPluginServer(grpcServer, testWebSearchServer{})
	healthServer := health.NewServer()
	healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)
	grpc_health_v1.RegisterHealthServer(grpcServer, healthServer)
	go func() { _ = grpcServer.Serve(listener) }()
	t.Cleanup(grpcServer.Stop)

	manager := NewManager("0.7.2", nil)
	manifest := &Manifest{
		Metadata: Metadata{ID: "dev.example.search", Name: "Example Search", Version: "1.0.0"},
		Spec: Spec{
			ExtensionPoints: []ExtensionPoint{{Type: ExtensionWebSearch, ID: "example_search", ProtocolVersion: "v1"}},
			Runtime:         Runtime{Type: "grpc", Address: listener.Addr().String(), StartupTimeout: 2 * time.Second},
			Permissions:     Permissions{Network: NetworkPermission{Outbound: true}},
		},
	}
	require.NoError(t, manager.Load(context.Background(), []*Manifest{manifest}))
	require.Equal(t, []string{"example_search"}, manager.WebSearchTypes(manifest.Metadata.ID))
	status, ok := manager.Status(manifest.Metadata.ID)
	require.True(t, ok)
	require.Equal(t, StateHealthy, status.State)

	disabled, err := manager.Disable(manifest.Metadata.ID)
	require.NoError(t, err)
	require.Equal(t, []string{"example_search"}, disabled.WebSearchTypes)
	status, _ = manager.Status(manifest.Metadata.ID)
	require.Equal(t, StateDisabled, status.State)
}

func TestWebSearchTypeInfoComesFromManifestSchema(t *testing.T) {
	loaded := &LoadedWebSearch{
		Manifest: &Manifest{Metadata: Metadata{Name: "External Search"}, Spec: Spec{ConfigSchema: map[string]any{
			"required": []any{"api_key", "region"},
			"properties": map[string]any{
				"api_key": map[string]any{"type": "string"},
				"region":  map[string]any{"type": "string", "title": "Region", "enum": []any{"cn", "global"}},
			},
		}}},
		Point: ExtensionPoint{ID: "external_search"},
	}
	info := loaded.TypeInfo()
	require.Equal(t, "external_search", info.ID)
	require.Equal(t, "External Search", info.Name)
	require.True(t, info.RequiresAPIKey)
	require.Len(t, info.ConfigFields, 1)
	require.Equal(t, "select", info.ConfigFields[0].Type)
	require.True(t, info.ConfigFields[0].Required)
}

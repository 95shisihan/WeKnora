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
	disabled, err := manager.Disable(manifest.Metadata.ID)
	require.NoError(t, err)
	require.Equal(t, []string{"example_model"}, disabled.ModelProviderTypes)
}

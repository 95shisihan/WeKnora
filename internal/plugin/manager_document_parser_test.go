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

type testDocumentParserServer struct {
	pluginproto.UnimplementedDocumentParserPluginServer
}

func (testDocumentParserServer) GetInfo(context.Context, *pluginproto.DocumentParserInfoRequest) (*pluginproto.DocumentParserInfoResponse, error) {
	return &pluginproto.DocumentParserInfoResponse{Id: "dev.example.parser", Version: "1.0.0", ProtocolVersion: "v1", EngineName: "example_parser", FileTypes: []string{"txt"}}, nil
}
func (testDocumentParserServer) Parse(*pluginproto.DocumentParseRequest, grpc.ServerStreamingServer[pluginproto.DocumentParseFrame]) error {
	return nil
}

func TestManagerLoadsAndDisablesDocumentParserPlugin(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	grpcServer := grpc.NewServer()
	pluginproto.RegisterDocumentParserPluginServer(grpcServer, testDocumentParserServer{})
	healthServer := health.NewServer()
	healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)
	grpc_health_v1.RegisterHealthServer(grpcServer, healthServer)
	go func() { _ = grpcServer.Serve(listener) }()
	t.Cleanup(grpcServer.Stop)

	manager := NewManager("0.7.2", nil)
	manifest := &Manifest{
		Metadata: Metadata{ID: "dev.example.parser", Name: "Example Parser", Version: "1.0.0"},
		Spec: Spec{
			ExtensionPoints: []ExtensionPoint{{Type: ExtensionDocumentParser, ID: "example_parser", ProtocolVersion: "v1"}},
			Runtime:         Runtime{Type: "grpc", Address: listener.Addr().String(), StartupTimeout: 2 * time.Second},
			Permissions:     Permissions{Network: NetworkPermission{Outbound: true}},
		},
	}
	require.NoError(t, manager.Load(context.Background(), []*Manifest{manifest}))
	require.Equal(t, []string{"example_parser"}, manager.DocumentParserTypes(manifest.Metadata.ID))
	status, ok := manager.Status(manifest.Metadata.ID)
	require.True(t, ok)
	require.Equal(t, StateHealthy, status.State)

	disabled, err := manager.Disable(manifest.Metadata.ID)
	require.NoError(t, err)
	require.Equal(t, []string{"example_parser"}, disabled.DocumentParserTypes)
}

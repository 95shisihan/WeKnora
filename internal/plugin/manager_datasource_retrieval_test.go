package plugin

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	pluginproto "github.com/Tencent/WeKnora/plugin/proto"
	retrievalsdk "github.com/Tencent/WeKnora/plugin/sdk/retrieval"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

type managerDatasourceServer struct {
	pluginproto.UnimplementedDatasourcePluginServer
}

func (managerDatasourceServer) GetInfo(
	context.Context, *pluginproto.GetInfoRequest,
) (*pluginproto.GetInfoResponse, error) {
	return &pluginproto.GetInfoResponse{
		Id: "dev.example.datasource", Version: "1.0.0",
		ProtocolVersion: "v1", ConnectorType: "example_datasource",
	}, nil
}

type managerRetrievalServer struct {
	pluginproto.UnimplementedRetrievalEnginePluginServer
}

func (managerRetrievalServer) GetInfo(
	context.Context, *emptypb.Empty,
) (*wrapperspb.BytesValue, error) {
	value, err := json.Marshal(retrievalsdk.Info{
		ID: "dev.example.retrieval", Version: "1.0.0",
		ProtocolVersion: "v1", EngineType: "example_retrieval",
		Support: []string{"keywords", "vector"},
	})
	if err != nil {
		return nil, err
	}
	return wrapperspb.Bytes(value), nil
}

func startManagerTestServer(t *testing.T, register func(*grpc.Server)) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := grpc.NewServer()
	register(server)
	healthServer := health.NewServer()
	healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)
	grpc_health_v1.RegisterHealthServer(server, healthServer)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	return listener.Addr().String()
}

func TestManagerLoadsDisablesAndEnablesDatasourcePlugin(t *testing.T) {
	address := startManagerTestServer(t, func(server *grpc.Server) {
		pluginproto.RegisterDatasourcePluginServer(server, managerDatasourceServer{})
	})
	manager := NewManager("0.7.2", nil)
	t.Cleanup(func() { _ = manager.Close() })
	manifest := &Manifest{
		Metadata: Metadata{ID: "dev.example.datasource", Name: "Example Datasource", Version: "1.0.0"},
		Spec: Spec{
			ExtensionPoints: []ExtensionPoint{{Type: ExtensionDatasource, ID: "example_datasource", ProtocolVersion: "v1"}},
			Runtime:         Runtime{Type: "grpc", Address: address, StartupTimeout: 2 * time.Second},
			Permissions:     Permissions{Network: NetworkPermission{Outbound: true}},
		},
	}
	require.NoError(t, manager.Load(context.Background(), []*Manifest{manifest}))
	require.Equal(t, []string{"example_datasource"}, manager.DatasourceTypes(manifest.Metadata.ID))

	disabled, err := manager.Disable(manifest.Metadata.ID)
	require.NoError(t, err)
	require.Equal(t, []string{"example_datasource"}, disabled.DatasourceTypes)
	status, ok := manager.Status(manifest.Metadata.ID)
	require.True(t, ok)
	require.Equal(t, StateDisabled, status.State)

	enabled, err := manager.Enable(context.Background(), manifest.Metadata.ID)
	require.NoError(t, err)
	require.Len(t, enabled.Datasources, 1)
	status, _ = manager.Status(manifest.Metadata.ID)
	require.Equal(t, StateHealthy, status.State)
}

func TestManagerLoadsDisablesAndEnablesRetrievalPlugin(t *testing.T) {
	address := startManagerTestServer(t, func(server *grpc.Server) {
		pluginproto.RegisterRetrievalEnginePluginServer(server, managerRetrievalServer{})
	})
	manager := NewManager("0.7.2", nil)
	t.Cleanup(func() { _ = manager.Close() })
	manifest := &Manifest{
		Metadata: Metadata{ID: "dev.example.retrieval", Name: "Example Retrieval", Version: "1.0.0"},
		Spec: Spec{
			ExtensionPoints: []ExtensionPoint{{Type: ExtensionRetrievalEngine, ID: "example_retrieval", ProtocolVersion: "v1"}},
			Runtime:         Runtime{Type: "grpc", Address: address, StartupTimeout: 2 * time.Second},
			Permissions:     Permissions{Network: NetworkPermission{Outbound: true}},
		},
	}
	require.NoError(t, manager.Load(context.Background(), []*Manifest{manifest}))
	require.Equal(t, []string{"example_retrieval"}, manager.RetrievalEngineTypes(manifest.Metadata.ID))

	disabled, err := manager.Disable(manifest.Metadata.ID)
	require.NoError(t, err)
	require.Equal(t, []string{"example_retrieval"}, disabled.RetrievalEngineTypes)

	enabled, err := manager.Enable(context.Background(), manifest.Metadata.ID)
	require.NoError(t, err)
	require.Len(t, enabled.RetrievalEngines, 1)
	status, ok := manager.Status(manifest.Metadata.ID)
	require.True(t, ok)
	require.Equal(t, StateHealthy, status.State)
}

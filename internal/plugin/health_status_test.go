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

// Every server returns valid GetInfo data, so an unhealthy plugin cannot be
// rejected merely because its identity RPC failed.
func TestPluginHealthRequiresServing(t *testing.T) {
	cases := []struct {
		extensionType string
		pluginID      string
		extensionID   string
		register      func(*grpc.Server)
	}{
		{ExtensionDatasource, "dev.example.datasource", "example_datasource", func(s *grpc.Server) {
			pluginproto.RegisterDatasourcePluginServer(s, managerDatasourceServer{})
		}},
		{ExtensionDocumentParser, "dev.example.parser", "example_parser", func(s *grpc.Server) {
			pluginproto.RegisterDocumentParserPluginServer(s, testDocumentParserServer{})
		}},
		{ExtensionWebSearch, "dev.example.search", "example_search", func(s *grpc.Server) {
			pluginproto.RegisterWebSearchPluginServer(s, testWebSearchServer{})
		}},
		{ExtensionModelProvider, "dev.example.model", "example_model", func(s *grpc.Server) {
			pluginproto.RegisterModelProviderPluginServer(s, testModelProviderServer{})
		}},
		{ExtensionRetrievalEngine, "dev.example.retrieval", "example_retrieval", func(s *grpc.Server) {
			pluginproto.RegisterRetrievalEnginePluginServer(s, managerRetrievalServer{})
		}},
	}
	for _, tc := range cases {
		t.Run(tc.extensionType, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			server := grpc.NewServer()
			tc.register(server)
			healthServer := health.NewServer()
			grpc_health_v1.RegisterHealthServer(server, healthServer)
			go func() { _ = server.Serve(listener) }()
			t.Cleanup(server.Stop)
			manifest := &Manifest{
				Metadata: Metadata{ID: tc.pluginID, Name: tc.extensionID, Version: "1.0.0"},
				Spec: Spec{
					ExtensionPoints: []ExtensionPoint{{Type: tc.extensionType, ID: tc.extensionID, ProtocolVersion: "v1"}},
					Runtime:         Runtime{Type: "grpc", Address: listener.Addr().String(), StartupTimeout: 150 * time.Millisecond},
					Permissions:     Permissions{Network: NetworkPermission{Outbound: true}},
				},
			}
			for _, state := range []grpc_health_v1.HealthCheckResponse_ServingStatus{
				grpc_health_v1.HealthCheckResponse_NOT_SERVING,
				grpc_health_v1.HealthCheckResponse_UNKNOWN,
				grpc_health_v1.HealthCheckResponse_SERVICE_UNKNOWN,
				grpc_health_v1.HealthCheckResponse_ServingStatus(99),
			} {
				t.Run("startup_"+state.String(), func(t *testing.T) {
					healthServer.SetServingStatus("", state)
					manager := NewManager("0.7.2", nil)
					t.Cleanup(func() { require.NoError(t, manager.Close()) })
					err := manager.Load(context.Background(), []*Manifest{manifest})
					require.ErrorContains(t, err, "health check")
					require.ErrorContains(t, err, state.String())
					status, ok := manager.Status(tc.pluginID)
					require.True(t, ok)
					require.Equal(t, StateUnhealthy, status.State)
				})
			}

			t.Run("runtime_failure_and_recovery", func(t *testing.T) {
				healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)
				manager := NewManager("0.7.2", nil)
				t.Cleanup(func() { require.NoError(t, manager.Close()) })
				require.NoError(t, manager.Load(context.Background(), []*Manifest{manifest}))
				status, ok := manager.Status(tc.pluginID)
				require.True(t, ok)
				require.Equal(t, StateHealthy, status.State)
				manager.StartHealthChecks(10 * time.Millisecond)
				healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_NOT_SERVING)
				require.Eventually(t, func() bool {
					status, _ := manager.Status(tc.pluginID)
					return status.State == StateUnhealthy
				}, 2*time.Second, 10*time.Millisecond)
				status, _ = manager.Status(tc.pluginID)
				require.Contains(t, status.LastError, "NOT_SERVING")

				healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)
				require.Eventually(t, func() bool {
					status, _ := manager.Status(tc.pluginID)
					return status.State == StateHealthy && status.LastError == ""
				}, 2*time.Second, 10*time.Millisecond)
			})
		})
	}
}

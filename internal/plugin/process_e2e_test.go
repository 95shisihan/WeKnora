package plugin

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	pluginproto "github.com/Tencent/WeKnora/plugin/proto"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"gopkg.in/yaml.v3"
)

const processE2EConnectorType = "process_e2e_directory"

type processE2EDatasource struct {
	pluginproto.UnimplementedDatasourcePluginServer
}

func (processE2EDatasource) GetInfo(context.Context, *pluginproto.GetInfoRequest) (*pluginproto.GetInfoResponse, error) {
	return &pluginproto.GetInfoResponse{
		Id: os.Getenv("WEKNORA_PLUGIN_ID"), Version: os.Getenv("WEKNORA_PLUGIN_VERSION"),
		ProtocolVersion: "v1", ConnectorType: processE2EConnectorType,
	}, nil
}

func (processE2EDatasource) Validate(context.Context, *pluginproto.ConfigRequest) (*pluginproto.Empty, error) {
	return &pluginproto.Empty{}, nil
}

func (processE2EDatasource) ListResources(context.Context, *pluginproto.ListResourcesRequest) (*pluginproto.ListResourcesResponse, error) {
	return &pluginproto.ListResourcesResponse{}, nil
}

func (processE2EDatasource) ResolveResourceAncestors(context.Context, *pluginproto.ResolveResourceAncestorsRequest) (*pluginproto.ResolveResourceAncestorsResponse, error) {
	return &pluginproto.ResolveResourceAncestorsResponse{}, nil
}

func (processE2EDatasource) Fetch(request *pluginproto.FetchRequest, stream grpc.ServerStreamingServer[pluginproto.FetchEvent]) error {
	items := []*pluginproto.FetchedItem{
		{ExternalId: "one.txt", FileName: "one.txt", Content: []byte("one")},
		{ExternalId: "two.txt", FileName: "two.txt", Content: []byte("two")},
	}
	if !request.GetFull() && len(request.GetCursorJson()) > 0 {
		items = []*pluginproto.FetchedItem{{ExternalId: "two.txt", FileName: "two.txt", Content: []byte("two changed")}}
	}
	for _, item := range items {
		if err := stream.Send(&pluginproto.FetchEvent{Payload: &pluginproto.FetchEvent_Item{Item: item}}); err != nil {
			return err
		}
	}
	cursor, _ := json.Marshal(&types.SyncCursor{ConnectorCursor: map[string]any{"revision": float64(1)}})
	return stream.Send(&pluginproto.FetchEvent{Payload: &pluginproto.FetchEvent_FinalCursorJson{FinalCursorJson: cursor}})
}

// TestExternalDatasourceHelperProcess is launched by the real process-boundary
// acceptance test below. It is not a mock transport: the manager starts this
// test binary from an independently discovered plugin directory and connects
// over a TCP gRPC endpoint exactly like plugin.dev.yaml on Windows.
func TestExternalDatasourceHelperProcess(t *testing.T) {
	if os.Getenv("WEKNORA_PROCESS_E2E_HELPER") != "1" {
		return
	}
	listener, err := net.Listen("tcp", os.Getenv("WEKNORA_PLUGIN_ADDRESS"))
	if err != nil {
		os.Exit(2)
	}
	server := grpc.NewServer()
	pluginproto.RegisterDatasourcePluginServer(server, processE2EDatasource{})
	healthServer := health.NewServer()
	healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)
	grpc_health_v1.RegisterHealthServer(server, healthServer)
	if err := server.Serve(listener); err != nil {
		os.Exit(3)
	}
}

func TestExternalDirectoryProcessPluginCompletesIncrementalRoundTrip(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	address := listener.Addr().String()
	require.NoError(t, listener.Close())

	executable, err := os.Executable()
	require.NoError(t, err)
	externalRoot := t.TempDir()
	pluginDir := filepath.Join(externalRoot, "process-plugin")
	require.NoError(t, os.Mkdir(pluginDir, 0o700))
	helperName := "plugin-helper" + filepath.Ext(executable)
	require.NoError(t, copyExecutable(executable, filepath.Join(pluginDir, helperName)))
	manifest := Manifest{
		APIVersion: APIVersionV1Alpha1, Kind: KindPlugin,
		Metadata: Metadata{ID: "dev.example.process-e2e", Name: "Process E2E", Version: "1.0.0"},
		Spec: Spec{
			ExtensionPoints: []ExtensionPoint{{Type: ExtensionDatasource, ID: processE2EConnectorType, ProtocolVersion: ProtocolVersionV1}},
			Compatibility:   Compatibility{WeKnora: ">=0.7.0 <0.8.0", PluginAPI: ProtocolVersionV1},
			Runtime:         Runtime{Type: "grpc", Address: address, Command: []string{helperName, "-test.run=^TestExternalDatasourceHelperProcess$"}, StartupTimeoutText: "10s"},
			Permissions:     Permissions{Network: NetworkPermission{Outbound: true}},
		},
	}
	raw, err := yaml.Marshal(manifest)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(pluginDir, "plugin.yaml"), raw, 0o600))
	t.Setenv("WEKNORA_PROCESS_E2E_HELPER", "1")

	manager := NewManager("0.7.2", nil)
	t.Cleanup(func() { require.NoError(t, manager.Close()) })
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	require.NoError(t, manager.LoadDirectories(ctx, []string{externalRoot}))
	require.Len(t, manager.Datasources(), 1)
	connector := manager.Datasources()[0].Connector

	config := &types.DataSourceConfig{Type: processE2EConnectorType}
	first, err := connector.FetchAll(ctx, config, nil)
	require.NoError(t, err)
	require.Len(t, first, 2)
	second, cursor, err := connector.FetchIncremental(ctx, config, &types.SyncCursor{
		ConnectorCursor: map[string]any{"revision": float64(1)},
	})
	require.NoError(t, err)
	require.Len(t, second, 1)
	require.Equal(t, "two.txt", second[0].ExternalID)
	require.NotNil(t, cursor)
	status, ok := manager.Status("dev.example.process-e2e")
	require.True(t, ok)
	require.Equal(t, StateHealthy, status.State)
}

func copyExecutable(source, target string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o700)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

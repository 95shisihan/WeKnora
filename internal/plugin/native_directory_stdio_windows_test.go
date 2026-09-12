//go:build windows && (amd64 || arm64)

package plugin

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/plugin/windowsandbox"
	pluginproto "github.com/Tencent/WeKnora/plugin/proto"
	"github.com/Tencent/WeKnora/plugin/sdk/transport"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"
)

// Exercises the real sample in a native restricted process, over real anonymous
// pipes, including dynamic filesystem permission grants and incremental cursors.
func TestNativeDirectoryStdioIncremental(t *testing.T) {
	if os.Getenv("WEKNORA_WINDOWS_SANDBOX_TEST") != "1" || os.Getenv("WEKNORA_NATIVE_DIRECTORY_EXE") == "" {
		t.Skip("requires WEKNORA_NATIVE_DIRECTORY_EXE and native Windows opt-in")
	}
	dir := t.TempDir()
	exe := filepath.Join(dir, "directory.exe")
	binary, err := os.ReadFile(os.Getenv("WEKNORA_NATIVE_DIRECTORY_EXE"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(exe, binary, 0700))
	data := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(data, "one.md"), []byte("one"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(data, "two.md"), []byte("two"), 0600))
	p, err := windowsandbox.Start(windowsandbox.Config{
		Command: []string{exe}, Directory: dir, ConfigRead: []string{"settings.root"}, Stderr: os.Stderr,
		Environment: append(os.Environ(), "WEKNORA_PLUGIN_ADDRESS=stdio://", "WEKNORA_PLUGIN_ID=io.weknora.local-directory", "WEKNORA_PLUGIN_VERSION=0.1.0"),
	})
	require.NoError(t, err)
	defer p.Close()
	address := "passthrough:///native-directory-test"
	unregister := transport.Register(address, p.Conn, p.PrepareConfig)
	defer unregister()
	conn, err := grpc.NewClient(address, append(transport.Options(address), grpc.WithTransportCredentials(insecure.NewCredentials()))...)
	require.NoError(t, err)
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	health, err := grpc_health_v1.NewHealthClient(conn).Check(ctx, &grpc_health_v1.HealthCheckRequest{})
	require.NoError(t, err)
	require.Equal(t, grpc_health_v1.HealthCheckResponse_SERVING, health.GetStatus())
	client := pluginproto.NewDatasourcePluginClient(conn)
	info, err := client.GetInfo(ctx, &pluginproto.GetInfoRequest{})
	require.NoError(t, err)
	require.Equal(t, "io.weknora.local-directory", info.GetId())
	config, err := json.Marshal(map[string]any{"settings": map[string]any{"root": data}})
	require.NoError(t, err)
	_, err = client.Validate(ctx, &pluginproto.ConfigRequest{ConfigJson: config})
	require.NoError(t, err)
	fetch := func(cursor []byte) ([]string, []byte) {
		stream, err := client.Fetch(ctx, &pluginproto.FetchRequest{ConfigJson: config, Full: len(cursor) == 0, CursorJson: cursor})
		require.NoError(t, err)
		var files []string
		var next []byte
		for {
			event, err := stream.Recv()
			if err == io.EOF {
				break
			}
			require.NoError(t, err)
			if item := event.GetItem(); item != nil {
				files = append(files, item.GetExternalId())
			}
			if raw := event.GetFinalCursorJson(); len(raw) > 0 {
				next = raw
			}
		}
		return files, next
	}
	files, cursor := fetch(nil)
	require.ElementsMatch(t, []string{"one.md", "two.md"}, files)
	require.NotEmpty(t, cursor)
	require.NoError(t, os.WriteFile(filepath.Join(data, "two.md"), []byte("changed"), 0600))
	files, cursor = fetch(cursor)
	require.Equal(t, []string{"two.md"}, files)
	files, _ = fetch(cursor)
	require.Empty(t, files)
	t.Log("native no-network plugin: health, configuration, full sync, one-file incremental sync and unchanged sync passed over stdio")
	require.NoError(t, p.Close())
}

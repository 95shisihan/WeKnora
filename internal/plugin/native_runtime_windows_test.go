//go:build windows && (amd64 || arm64)

package plugin

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestWindowsNativeManagerDirectoryRoundTrip(t *testing.T) {
	source := os.Getenv("WEKNORA_NATIVE_DIRECTORY_EXE")
	if source == "" || os.Getenv("WEKNORA_WINDOWS_SANDBOX_TEST") != "1" {
		t.Skip("build directory plugin and set WEKNORA_NATIVE_DIRECTORY_EXE for native manager acceptance")
	}
	dir := t.TempDir()
	require.NoError(t, copyExecutable(source, filepath.Join(dir, "directory.exe")))
	data := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(data, "one.md"), []byte("one"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(data, "two.md"), []byte("two"), 0600))
	manifest := Manifest{APIVersion: APIVersionV1Alpha1, Kind: KindPlugin, Metadata: Metadata{ID: "io.weknora.local-directory", Name: "Local", Version: "0.1.0"}, Spec: Spec{
		ExtensionPoints: []ExtensionPoint{{Type: ExtensionDatasource, ID: "local_directory", ProtocolVersion: "v1"}},
		Runtime:         Runtime{Type: "grpc", Address: "stdio://", Command: []string{"directory.exe"}, StartupTimeoutText: "15s"},
		Permissions:     Permissions{Network: NetworkPermission{Outbound: false}, Filesystem: FilesystemPermission{Read: []string{"config:settings.root"}}},
	}}
	raw, err := yaml.Marshal(manifest)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "plugin.yaml"), raw, 0600))
	manager := NewManager("0.7.2", nil)
	defer manager.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	require.NoError(t, manager.LoadDirectories(ctx, []string{dir}))
	require.Len(t, manager.Datasources(), 1)
	connector := manager.Datasources()[0].Connector
	config := &types.DataSourceConfig{Type: "local_directory", Settings: map[string]any{"root": data}}
	require.NoError(t, connector.Validate(ctx, config))
	first, cursor, err := connector.FetchIncremental(ctx, config, nil)
	require.NoError(t, err)
	require.Len(t, first, 2)
	require.NoError(t, os.WriteFile(filepath.Join(data, "two.md"), []byte("changed"), 0600))
	second, cursor, err := connector.FetchIncremental(ctx, config, cursor)
	require.NoError(t, err)
	require.Len(t, second, 1)
	require.Equal(t, "two.md", second[0].ExternalID)
	third, _, err := connector.FetchIncremental(ctx, config, cursor)
	require.NoError(t, err)
	require.Empty(t, third)
	state, ok := manager.Status(manifest.Metadata.ID)
	require.True(t, ok)
	require.Equal(t, StateHealthy, state.State)
	_, err = manager.Disable(manifest.Metadata.ID)
	require.NoError(t, err)
	require.Empty(t, manager.Datasources())
	_, err = manager.Enable(ctx, manifest.Metadata.ID)
	require.NoError(t, err)
	require.Len(t, manager.Datasources(), 1)
	require.NoError(t, manager.Datasources()[0].Connector.Health(ctx))
	require.NoError(t, manager.Close())
	t.Log("Manager loaded an external native no-network plugin and completed full/incremental/unchanged sync through stdio")
}

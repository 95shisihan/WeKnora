//go:build windows && (amd64 || arm64)

package plugin

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestWindowsNativePythonPublic(t *testing.T) {
	bundle := os.Getenv("WEKNORA_PYTHON_PUBLIC_BUNDLE")
	link := os.Getenv("WEKNORA_FEISHU_TEST_URL")
	if bundle == "" || link == "" || os.Getenv("WEKNORA_WINDOWS_SANDBOX_TEST") != "1" {
		t.Skip("requires Python public bundle, public URL and sandbox opt-in")
	}
	raw, err := os.ReadFile(bundle)
	require.NoError(t, err)
	manager := NewManager("0.7.2", nil)
	defer manager.Close()
	manifest, err := manager.InstallArchive(raw, t.TempDir())
	require.NoError(t, err)
	require.False(t, manifest.Spec.Permissions.Network.Outbound)
	require.Equal(t, "stdio://", manifest.Spec.Runtime.Address)
	state, ok := manager.Status(manifest.Metadata.ID)
	require.True(t, ok)
	require.False(t, state.HTTPApproved)
	require.NoError(t, manager.ApproveHTTP(state.PluginID, state.HTTPPolicyDigest))
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	_, err = manager.Enable(ctx, state.PluginID)
	require.NoError(t, err)
	c := manager.Datasources()[0].Connector
	config := &types.DataSourceConfig{Type: "feishu_public_plugin", Settings: map[string]any{"public_urls": link}}
	require.NoError(t, c.Validate(ctx, config))
	resources, err := c.ListResources(ctx, config, "")
	require.NoError(t, err)
	require.Len(t, resources, 1)
	require.NotEmpty(t, resources[0].Name)
	first, cursor, err := c.FetchIncremental(ctx, config, nil)
	require.NoError(t, err)
	require.Len(t, first, 1)
	require.NotEmpty(t, first[0].Content)
	second, _, err := c.FetchIncremental(ctx, config, cursor)
	require.NoError(t, err)
	require.Empty(t, second)
	config.Settings["public_urls"] = "https://unapproved.feishu.cn/wiki/test"
	require.ErrorContains(t, c.Validate(ctx, config), "DOMAIN_NOT_ALLOWED")
	require.NoError(t, c.Health(ctx))
	_, err = manager.Disable(state.PluginID)
	require.NoError(t, err)
	_, err = manager.Enable(ctx, state.PluginID)
	require.NoError(t, err)
	require.NoError(t, manager.Datasources()[0].Connector.Health(ctx))
	require.NoError(t, manager.Close())
	t.Log("Frozen Python EXE: stdio health, real Feishu text, resource listing, full/unchanged incremental sync, denied domain and re-enable passed")
}

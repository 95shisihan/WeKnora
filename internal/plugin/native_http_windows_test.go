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
)

func TestWindowsNativeControlledHTTP(t *testing.T) {
	exe := os.Getenv("WEKNORA_CONTROLLED_HTTP_EXE")
	if exe == "" || os.Getenv("WEKNORA_WINDOWS_SANDBOX_TEST") != "1" {
		t.Skip("requires controlled HTTP example EXE and native sandbox opt-in")
	}
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "bin"), 0700))
	require.NoError(t, copyExecutable(exe, filepath.Join(dir, "bin", "weknora-controlled-http.exe")))
	raw, err := os.ReadFile("testdata/controlled-http.yaml")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "plugin.yaml"), raw, 0600))
	manager := NewManager("0.7.2", nil)
	defer manager.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	require.NoError(t, manager.LoadDirectories(ctx, []string{dir}))
	state, ok := manager.Status("io.weknora.controlled-http")
	require.True(t, ok)
	require.Equal(t, StateDisabled, state.State)
	require.Empty(t, manager.Datasources())
	require.NoError(t, manager.ApproveHTTP(state.PluginID, state.HTTPPolicyDigest))
	_, err = manager.Enable(ctx, state.PluginID)
	require.NoError(t, err)
	c := manager.Datasources()[0].Connector
	config := &types.DataSourceConfig{Type: "controlled_http", Settings: map[string]any{"url": "https://unapproved.example.org/"}}
	require.ErrorContains(t, c.Validate(ctx, config), "DOMAIN_NOT_ALLOWED")
	require.NoError(t, c.Health(ctx))
	if link := os.Getenv("WEKNORA_FEISHU_TEST_URL"); link != "" {
		config.Settings["url"] = link
		require.NoError(t, c.Validate(ctx, config))
		t.Log("Real Feishu HTTPS request succeeded through host broker while plugin runs with outbound=false")
	}
	_, err = manager.Disable(state.PluginID)
	require.NoError(t, err)
	_, err = manager.Enable(ctx, state.PluginID)
	require.NoError(t, err)
	c = manager.Datasources()[0].Connector
	config.Settings["url"] = "https://unapproved.example.org/"
	require.ErrorContains(t, c.Validate(ctx, config), "DOMAIN_NOT_ALLOWED")
	require.NoError(t, c.Health(ctx))
	require.NoError(t, manager.Close())
	t.Log("Administrator approval, OS-isolated process, reverse stdio HTTP, denied domain, health and re-enable passed")
}

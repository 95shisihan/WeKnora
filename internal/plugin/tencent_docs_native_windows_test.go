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

// Exercise the shipped ZIP in a disposable installation; never approve the
// user's real installation, whose first-use dialog is the manual test target.
func TestTencentDocsNativeApproval(t *testing.T) {
	archivePath := os.Getenv("WEKNORA_TENCENT_DOCS_ZIP")
	if archivePath == "" || os.Getenv("WEKNORA_WINDOWS_SANDBOX_TEST") != "1" {
		t.Skip("requires Tencent Docs ZIP and Windows sandbox opt-in")
	}
	archive, err := os.ReadFile(archivePath)
	require.NoError(t, err)
	manager := NewManager("0.7.2", nil)
	defer manager.Close()
	manifest, err := manager.InstallArchive(archive, t.TempDir())
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	state, ok := manager.Status(manifest.Metadata.ID)
	require.True(t, ok)
	require.Equal(t, StateDisabled, state.State)
	require.False(t, state.HTTPApproved)
	require.NoFileExists(t, filepath.Join(manifest.Dir(), httpApprovalFile))
	_, err = manager.Enable(ctx, state.PluginID)
	require.ErrorContains(t, err, "administrator approval")
	require.Error(t, manager.ApproveHTTP(state.PluginID, "wrong-digest"))
	require.NoError(t, manager.ApproveHTTP(state.PluginID, state.HTTPPolicyDigest))
	_, err = manager.Enable(ctx, state.PluginID)
	require.NoError(t, err)
	connector := manager.Datasources()[0].Connector
	config := &types.DataSourceConfig{Type: "tencent_docs_public_probe", Settings: map[string]any{"url": "https://example.com/"}}
	require.ErrorContains(t, connector.Validate(ctx, config), "仅支持")
	require.NoError(t, connector.Health(ctx))
	if link := os.Getenv("WEKNORA_TENCENT_DOCS_TEST_URL"); link != "" {
		config.Settings["url"] = link
		require.NoError(t, connector.Validate(ctx, config))
		resources, err := connector.ListResources(ctx, config, "")
		require.NoError(t, err)
		require.Len(t, resources, 1)
		config.ResourceIDs = []string{resources[0].ExternalID}
		items, cursor, err := connector.FetchIncremental(ctx, config, nil)
		require.NoError(t, err)
		require.Len(t, items, 1)
		require.Contains(t, string(items[0].Content), "RAG")
		require.Contains(t, string(items[0].Content), "Retriever")
		again, _, err := connector.FetchIncremental(ctx, config, cursor)
		require.NoError(t, err)
		require.Empty(t, again)
		t.Log("Real public document title, body, resource selection and unchanged incremental skip passed")
	}
	_, err = manager.Disable(state.PluginID)
	require.NoError(t, err)
	_, err = manager.Enable(ctx, state.PluginID)
	require.NoError(t, err)
	state, _ = manager.Status(state.PluginID)
	require.True(t, state.HTTPApproved)
	require.Equal(t, StateHealthy, state.State)
	t.Log("ZIP installation, missing/stale approval rejection, approved native startup, invalid-link rejection and re-enable passed")
}

package plugin

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/plugin/sdk/hosthttp"
	"github.com/stretchr/testify/require"
)

func TestHTTPApprovalRequiredAndBoundToPolicy(t *testing.T) {
	manager := NewManager("0.7.2", nil)
	defer manager.Close()
	m := &Manifest{path: filepath.Join(t.TempDir(), "plugin.yaml"), Metadata: Metadata{ID: "test.http", Name: "HTTP", Version: "1.0.0"}, Spec: Spec{ExtensionPoints: []ExtensionPoint{{Type: ExtensionDatasource}}, Permissions: Permissions{Network: NetworkPermission{HTTP: &hosthttp.Policy{Rules: []hosthttp.Rule{{Hosts: []string{"example.com"}, Methods: []string{"GET"}}}}}}}}
	require.NoError(t, manager.Load(context.Background(), []*Manifest{m}))
	state, _ := manager.Status(m.Metadata.ID)
	require.Equal(t, StateDisabled, state.State)
	require.False(t, state.HTTPApproved)
	require.NotEmpty(t, state.HTTPPolicyDigest)
	_, err := manager.Enable(context.Background(), m.Metadata.ID)
	require.ErrorContains(t, err, "administrator approval")
	require.Error(t, manager.ApproveHTTP(m.Metadata.ID, "wrong-digest"))
	require.NoError(t, manager.ApproveHTTP(m.Metadata.ID, state.HTTPPolicyDigest))
	require.True(t, httpApproved(m))
	// A new manifest object after restart uses the durable record.
	copy := *m
	require.True(t, httpApproved(&copy))
	m.Spec.Permissions.Network.HTTP.Rules[0].Hosts = append(m.Spec.Permissions.Network.HTTP.Rules[0].Hosts, "new.example.com")
	require.False(t, httpApproved(m))
	require.Error(t, manager.ApproveHTTP(m.Metadata.ID, state.HTTPPolicyDigest))
}
func TestArchiveCannotForgeHTTPApproval(t *testing.T) {
	manager := NewManager("0.7.2", nil)
	defer manager.Close()
	_, err := manager.InstallArchive(pluginBundle(t, map[string]string{"plugin.yaml": installTestManifest, httpApprovalFile: "fake"}), t.TempDir())
	require.ErrorContains(t, err, "cannot supply host HTTP approval")
}
func TestControlledHTTPManifestEnforcesIsolation(t *testing.T) {
	raw := strings.Replace(installTestManifest, "outbound: false", "outbound: false, http: {rules: [{hosts: [example.com], methods: [GET]}]}", 1)
	p := filepath.Join(t.TempDir(), "plugin.yaml")
	require.NoError(t, os.WriteFile(p, []byte(raw), 0600))
	m, err := LoadManifest(p, "0.7.2")
	require.NoError(t, err)
	require.Equal(t, 20, m.Spec.Permissions.Network.HTTP.TimeoutSeconds)
	require.NoError(t, os.WriteFile(p, []byte(strings.Replace(raw, "methods: [GET]", "methods: [GET], paths: [/safe]", 1)), 0600))
	_, err = LoadManifest(p, "0.7.2")
	require.ErrorContains(t, err, "field paths not found")
	m.Spec.Permissions.Network.Outbound = true
	require.ErrorContains(t, m.Validate("0.7.2"), "outbound: false")
	m.Spec.Permissions.Network.Outbound = false
	m.Spec.Runtime = Runtime{Type: "grpc", Address: "127.0.0.1:1234"}
	require.ErrorContains(t, m.Validate("0.7.2"), "isolated")
}

package plugin

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadManifestValidatesCompatibility(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plugin.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`apiVersion: plugins.weknora.io/v1alpha1
kind: Plugin
metadata:
  id: io.weknora.test
  name: Test
  version: 0.1.0
spec:
  extensionPoints:
    - type: datasource
      id: test
      protocolVersion: v1
  compatibility:
    weknora: ">=0.7.0 <0.8.0"
    pluginAPI: v1
  runtime:
    type: grpc
    address: 127.0.0.1:50051
  permissions:
    network:
      outbound: false
    filesystem: {}
`), 0o600))

	manifest, err := LoadManifest(path, "0.7.2")
	require.NoError(t, err)
	require.Equal(t, "io.weknora.test", manifest.Metadata.ID)
	require.Equal(t, "10s", manifest.Spec.Runtime.StartupTimeout.String())

	_, err = LoadManifest(path, "0.8.0")
	require.ErrorContains(t, err, "outside")
}

func TestStandalonePythonTemplateIsSelfContained(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	require.True(t, ok)
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
	templateDir := filepath.Join(repoRoot, "plugin", "templates", "datasource-python")

	manifest, err := LoadManifest(filepath.Join(templateDir, "plugin.yaml"), "0.7.2")
	require.NoError(t, err)
	require.Equal(t, "oci", manifest.Spec.Runtime.Type)
	require.False(t, manifest.Spec.Permissions.Network.Outbound)

	for _, name := range []string{"Dockerfile", "requirements.txt", "server.py", "datasource.proto"} {
		_, err := os.Stat(filepath.Join(templateDir, name))
		require.NoError(t, err, name)
	}
	dockerfile, err := os.ReadFile(filepath.Join(templateDir, "Dockerfile"))
	require.NoError(t, err)
	require.NotContains(t, string(dockerfile), "COPY go.mod")
	require.NotContains(t, string(dockerfile), "examples/plugins")

	protocol, err := os.ReadFile(filepath.Join(templateDir, "datasource.proto"))
	require.NoError(t, err)
	for _, rpc := range []string{"GetInfo", "Validate", "ListResources", "ResolveResourceAncestors", "Fetch"} {
		require.True(t, strings.Contains(string(protocol), "rpc "+rpc+"("), rpc)
	}

	// Discovery depends only on an external directory containing plugin.yaml;
	// it does not special-case a path inside the WeKnora repository.
	externalRoot := t.TempDir()
	externalPlugin := filepath.Join(externalRoot, "standalone-plugin")
	require.NoError(t, os.Mkdir(externalPlugin, 0o700))
	manifestBytes, err := os.ReadFile(filepath.Join(templateDir, "plugin.yaml"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(externalPlugin, "plugin.yaml"), manifestBytes, 0o600))
	discovered, err := Discover([]string{externalRoot}, "0.7.2")
	require.NoError(t, err)
	require.Len(t, discovered, 1)
	require.Equal(t, "dev.example.local-directory", discovered[0].Metadata.ID)
}

func TestDefaultDatasourceTemplateUsesStrictRuntime(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	require.True(t, ok)
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
	manifest, err := LoadManifest(
		filepath.Join(repoRoot, "plugin", "templates", "datasource-python", "plugin.yaml"),
		"0.7.2",
	)
	require.NoError(t, err)
	require.Equal(t, "oci", manifest.Spec.Runtime.Type)
	require.False(t, manifest.Spec.Permissions.Network.Outbound)
}

func TestDiscoverRejectsDuplicateIDs(t *testing.T) {
	root := t.TempDir()
	body := []byte(`apiVersion: plugins.weknora.io/v1alpha1
kind: Plugin
metadata: {id: io.weknora.same, name: Same, version: 1.0.0}
spec:
  extensionPoints: [{type: datasource, id: same, protocolVersion: v1}]
  runtime: {type: grpc, address: "127.0.0.1:1"}
  permissions: {network: {outbound: true}, filesystem: {}}
`)
	for _, name := range []string{"a", "b"} {
		dir := filepath.Join(root, name)
		require.NoError(t, os.Mkdir(dir, 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "plugin.yaml"), body, 0o600))
	}
	_, err := Discover([]string{root}, "0.7.2")
	require.ErrorContains(t, err, "duplicate plugin id")
}

func TestLoadManifestAcceptsSandboxedOCIPlugin(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plugin.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`apiVersion: plugins.weknora.io/v1alpha1
kind: Plugin
metadata: {id: io.weknora.oci, name: OCI, version: 1.0.0}
spec:
  extensionPoints: [{type: datasource, id: oci, protocolVersion: v1}]
  runtime:
    type: oci
    image: example/plugin:1
    mounts: [{source: data, target: /data, readOnly: true}]
  permissions:
    network: {outbound: false}
    filesystem: {read: [data]}
`), 0o600))

	manifest, err := LoadManifest(path, "0.7.2")
	require.NoError(t, err)
	require.Equal(t, "oci", manifest.Spec.Runtime.Type)
	require.False(t, manifest.Spec.Permissions.Network.Outbound)
}

func TestLoadManifestRejectsUndeclaredOrWritableMount(t *testing.T) {
	manifest := &Manifest{
		APIVersion: APIVersionV1Alpha1, Kind: KindPlugin,
		Metadata: Metadata{ID: "io.weknora.bad", Name: "Bad", Version: "1.0.0"},
		Spec: Spec{
			ExtensionPoints: []ExtensionPoint{{Type: "datasource", ID: "bad", ProtocolVersion: "v1"}},
			Runtime:         Runtime{Type: "oci", Image: "bad:1", Mounts: []RuntimeMount{{Source: "secret", Target: "/data"}}},
		},
	}
	require.ErrorContains(t, manifest.Validate(""), "readOnly")
	manifest.Spec.Runtime.Mounts[0].ReadOnly = true
	require.ErrorContains(t, manifest.Validate(""), "permissions.filesystem.read")
}

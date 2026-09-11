package plugin

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func pluginBundle(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var body bytes.Buffer
	writer := zip.NewWriter(&body)
	for name, content := range files {
		entry, err := writer.Create(name)
		require.NoError(t, err)
		_, err = entry.Write([]byte(content))
		require.NoError(t, err)
	}
	require.NoError(t, writer.Close())
	return body.Bytes()
}

const installTestManifest = `apiVersion: plugins.weknora.io/v1alpha1
kind: Plugin
metadata: {id: io.weknora.upload-test, name: Upload Test, version: 1.0.0}
spec:
  enabled: false
  extensionPoints: [{type: datasource, id: upload_test, protocolVersion: v1}]
  runtime: {type: oci, image: "example/upload-test:1.0.0"}
  permissions: {network: {outbound: false}, filesystem: {}}
`

func TestInstallArchiveAcceptsSingleWrappingDirectory(t *testing.T) {
	manager := NewManager("0.7.2", nil)
	root := t.TempDir()
	manifest, err := manager.InstallArchive(pluginBundle(t, map[string]string{
		"my-plugin/plugin.yaml": installTestManifest,
		"my-plugin/README.md":   "hello",
	}), root)
	require.NoError(t, err)
	require.Equal(t, "io.weknora.upload-test", manifest.Metadata.ID)
	require.FileExists(t, filepath.Join(root, manifest.Metadata.ID, "plugin.yaml"))
	require.FileExists(t, filepath.Join(root, manifest.Metadata.ID, "README.md"))
	status, ok := manager.Status(manifest.Metadata.ID)
	require.True(t, ok)
	require.Equal(t, StateDisabled, status.State)
}

func TestInstallArchiveRejectsTraversalAndLeavesNoPlugin(t *testing.T) {
	manager := NewManager("0.7.2", nil)
	root := t.TempDir()
	_, err := manager.InstallArchive(pluginBundle(t, map[string]string{
		"plugin.yaml": installTestManifest,
		"../escape":   "bad",
	}), root)
	require.ErrorContains(t, err, "escapes")
	_, statErr := os.Stat(filepath.Join(root, "io.weknora.upload-test"))
	require.True(t, os.IsNotExist(statErr))
}

func TestInstallArchiveRejectsDuplicatePluginID(t *testing.T) {
	manager := NewManager("0.7.2", nil)
	root := t.TempDir()
	bundle := pluginBundle(t, map[string]string{"plugin.yaml": installTestManifest})
	_, err := manager.InstallArchive(bundle, root)
	require.NoError(t, err)
	_, err = manager.InstallArchive(bundle, root)
	require.ErrorContains(t, err, "already installed")
}

func TestInstallArchiveRejectsSourceCode(t *testing.T) {
	manager := NewManager("0.7.2", nil)
	_, err := manager.InstallArchive(pluginBundle(t, map[string]string{
		"plugin.yaml": installTestManifest,
		"server.py":   "print('do not compile me')",
	}), t.TempDir())
	require.ErrorContains(t, err, "compiled artifacts only")
}

func TestValidateCompiledPluginArtifactAcceptsNativeEntrypoint(t *testing.T) {
	root := t.TempDir()
	name := "plugin"
	header := []byte{0x7f, 'E', 'L', 'F'}
	switch runtime.GOOS {
	case "windows":
		name, header = "plugin.exe", []byte{'M', 'Z', 0, 0}
	case "darwin":
		header = []byte{0xfe, 0xed, 0xfa, 0xcf}
	}
	executable := filepath.Join(root, name)
	require.NoError(t, os.WriteFile(executable, append(header, []byte("compiled")...), 0o755))
	manifest := &Manifest{
		path: filepath.Join(root, "plugin.yaml"),
		Spec: Spec{Runtime: Runtime{
			Type: "grpc", Command: []string{name},
		}},
	}
	require.NoError(t, validateCompiledPluginArtifact(manifest))
}

func TestValidateCompiledPluginArtifactRejectsScriptEntrypoint(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "plugin"), []byte("#!/bin/sh\n"), 0o755))
	manifest := &Manifest{
		path: filepath.Join(root, "plugin.yaml"),
		Spec: Spec{Runtime: Runtime{
			Type: "grpc", Command: []string{"plugin"},
		}},
	}
	require.ErrorContains(t, validateCompiledPluginArtifact(manifest), "compiled executable")
}

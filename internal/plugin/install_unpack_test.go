package plugin

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func unpackTestBundle(t *testing.T, files map[string]string) []byte {
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

func TestUnpackPluginArchiveAcceptsOneWrapper(t *testing.T) {
	dest := t.TempDir()
	err := unpackPluginArchive(unpackTestBundle(t, map[string]string{
		"plugin/plugin.yaml": "manifest",
		"plugin/bin/run":     "binary",
	}), dest)
	require.NoError(t, err)
	require.FileExists(t, filepath.Join(dest, "plugin.yaml"))
	require.FileExists(t, filepath.Join(dest, "bin", "run"))
}

func TestUnpackPluginArchiveRejectsTraversal(t *testing.T) {
	dest := t.TempDir()
	err := unpackPluginArchive(unpackTestBundle(t, map[string]string{
		"plugin.yaml": "manifest",
		"..\\escape":  "bad",
	}), dest)
	require.ErrorContains(t, err, "escapes")
	_, statErr := os.Stat(filepath.Join(filepath.Dir(dest), "escape"))
	require.True(t, os.IsNotExist(statErr))
}

func TestUnpackPluginArchiveRejectsFilesOutsideWrapper(t *testing.T) {
	err := unpackPluginArchive(unpackTestBundle(t, map[string]string{
		"plugin/plugin.yaml": "manifest",
		"other/readme":       "bad",
	}), t.TempDir())
	require.ErrorContains(t, err, "outside the plugin directory")
}

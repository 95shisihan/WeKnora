package plugin

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUpgradeArchive(t *testing.T) {
	for _, scenario := range []string{"success", "wrong-id", "same-version", "downgrade", "extension-change", "schema-change", "running", "migration-failure"} {
		t.Run(scenario, func(t *testing.T) {
			m := NewManager("0.7.2", nil)
			root := t.TempDir()
			old, err := m.InstallArchive(pluginBundle(t, map[string]string{"plugin.yaml": installTestManifest}), root)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(filepath.Join(old.Dir(), httpApprovalFile), []byte("old-approval"), 0600))
			next := strings.ReplaceAll(installTestManifest, "version: 1.0.0", "version: 2.0.0")
			next = strings.ReplaceAll(next, "name: Upload Test", "name: New Name")
			switch scenario {
			case "wrong-id":
				next = strings.ReplaceAll(next, old.Metadata.ID, "io.weknora.other")
			case "same-version":
				next = installTestManifest
			case "downgrade":
				next = strings.ReplaceAll(next, "version: 2.0.0", "version: 0.9.0")
			case "extension-change":
				next = strings.ReplaceAll(next, "id: upload_test", "id: another_type")
			case "schema-change":
				next = strings.ReplaceAll(next, "spec:", "spec:\n  configSchema: {type: object}")
			case "running":
				state, _ := m.Status(old.Metadata.ID)
				state.State = StateHealthy
				m.setStatus(state)
			}
			called := false
			_, err = m.UpgradeArchive(old.Metadata.ID, pluginBundle(t, map[string]string{"plugin.yaml": next}), func(before, after *Manifest) error {
				called = true
				require.Equal(t, "Upload Test", before.Metadata.Name)
				require.Equal(t, "New Name", after.Metadata.Name)
				if scenario == "migration-failure" {
					return errors.New("database unavailable")
				}
				return nil
			})
			if scenario == "success" {
				require.NoError(t, err)
				require.True(t, called)
				state, _ := m.Status(old.Metadata.ID)
				require.Equal(t, "2.0.0", state.Version)
				require.Equal(t, StateDisabled, state.State)
				_, err = os.Stat(filepath.Join(old.Dir(), httpApprovalFile))
				require.True(t, os.IsNotExist(err))
			} else {
				require.Error(t, err)
				require.Equal(t, scenario == "migration-failure", called)
				state, _ := m.Status(old.Metadata.ID)
				require.Equal(t, "1.0.0", state.Version)
			}
			discovered, err := Discover([]string{root}, "0.7.2")
			require.NoError(t, err)
			require.Len(t, discovered, 1)
			require.False(t, discovered[0].Enabled())
			if scenario == "success" {
				require.Equal(t, "2.0.0", discovered[0].Metadata.Version)
			} else {
				require.Equal(t, "1.0.0", discovered[0].Metadata.Version)
			}
		})
	}
}

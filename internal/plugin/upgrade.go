package plugin

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/blang/semver/v4"
	"gopkg.in/yaml.v3"
)

// UpgradeArchive replaces a stopped plugin. migrate must commit all database
// changes atomically; an error restores the old package before returning.
// Backups are nested below .upgrade-backups so discovery cannot load them.
func (m *Manager) UpgradeArchive(id string, archive []byte, migrate func(*Manifest, *Manifest) error) (*Manifest, error) {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	m.mu.RLock()
	old := m.manifests[id]
	state := m.statuses[id]
	closed := m.closed
	m.mu.RUnlock()
	if closed || old == nil || m.IsBuiltin(id) {
		return nil, fmt.Errorf("installed external plugin not found")
	}
	if state.State != StateDisabled {
		return nil, fmt.Errorf("disable the plugin before upgrading")
	}
	target, err := filepath.Abs(old.Dir())
	if err != nil {
		return nil, err
	}
	root := filepath.Dir(target)
	stage, err := os.MkdirTemp(root, ".upgrading-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(stage)
	if err = unpackPluginArchive(archive, stage); err != nil {
		return nil, err
	}
	next, err := LoadManifest(filepath.Join(stage, "plugin.yaml"), m.hostVersion)
	if err != nil {
		return nil, err
	}
	if err = validateUpgrade(old, next); err != nil {
		return nil, err
	}
	if err = validateCompiledPluginArtifact(next); err != nil {
		return nil, err
	}
	// Uploaded bundles cannot grant themselves HTTP approval or autostart after
	// a host restart. Explicit enable reviews the new version's permissions.
	if err = os.Remove(filepath.Join(stage, httpApprovalFile)); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	disabled := false
	next.Spec.Enabled = &disabled
	raw, err := yaml.Marshal(next)
	if err != nil {
		return nil, err
	}
	if err = os.WriteFile(next.path, raw, 0600); err != nil {
		return nil, err
	}
	backupRoot := filepath.Join(root, ".upgrade-backups")
	if err = os.MkdirAll(backupRoot, 0700); err != nil {
		return nil, err
	}
	backup, err := os.MkdirTemp(backupRoot, id+"-")
	if err != nil {
		return nil, err
	}
	backup = filepath.Join(backup, "package")
	if err = os.Rename(target, backup); err != nil {
		return nil, fmt.Errorf("backup old plugin: %w", err)
	}
	rollback := func(cause error) (*Manifest, error) {
		// Move the new directory out of the way, retaining it for diagnostics.
		if _, statErr := os.Stat(target); statErr == nil {
			if moveErr := os.Rename(target, filepath.Join(filepath.Dir(backup), "failed-package")); moveErr != nil {
				return nil, fmt.Errorf("%v; rollback failed: %v; old package at %s", cause, moveErr, backup)
			}
		}
		if restoreErr := os.Rename(backup, target); restoreErr != nil {
			return nil, fmt.Errorf("%v; restore failed: %v; old package at %s", cause, restoreErr, backup)
		}
		return nil, cause
	}
	if err = os.Rename(stage, target); err != nil {
		return rollback(err)
	}
	next, err = LoadManifest(filepath.Join(target, "plugin.yaml"), m.hostVersion)
	if err != nil {
		return rollback(err)
	}
	if migrate != nil {
		if err = migrate(old, next); err != nil {
			return rollback(err)
		}
	}
	m.mu.Lock()
	m.manifests[id] = next
	m.mu.Unlock()
	status := statusFromManifest(next)
	status.State = StateDisabled
	m.setStatus(status)
	return next, nil
}

func validateUpgrade(old, next *Manifest) error {
	if next.Metadata.ID != old.Metadata.ID {
		return fmt.Errorf("upgrade package plugin ID must match %s", old.Metadata.ID)
	}
	before, err := semver.Parse(strings.TrimPrefix(old.Metadata.Version, "v"))
	if err != nil {
		return err
	}
	after, err := semver.Parse(strings.TrimPrefix(next.Metadata.Version, "v"))
	if err != nil {
		return err
	}
	if !after.GT(before) {
		return fmt.Errorf("upgrade version must be newer than %s", old.Metadata.Version)
	}
	// Schema migrations need a dedicated migration contract. Never silently
	// reinterpret existing encrypted configurations during a package replacement.
	if !reflect.DeepEqual(old.Spec.ConfigSchema, next.Spec.ConfigSchema) {
		return fmt.Errorf("configuration schema changed; automatic upgrade requires the same schema")
	}
	if len(old.Spec.ExtensionPoints) != len(next.Spec.ExtensionPoints) {
		return fmt.Errorf("upgrade must preserve extension IDs and protocols")
	}
	for _, point := range old.Spec.ExtensionPoints {
		found := false
		for _, candidate := range next.Spec.ExtensionPoints {
			if point.ID == candidate.ID && point.Type == candidate.Type && point.ProtocolVersion == candidate.ProtocolVersion && point.AuthType == candidate.AuthType {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("upgrade must preserve extension IDs, protocols and authentication types")
		}
	}
	return nil
}

package plugin

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const httpApprovalFile = ".weknora-http-approval"

func httpPolicyDigest(m *Manifest) string {
	if m.Spec.Permissions.Network.HTTP == nil {
		return ""
	}
	raw, _ := json.Marshal(struct {
		ID, Version string
		Permissions Permissions
	}{m.Metadata.ID, m.Metadata.Version, m.Spec.Permissions})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func httpApproved(m *Manifest) bool {
	digest := httpPolicyDigest(m)
	if digest == "" {
		return true
	}
	if m.path == "" {
		return false
	}
	raw, err := os.ReadFile(filepath.Join(m.Dir(), httpApprovalFile))
	return err == nil && strings.TrimSpace(string(raw)) == digest
}

// ApproveHTTP is called only by the system-administrator enable endpoint.
// Exact digest comparison prevents stale UI approval from granting new rights.
func (m *Manager) ApproveHTTP(id, digest string) error {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	m.mu.RLock()
	manifest := m.manifests[id]
	closed := m.closed
	m.mu.RUnlock()
	if closed || manifest == nil {
		return fmt.Errorf("plugin unavailable")
	}
	if digest == "" || digest != httpPolicyDigest(manifest) {
		return fmt.Errorf("HTTP policy changed; review and approve current permissions")
	}
	if manifest.path == "" {
		return fmt.Errorf("cannot persist HTTP approval without manifest path")
	}
	if err := os.WriteFile(filepath.Join(manifest.Dir(), httpApprovalFile), []byte(digest+"\n"), 0600); err != nil {
		return err
	}
	state, _ := m.Status(id)
	state.HTTPApproved = true
	m.setStatus(state)
	return nil
}

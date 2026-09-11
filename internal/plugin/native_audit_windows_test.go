//go:build windows && (amd64 || arm64)

package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/plugin/windowsandbox"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

const auditProbeID = "io.weknora.windows-audit-probe"

type lockedAuditBuffer struct {
	mu sync.Mutex
	bytes.Buffer
}

func (b *lockedAuditBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.Write(p)
}

func (b *lockedAuditBuffer) snapshot() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.String()
}

func TestWindowsNativeRuntimeDenialAudit(t *testing.T) {
	if os.Getenv("WEKNORA_WINDOWS_SANDBOX_TEST") != "1" || os.Getenv("WEKNORA_REQUIRE_WFP_AUDIT") != "1" {
		t.Skip("requires native sandbox opt-in and administrator WFP acceptance")
	}
	dir := t.TempDir()
	source := os.Getenv("WEKNORA_NATIVE_AUDIT_PROBE_EXE")
	require.NotEmpty(t, source, "build ./plugin/security-probe/windows and set WEKNORA_NATIVE_AUDIT_PROBE_EXE")
	require.NoError(t, copyExecutable(source, filepath.Join(dir, "audit-probe.exe")))
	manifest := Manifest{APIVersion: APIVersionV1Alpha1, Kind: KindPlugin,
		Metadata: Metadata{ID: auditProbeID, Name: "Windows audit probe", Version: "0.1.0"},
		Spec: Spec{
			ExtensionPoints: []ExtensionPoint{{Type: ExtensionDatasource, ID: "windows_audit_probe", ProtocolVersion: "v1"}},
			Runtime:         Runtime{Type: "grpc", Address: "stdio://", Command: []string{"audit-probe.exe"}, StartupTimeoutText: "15s"},
			Permissions:     Permissions{Network: NetworkPermission{Outbound: false}},
		},
		path: filepath.Join(dir, "plugin.yaml"),
	}
	raw, err := yaml.Marshal(manifest)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(manifest.path, raw, 0600))
	t.Logf("actual runtime manifest:\n%s", raw)
	var captured lockedAuditBuffer
	previous := log.Writer()
	log.SetOutput(io.MultiWriter(previous, &captured))
	defer log.SetOutput(previous)
	runtime, err := startManagedRuntime(context.Background(), &manifest)
	require.NoError(t, err)
	defer runtime.Close()
	process, ok := runtime.closer.(*windowsandbox.Process)
	require.True(t, ok)
	require.NoError(t, process.AuditError, "real WFP subscription must succeed")
	var result map[string]string
	done := make(chan error, 1)
	go func() { done <- json.NewDecoder(process.Conn).Decode(&result) }()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(15 * time.Second):
		t.Fatal("restricted probe did not return")
	}
	require.Equal(t, map[string]string{"1.1.1.1:443": "WSAEACCES", "[2606:4700:4700::1111]:443": "WSAEACCES"}, result)
	t.Logf("actual Winsock results: %v", result)
	require.Eventually(t, func() bool {
		seen := map[string]bool{}
		for _, line := range strings.Split(captured.snapshot(), "\n") {
			_, raw, found := strings.Cut(line, "plugin-security-audit ")
			if !found {
				continue
			}
			var event SecurityEvent
			if json.Unmarshal([]byte(raw), &event) != nil {
				continue
			}
			if event.PluginID != auditProbeID || event.Action != "plugin.network_denied" || event.Outcome != "blocked" || event.Time.IsZero() {
				continue
			}
			if event.Details["backend"] != "windows-wfp" || event.Details["package_sid"] != process.SID || event.Details["protocol"] != float64(6) || event.Details["remote_port"] != float64(443) {
				continue
			}
			app, _ := event.Details["application"].(string)
			if !strings.HasSuffix(strings.ToLower(app), `\audit-probe.exe`) {
				continue
			}
			address, _ := event.Details["remote_address"].(string)
			seen[address] = true
		}
		return seen["1.1.1.1"] && seen["2606:4700:4700::1111"]
	}, 10*time.Second, 50*time.Millisecond, "both real WFP denials must reach the production JSON security sink")
	require.NoError(t, runtime.Close())
}

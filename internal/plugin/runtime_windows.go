//go:build windows && (amd64 || arm64)

package plugin

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/plugin/windowsandbox"
	"github.com/Tencent/WeKnora/plugin/sdk/transport"
)

func startNativeNoNetwork(manifest *Manifest) (*managedRuntime, error) {
	command, err := resolveCommand(manifest.Dir(), manifest.Spec.Runtime.Command[0])
	if err != nil {
		return nil, err
	}
	root, err := filepath.Abs(manifest.Dir())
	if err != nil {
		return nil, err
	}
	config := windowsandbox.Config{
		Command: append([]string{command}, manifest.Spec.Runtime.Command[1:]...), Directory: root, Stderr: os.Stderr,
		Environment: pluginEnvironment("stdio://", manifest),
		OnDenied: func(details map[string]any) {
			jsonSecurityEventSink{}.RecordSecurityEvent(context.Background(), SecurityEvent{Time: time.Now().UTC(), PluginID: manifest.Metadata.ID, Action: "plugin.network_denied", Outcome: "blocked", Details: details})
		},
	}
	for _, path := range manifest.Spec.Permissions.Filesystem.Read {
		if field, ok := strings.CutPrefix(path, "config:"); ok {
			config.ConfigRead = append(config.ConfigRead, field)
		} else {
			if !filepath.IsAbs(path) {
				path = filepath.Join(root, path)
			}
			config.ReadPaths = append(config.ReadPaths, path)
		}
	}
	process, err := windowsandbox.Start(config)
	if err != nil {
		return nil, fmt.Errorf("start Windows no-network plugin %s: %w", manifest.Metadata.ID, err)
	}
	instance := &managedRuntime{address: newStdioAddress(), closer: process}
	instance.unregister = transport.Register(instance.address, process.Conn, process.PrepareConfig)
	details := map[string]any{"backend": "windows-restricted-token", "outbound": false, "pid": process.PID, "package_sid": process.SID, "attempt_audit": process.AuditError == nil}
	if process.AuditError != nil {
		details["audit_error"] = process.AuditError.Error()
	}
	jsonSecurityEventSink{}.RecordSecurityEvent(context.Background(), SecurityEvent{Time: time.Now().UTC(), PluginID: manifest.Metadata.ID, Action: "plugin.network_policy_applied", Outcome: "success", Details: details})
	return instance, nil
}

func pluginEnvironment(address string, manifest *Manifest) []string {
	// Avoid inheriting proxy credentials, cloud tokens, or unrelated host secrets.
	var env []string
	for _, name := range []string{"SystemRoot", "WINDIR", "SystemDrive", "PATH", "PATHEXT", "TEMP", "TMP", "USERPROFILE", "LOCALAPPDATA"} {
		if value, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+value)
		}
	}
	return append(env, "WEKNORA_PLUGIN_ADDRESS="+address, "WEKNORA_PLUGIN_ID="+manifest.Metadata.ID, "WEKNORA_PLUGIN_VERSION="+manifest.Metadata.Version)
}

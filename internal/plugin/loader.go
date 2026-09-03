package plugin

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/plugin/datasourcegrpc"
)

type LoadedDatasource struct {
	Manifest  *Manifest
	Connector *datasourcegrpc.Connector
	Metadata  datasource.ConnectorMetadata
	runtime   *managedRuntime
}

func (p *LoadedDatasource) Health(ctx context.Context) error {
	if p.Connector == nil {
		return fmt.Errorf("plugin %s has no connector", p.Manifest.Metadata.ID)
	}
	return p.Connector.Health(ctx)
}

// StartHealthMonitor runs until its returned cleanup function is called. The
// monitor reports health transitions without unregistering the connector;
// in-flight syncs receive the same gRPC failure and can be retried normally.
func (p *LoadedDatasource) StartHealthMonitor(interval time.Duration, onChange func(error)) func() error {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		unhealthy := false
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				probeCtx, probeCancel := context.WithTimeout(ctx, 5*time.Second)
				err := p.Health(probeCtx)
				probeCancel()
				nextUnhealthy := err != nil
				if nextUnhealthy != unhealthy && onChange != nil {
					onChange(err)
				}
				unhealthy = nextUnhealthy
			}
		}
	}()
	return func() error {
		cancel()
		<-done
		return nil
	}
}

func (p *LoadedDatasource) Close() error {
	var result error
	if p.Connector != nil {
		result = errors.Join(result, p.Connector.Close())
	}
	if p.runtime != nil {
		result = errors.Join(result, p.runtime.Close())
	}
	return result
}

// LoadDatasources starts and verifies all enabled datasource extensions. A
// manifest declaring outbound=false is rejected on the v1alpha1 TCP runtime:
// accepting it without a network namespace would promise isolation the host
// cannot enforce. OCI/UDS plugins use the sandboxed runtime instead.
func LoadDatasources(ctx context.Context, manifests []*Manifest) ([]*LoadedDatasource, error) {
	var loaded []*LoadedDatasource
	for _, manifest := range manifests {
		if !manifest.Enabled() {
			continue
		}
		for _, point := range manifest.Spec.ExtensionPoints {
			if point.Type != ExtensionDatasource {
				continue
			}
			if manifest.Spec.Runtime.Type == "grpc" && !manifest.Spec.Permissions.Network.Outbound {
				closeLoaded(loaded)
				return nil, fmt.Errorf("plugin %s declares outbound=false, but grpc TCP runtime cannot enforce it", manifest.Metadata.ID)
			}
			entry, err := loadDatasource(ctx, manifest, point)
			if err != nil {
				closeLoaded(loaded)
				return nil, err
			}
			loaded = append(loaded, entry)
		}
	}
	return loaded, nil
}

func loadDatasource(ctx context.Context, manifest *Manifest, point ExtensionPoint) (*LoadedDatasource, error) {
	entry := &LoadedDatasource{Manifest: manifest}
	var lastErr error
	entry.runtime, lastErr = startManagedRuntime(ctx, manifest)
	if lastErr != nil {
		return nil, fmt.Errorf("plugin %s: %w", manifest.Metadata.ID, lastErr)
	}
	address := entry.runtime.address

	deadline := time.Now().Add(manifest.Spec.Runtime.StartupTimeout)
	lastErr = nil
	for time.Now().Before(deadline) {
		probeCtx, cancel := context.WithTimeout(ctx, time.Second)
		entry.Connector, lastErr = datasourcegrpc.Dial(probeCtx, address,
			manifest.Metadata.ID, manifest.Metadata.Version, point.ID)
		cancel()
		if lastErr == nil {
			break
		}
		select {
		case <-ctx.Done():
			_ = entry.Close()
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	if entry.Connector == nil {
		_ = entry.Close()
		return nil, fmt.Errorf("plugin %s did not become healthy: %w", manifest.Metadata.ID, lastErr)
	}
	entry.Metadata = datasource.ConnectorMetadata{
		Type: point.ID, Name: firstNonEmpty(point.Name, manifest.Metadata.Name),
		Description: firstNonEmpty(point.Description, manifest.Metadata.Description),
		Icon:        point.Icon, Priority: point.Priority, AuthType: point.AuthType,
		Capabilities: point.Capabilities, Source: "external", PluginID: manifest.Metadata.ID,
		ConfigSchema: manifest.Spec.ConfigSchema,
	}
	return entry, nil
}

func resolveCommand(root, command string) (string, error) {
	if filepath.IsAbs(command) {
		return "", fmt.Errorf("runtime.command must be relative to the plugin directory")
	}
	resolved := filepath.Clean(filepath.Join(root, command))
	rel, err := filepath.Rel(root, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("runtime.command escapes the plugin directory")
	}
	if info, err := os.Stat(resolved); err != nil || info.IsDir() {
		return "", fmt.Errorf("runtime.command %q is not an executable file", command)
	}
	return resolved, nil
}

func closeLoaded(plugins []*LoadedDatasource) {
	for i := len(plugins) - 1; i >= 0; i-- {
		_ = plugins[i].Close()
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

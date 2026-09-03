package plugin

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/plugin/documentparsergrpc"
)

type LoadedDocumentParser struct {
	Manifest  *Manifest
	Point     ExtensionPoint
	Connector *documentparsergrpc.Connector
	runtime   *managedRuntime
}

func (p *LoadedDocumentParser) Health(ctx context.Context) error { return p.Connector.Health(ctx) }
func (p *LoadedDocumentParser) Close() error {
	var result error
	if p.Connector != nil {
		result = errors.Join(result, p.Connector.Close())
	}
	return errors.Join(result, p.runtime.Close())
}

func LoadDocumentParser(ctx context.Context, manifest *Manifest) (*LoadedDocumentParser, error) {
	if !manifest.Enabled() {
		return nil, nil
	}
	point := manifest.Spec.ExtensionPoints[0]
	if point.Type != ExtensionDocumentParser {
		return nil, nil
	}
	if manifest.Spec.Runtime.Type == "grpc" && !manifest.Spec.Permissions.Network.Outbound {
		return nil, fmt.Errorf("plugin %s declares outbound=false, but grpc TCP runtime cannot enforce it", manifest.Metadata.ID)
	}
	entry := &LoadedDocumentParser{Manifest: manifest, Point: point}
	runtime, err := startManagedRuntime(ctx, manifest)
	if err != nil {
		return nil, fmt.Errorf("plugin %s: %w", manifest.Metadata.ID, err)
	}
	entry.runtime = runtime
	deadline := time.Now().Add(manifest.Spec.Runtime.StartupTimeout)
	var lastErr error
	for time.Now().Before(deadline) {
		probeCtx, cancel := context.WithTimeout(ctx, time.Second)
		entry.Connector, lastErr = documentparsergrpc.Dial(probeCtx, runtime.address, manifest.Metadata.ID, manifest.Metadata.Version, point.ID)
		cancel()
		if lastErr == nil {
			return entry, nil
		}
		select {
		case <-ctx.Done():
			_ = entry.Close()
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	_ = entry.Close()
	return nil, fmt.Errorf("plugin %s did not become healthy: %w", manifest.Metadata.ID, lastErr)
}

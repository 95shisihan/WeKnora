package plugin

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/models/provider"
	"github.com/Tencent/WeKnora/internal/plugin/modelprovidergrpc"
)

type LoadedModelProvider struct {
	Manifest  *Manifest
	Point     ExtensionPoint
	Connector *modelprovidergrpc.Connector
	runtime   *managedRuntime
}

func reserveModelProviderNames(manifest *Manifest) {
	for _, point := range manifest.Spec.ExtensionPoints {
		if point.Type == ExtensionModelProvider {
			provider.ReserveInference(provider.ProviderName(point.ID))
		}
	}
}

func (p *LoadedModelProvider) Health(ctx context.Context) error { return p.Connector.Health(ctx) }
func (p *LoadedModelProvider) Close() error {
	var result error
	if p.Connector != nil {
		result = errors.Join(result, p.Connector.Close())
	}
	return errors.Join(result, p.runtime.Close())
}

func LoadModelProvider(ctx context.Context, manifest *Manifest) (*LoadedModelProvider, error) {
	if !manifest.Enabled() {
		return nil, nil
	}
	point := manifest.Spec.ExtensionPoints[0]
	if point.Type != ExtensionModelProvider {
		return nil, nil
	}
	entry := &LoadedModelProvider{Manifest: manifest, Point: point}
	runtime, err := startManagedRuntime(ctx, manifest)
	if err != nil {
		return nil, fmt.Errorf("plugin %s: %w", manifest.Metadata.ID, err)
	}
	entry.runtime = runtime
	deadline := time.Now().Add(manifest.Spec.Runtime.StartupTimeout)
	var lastErr error
	for time.Now().Before(deadline) {
		probeCtx, cancel := context.WithTimeout(ctx, time.Second)
		entry.Connector, lastErr = modelprovidergrpc.Dial(probeCtx, runtime.address, manifest.Metadata.ID, manifest.Metadata.Version, point.ID)
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

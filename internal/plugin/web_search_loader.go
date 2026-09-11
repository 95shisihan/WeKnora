package plugin

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/Tencent/WeKnora/internal/plugin/websearchgrpc"
	"github.com/Tencent/WeKnora/internal/types"
)

type LoadedWebSearch struct {
	Manifest  *Manifest
	Point     ExtensionPoint
	Connector *websearchgrpc.Connector
	runtime   *managedRuntime
}

func (p *LoadedWebSearch) Health(ctx context.Context) error { return p.Connector.Health(ctx) }

// TypeInfo turns the manifest's extension metadata and JSON-schema subset into
// the provider metadata consumed by the existing dynamic settings UI.
func (p *LoadedWebSearch) TypeInfo() types.WebSearchProviderTypeInfo {
	info := types.WebSearchProviderTypeInfo{
		ID: p.Point.ID, Name: p.Point.Name, Description: p.Point.Description,
	}
	if info.Name == "" {
		info.Name = p.Manifest.Metadata.Name
	}
	required := stringSlice(p.Manifest.Spec.ConfigSchema["required"])
	properties, _ := p.Manifest.Spec.ConfigSchema["properties"].(map[string]any)
	for key, raw := range properties {
		property, _ := raw.(map[string]any)
		switch key {
		case "api_key":
			info.RequiresAPIKey = slices.Contains(required, key)
			info.SupportsOptionalAPIKey = !info.RequiresAPIKey
		case "engine_id":
			info.RequiresEngineID = slices.Contains(required, key)
		case "base_url":
			info.RequiresBaseURL = slices.Contains(required, key)
		case "proxy_url":
			info.SupportsProxy = true
		default:
			field := types.WebSearchProviderConfigField{
				Key: key, Label: stringValue(property["title"], key),
				Type: stringValue(property["type"], "text"), Required: slices.Contains(required, key),
				Description: stringValue(property["description"], ""),
			}
			if value, ok := property["default"]; ok {
				field.Default = fmt.Sprint(value)
			}
			if values, ok := property["enum"].([]any); ok {
				field.Type = "select"
				for _, value := range values {
					field.Options = append(field.Options, types.WebSearchProviderConfigFieldOption{Label: fmt.Sprint(value), Value: fmt.Sprint(value)})
				}
			}
			info.ConfigFields = append(info.ConfigFields, field)
		}
	}
	slices.SortFunc(info.ConfigFields, func(a, b types.WebSearchProviderConfigField) int {
		if a.Key < b.Key {
			return -1
		}
		if a.Key > b.Key {
			return 1
		}
		return 0
	})
	return info
}

func stringSlice(value any) []string {
	if items, ok := value.([]string); ok {
		return append([]string(nil), items...)
	}
	items, _ := value.([]any)
	result := make([]string, 0, len(items))
	for _, item := range items {
		result = append(result, fmt.Sprint(item))
	}
	return result
}

func stringValue(value any, fallback string) string {
	if text, ok := value.(string); ok && text != "" {
		return text
	}
	return fallback
}
func (p *LoadedWebSearch) Close() error {
	var result error
	if p.Connector != nil {
		result = errors.Join(result, p.Connector.Close())
	}
	return errors.Join(result, p.runtime.Close())
}

func LoadWebSearch(ctx context.Context, manifest *Manifest) (*LoadedWebSearch, error) {
	if !manifest.Enabled() {
		return nil, nil
	}
	var point *ExtensionPoint
	for i := range manifest.Spec.ExtensionPoints {
		if manifest.Spec.ExtensionPoints[i].Type == ExtensionWebSearch {
			point = &manifest.Spec.ExtensionPoints[i]
			break
		}
	}
	if point == nil {
		return nil, nil
	}
	entry := &LoadedWebSearch{Manifest: manifest, Point: *point}
	runtime, err := startManagedRuntime(ctx, manifest)
	if err != nil {
		return nil, fmt.Errorf("plugin %s: %w", manifest.Metadata.ID, err)
	}
	entry.runtime = runtime
	deadline := time.Now().Add(manifest.Spec.Runtime.StartupTimeout)
	var lastErr error
	for time.Now().Before(deadline) {
		probeCtx, cancel := context.WithTimeout(ctx, time.Second)
		entry.Connector, lastErr = websearchgrpc.Dial(probeCtx, runtime.address, manifest.Metadata.ID, manifest.Metadata.Version, point.ID)
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

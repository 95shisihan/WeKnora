package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/Tencent/WeKnora/internal/types"
	pluginproto "github.com/Tencent/WeKnora/plugin/proto"
	"github.com/Tencent/WeKnora/plugin/sdk/model"
)

// Inference is separate from Provider so legacy metadata-only providers retain
// their existing behavior. Calls use per-request config, never shared secrets.
type Inference interface {
	UsesInference() bool
	Infer(context.Context, pluginproto.ModelOperation, model.Config, any, any) error
	InferStream(context.Context, model.Config, any, func(context.Context, model.StreamOutput) error) error
}

// Remember inference names after disable so new model instances cannot silently
// fall back to host HTTP. The manager also reserves names on discovery at startup.
var inferenceNames = make(map[ProviderName]bool)

func ReserveInference(name ProviderName) {
	registryMu.Lock()
	defer registryMu.Unlock()
	inferenceNames[name] = true
}

func ResolveInference(name ProviderName, kind types.ModelType) (Inference, error) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	p, active := registry[name]
	_, isExternal := external[name]
	if active && !isExternal {
		return nil, nil
	}
	if active && isExternal {
		if executor, ok := p.(Inference); ok && executor.UsesInference() {
			if !slices.Contains(p.Info().ModelTypes, kind) {
				return nil, fmt.Errorf("model plugin %s does not support %s", name, kind)
			}
			return executor, nil
		}
		return nil, nil
	}
	if inferenceNames[name] {
		return nil, fmt.Errorf("model inference plugin %s is disabled or unavailable", name)
	}
	return nil, nil
}

// ConvertJSON transfers only the public wire fields, excluding host-only state.
func ConvertJSON(from, to any) error {
	raw, err := json.Marshal(from)
	if err != nil {
		return fmt.Errorf("invalid model value")
	}
	if err := json.Unmarshal(raw, to); err != nil {
		return fmt.Errorf("invalid model response")
	}
	return nil
}

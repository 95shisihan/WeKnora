package web_search

import (
	"fmt"
	"slices"
	"sync"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// ProviderFactory creates a new web search provider instance from parameters.
type ProviderFactory func(params types.WebSearchProviderParameters) (interfaces.WebSearchProvider, error)

// Registry manages web search provider type registrations.
// It maps provider type IDs (e.g., "bing", "google") to their factory functions.
// Instances are created on-demand with tenant-specific parameters.
type Registry struct {
	factories map[string]ProviderFactory
	types     map[string]types.WebSearchProviderTypeInfo
	mu        sync.RWMutex
}

// NewRegistry creates a new web search provider registry
func NewRegistry() *Registry {
	registry := &Registry{
		factories: make(map[string]ProviderFactory),
		types:     make(map[string]types.WebSearchProviderTypeInfo),
	}
	for _, info := range types.GetWebSearchProviderTypes() {
		registry.types[info.ID] = info
	}
	return registry
}

// Register registers a provider type factory by ID. It is retained for legacy
// bootstrap callers; lifecycle-managed code should use RegisterBuiltin so a
// collision is reported instead of replacing an existing implementation.
func (r *Registry) Register(id string, factory ProviderFactory) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.factories[id] = factory
}

func (r *Registry) RegisterBuiltin(id string, factory ProviderFactory) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.factories[id]; exists {
		return fmt.Errorf("web search provider type %s already registered", id)
	}
	if _, exists := r.types[id]; !exists {
		return fmt.Errorf("web search provider type %s metadata not found", id)
	}
	r.factories[id] = factory
	return nil
}

func (r *Registry) RegisterExternal(id string, factory ProviderFactory, info types.WebSearchProviderTypeInfo) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.factories[id]; exists {
		return fmt.Errorf("web search provider type %s already registered", id)
	}
	r.factories[id] = factory
	info.ID = id
	r.types[id] = info
	return nil
}

func (r *Registry) Unregister(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.factories, id)
	// Built-in metadata remains available across tests and bootstrap ordering;
	// external metadata is removed together with its runtime factory.
	if !isBuiltinProviderType(id) {
		delete(r.types, id)
	}
}

func (r *Registry) Has(id string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.factories[id]
	return ok
}

func (r *Registry) Type(id string) (types.WebSearchProviderTypeInfo, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	info, ok := r.types[id]
	return info, ok
}

func (r *Registry) Types() []types.WebSearchProviderTypeInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]types.WebSearchProviderTypeInfo, 0, len(r.factories))
	for id := range r.factories {
		if info, ok := r.types[id]; ok {
			result = append(result, info)
		}
	}
	slices.SortFunc(result, func(a, b types.WebSearchProviderTypeInfo) int {
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	return result
}

func isBuiltinProviderType(id string) bool {
	for _, info := range types.GetWebSearchProviderTypes() {
		if info.ID == id {
			return true
		}
	}
	return false
}

// CreateProvider creates a provider instance by type with the given parameters.
func (r *Registry) CreateProvider(providerType string, params types.WebSearchProviderParameters) (interfaces.WebSearchProvider, error) {
	r.mu.RLock()
	factory, ok := r.factories[providerType]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("web search provider type %s not registered", providerType)
	}
	return factory(params)
}

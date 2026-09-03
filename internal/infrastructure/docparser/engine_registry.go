package docparser

import (
	"context"
	"fmt"
	"sync"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// EngineRegistration is what every locally registered parser engine provides:
// the metadata the engine list shows, and the reader that does the parsing.
// Remote-only engines (e.g. markitdown) live in the Python docreader, are
// discovered through its ListEngines RPC, and never register here — the
// registry routes them to the docreader client by default.
type EngineRegistration interface {
	Name() string
	Description() string
	FileTypes(docreaderConnected bool) []string
	CheckAvailable(docreaderConnected bool, overrides map[string]string) (available bool, reason string)
	// NewReader builds the reader for one parse request. Returning an error
	// means this engine cannot serve the request (missing credentials,
	// unreachable service); the caller reports it rather than silently
	// parsing with something else.
	NewReader(ctx context.Context, deps ReaderDeps) (interfaces.DocReader, error)
}

// ReaderDeps carries everything an engine may need to build its reader but
// cannot construct itself: tenant configuration, tenant credentials, and the
// shared docreader connection.
type ReaderDeps struct {
	// Overrides holds tenant-level engine configuration (service endpoints,
	// API keys), as produced by ParserEngineConfig.ToOverridesMap.
	Overrides map[string]string
	// Remote is the docreader client. Nil when the service is not connected.
	Remote interfaces.DocReader
	// WeKnoraCloudCredentials resolves the tenant's WeKnora Cloud
	// credentials. It is a function rather than a value because resolving
	// them can hit the database, which most engines never need. Nil, or a
	// nil return, means the tenant has not configured them.
	WeKnoraCloudCredentials func(ctx context.Context) *types.WeKnoraCloudCredentials
}

// localEngines holds all locally registered parser engines, in registration
// order — which is also the order the engine list is shown in.
var (
	engineMu        sync.RWMutex
	localEngines    []EngineRegistration
	externalEngines = make(map[string]struct{})
)

// RegisterEngine adds an engine to the local registry. Called from init().
func RegisterEngine(e EngineRegistration) {
	engineMu.Lock()
	defer engineMu.Unlock()
	localEngines = append(localEngines, e)
}

// RegisterBuiltinEngine republishes an in-process parser after a lifecycle
// enable operation without marking it as an external plugin.
func RegisterBuiltinEngine(e EngineRegistration) error {
	if e == nil || e.Name() == "" {
		return fmt.Errorf("document parser engine name is required")
	}
	engineMu.Lock()
	defer engineMu.Unlock()
	for _, existing := range localEngines {
		if existing.Name() == e.Name() {
			return fmt.Errorf("document parser engine %q already registered", e.Name())
		}
	}
	localEngines = append(localEngines, e)
	return nil
}

func UnregisterBuiltinEngine(name string) error {
	engineMu.Lock()
	defer engineMu.Unlock()
	if _, isExternal := externalEngines[name]; isExternal {
		return fmt.Errorf("document parser engine %q is external", name)
	}
	for index, engine := range localEngines {
		if engine.Name() == name {
			localEngines = append(localEngines[:index], localEngines[index+1:]...)
			return nil
		}
	}
	return fmt.Errorf("builtin document parser engine %q is not registered", name)
}

// HasEngine reports whether a parser is currently published in the shared
// local lookup path.
func HasEngine(name string) bool {
	engineMu.RLock()
	defer engineMu.RUnlock()
	for _, engine := range localEngines {
		if engine.Name() == name {
			return true
		}
	}
	return false
}

// BuiltinEngines returns a snapshot suitable for lifecycle adoption.
func BuiltinEngines() []EngineRegistration {
	engineMu.RLock()
	defer engineMu.RUnlock()
	result := make([]EngineRegistration, 0, len(localEngines))
	for _, engine := range localEngines {
		if _, isExternal := externalEngines[engine.Name()]; !isExternal {
			result = append(result, engine)
		}
	}
	return result
}

// RegisterExternalEngine publishes an out-of-process engine into the same
// catalog and lookup path as built-in engines.
func RegisterExternalEngine(e EngineRegistration) error {
	if e == nil || e.Name() == "" {
		return fmt.Errorf("document parser engine name is required")
	}
	engineMu.Lock()
	defer engineMu.Unlock()
	for _, existing := range localEngines {
		if existing.Name() == e.Name() {
			return fmt.Errorf("document parser engine %q already registered", e.Name())
		}
	}
	localEngines = append(localEngines, e)
	externalEngines[e.Name()] = struct{}{}
	return nil
}

func UnregisterExternalEngine(name string) error {
	engineMu.Lock()
	defer engineMu.Unlock()
	if _, ok := externalEngines[name]; !ok {
		return fmt.Errorf("external document parser engine %q is not registered", name)
	}
	for index, engine := range localEngines {
		if engine.Name() == name {
			localEngines = append(localEngines[:index], localEngines[index+1:]...)
			break
		}
	}
	delete(externalEngines, name)
	return nil
}

// lookupEngine returns the locally registered engine with this name.
func lookupEngine(name string) (EngineRegistration, bool) {
	engineMu.RLock()
	defer engineMu.RUnlock()
	for _, engine := range localEngines {
		if engine.Name() == name {
			return engine, true
		}
	}
	return nil, false
}

// NewReader builds the reader for an engine.
//
// An empty engine name means "no explicit choice": simple formats are handled
// in Go and everything else goes to the docreader service. An unknown name is
// routed to the docreader too, so engines that only exist in the Python
// service keep working without a Go-side registration.
func NewReader(
	ctx context.Context, engine, fileType string, isURL bool, deps ReaderDeps,
) (interfaces.DocReader, error) {
	if registration, ok := lookupEngine(engine); ok {
		return registration.NewReader(ctx, deps)
	}
	if engine == "" && !isURL && IsSimpleFormat(fileType) {
		return &SimpleFormatReader{}, nil
	}
	return remoteReader(deps)
}

// remoteReader returns the docreader client, or an error when the service is
// not connected — a nil interface value here would panic at the call site.
func remoteReader(deps ReaderDeps) (interfaces.DocReader, error) {
	if deps.Remote == nil {
		return nil, errNotConnected
	}
	return deps.Remote, nil
}

// ListAllEngines returns the merged engine list: locally registered engines
// plus engines discovered from the remote docreader via ListEngines RPC.
//
// Merge rules:
//   - Local engines are always included, with Go-side availability checks.
//   - For a remote engine whose name matches a local one, the remote's
//     file_types and description take precedence (the remote service is
//     authoritative for its own capabilities).
//   - Remote engines not present locally are appended as-is, enabling
//     auto-discovery of newly added docreader engines without Go changes.
func ListAllEngines(
	docreaderConnected bool, overrides map[string]string, remoteEngines []types.ParserEngineInfo,
) []types.ParserEngineInfo {
	engineMu.RLock()
	engines := append([]EngineRegistration(nil), localEngines...)
	engineMu.RUnlock()
	remoteMap := make(map[string]types.ParserEngineInfo, len(remoteEngines))
	for _, re := range remoteEngines {
		remoteMap[re.Name] = re
	}

	seen := make(map[string]bool, len(engines))
	result := make([]types.ParserEngineInfo, 0, len(engines)+len(remoteEngines))

	for _, e := range engines {
		name := e.Name()
		seen[name] = true

		fileTypes := e.FileTypes(docreaderConnected)
		description := e.Description()

		if re, ok := remoteMap[name]; ok {
			if len(re.FileTypes) > 0 {
				fileTypes = re.FileTypes
			}
			if re.Description != "" {
				description = re.Description
			}
		}

		available, reason := e.CheckAvailable(docreaderConnected, overrides)
		result = append(result, types.ParserEngineInfo{
			Name:              name,
			Description:       description,
			FileTypes:         fileTypes,
			Available:         available,
			UnavailableReason: reason,
		})
	}

	for _, re := range remoteEngines {
		if seen[re.Name] {
			continue
		}
		result = append(result, re)
	}

	return result
}

// errEngineUnavailable reports an engine that is registered but cannot run for
// this tenant or this build.
func errEngineUnavailable(engine, reason string) error {
	return fmt.Errorf("parser engine %q is unavailable: %s", engine, reason)
}

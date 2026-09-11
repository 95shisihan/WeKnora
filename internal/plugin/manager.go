package plugin

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

type State string

const (
	StateDisabled  State = "disabled"
	StateStarting  State = "starting"
	StateHealthy   State = "healthy"
	StateUnhealthy State = "unhealthy"
	StateStopped   State = "stopped"
	StateIgnored   State = "ignored"
)

type Status struct {
	Network          *NetworkPermission `json:"network,omitempty"`
	HTTPPolicyDigest string             `json:"http_policy_digest,omitempty"`
	HTTPApproved     bool               `json:"http_approved,omitempty"`
	PluginID         string             `json:"plugin_id"`
	Name             string             `json:"name"`
	Version          string             `json:"version"`
	ExtensionTypes   []string           `json:"extension_types"`
	State            State              `json:"state"`
	LastError        string             `json:"last_error,omitempty"`
	CheckedAt        time.Time          `json:"checked_at"`
}

type EnabledExtensions struct {
	Datasources      []*LoadedDatasource
	WebSearches      []*LoadedWebSearch
	DocumentParsers  []*LoadedDocumentParser
	ModelProviders   []*LoadedModelProvider
	RetrievalEngines []*LoadedRetrievalEngine
}

type DisabledExtensions struct {
	DatasourceTypes      []string
	WebSearchTypes       []string
	DocumentParserTypes  []string
	ModelProviderTypes   []string
	RetrievalEngineTypes []string
}

// Manager owns the shared state machine for in-process built-ins and external
// plugin discovery/process/container lifecycles. Built-ins supply registry
// callbacks; external business registries consume adapters after startup and
// identity checks succeed.
type Manager struct {
	mu               sync.RWMutex
	opMu             sync.Mutex
	hostVersion      string
	statuses         map[string]Status
	manifests        map[string]*Manifest
	builtins         map[string]*BuiltinRegistration
	builtinRunning   map[string]bool
	datasources      []*LoadedDatasource
	webSearches      []*LoadedWebSearch
	documentParsers  []*LoadedDocumentParser
	modelProviders   []*LoadedModelProvider
	retrievalEngines []*LoadedRetrievalEngine
	stopMonitors     map[string][]func() error
	healthInterval   time.Duration
	onStateChange    func(Status)
	closed           bool
}

func NewManager(hostVersion string, onStateChange func(Status)) *Manager {
	return &Manager{
		hostVersion:    hostVersion,
		statuses:       make(map[string]Status),
		manifests:      make(map[string]*Manifest),
		builtins:       make(map[string]*BuiltinRegistration),
		builtinRunning: make(map[string]bool),
		stopMonitors:   make(map[string][]func() error),
		onStateChange:  onStateChange,
	}
}

func (m *Manager) LoadDirectories(ctx context.Context, dirs []string) error {
	manifests, err := Discover(dirs, m.hostVersion)
	if err != nil {
		return err
	}
	return m.Load(ctx, manifests)
}

// InstallArchive safely persists an uploaded plugin package and registers its
// manifest in the disabled state. Starting the plugin remains a separate
// operation so callers can report installation and runtime failures clearly.
func (m *Manager) InstallArchive(archive []byte, installRoot string) (*Manifest, error) {
	m.opMu.Lock()
	defer m.opMu.Unlock()

	m.mu.RLock()
	closed := m.closed
	m.mu.RUnlock()
	if closed {
		return nil, fmt.Errorf("plugin manager is closed")
	}
	installRoot = strings.TrimSpace(installRoot)
	if installRoot == "" {
		return nil, fmt.Errorf("plugin install directory is not configured")
	}
	root, err := filepath.Abs(installRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve plugin install directory: %w", err)
	}
	if err := os.MkdirAll(root, 0o750); err != nil {
		return nil, fmt.Errorf("create plugin install directory: %w", err)
	}
	stage, err := os.MkdirTemp(root, ".installing-")
	if err != nil {
		return nil, fmt.Errorf("create plugin staging directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(stage) }()
	if err := unpackPluginArchive(archive, stage); err != nil {
		return nil, err
	}
	manifest, err := LoadManifest(filepath.Join(stage, "plugin.yaml"), m.hostVersion)
	if err != nil {
		return nil, err
	}
	if err := validateCompiledPluginArtifact(manifest); err != nil {
		return nil, err
	}

	m.mu.RLock()
	_, builtinExists := m.builtins[manifest.Metadata.ID]
	_, manifestExists := m.manifests[manifest.Metadata.ID]
	m.mu.RUnlock()
	if builtinExists || manifestExists {
		return nil, fmt.Errorf("plugin %s is already installed", manifest.Metadata.ID)
	}
	target := filepath.Join(root, manifest.Metadata.ID)
	if _, err := os.Stat(target); err == nil {
		return nil, fmt.Errorf("plugin %s already exists on disk", manifest.Metadata.ID)
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("inspect plugin target: %w", err)
	}
	if err := os.Rename(stage, target); err != nil {
		return nil, fmt.Errorf("commit plugin installation: %w", err)
	}
	manifest, err = LoadManifest(filepath.Join(target, "plugin.yaml"), m.hostVersion)
	if err != nil {
		_ = os.RemoveAll(target)
		return nil, err
	}
	m.mu.Lock()
	m.manifests[manifest.Metadata.ID] = manifest
	m.mu.Unlock()
	status := statusFromManifest(manifest)
	status.State = StateDisabled
	m.setStatus(status)
	return manifest, nil
}

func (m *Manager) Load(ctx context.Context, manifests []*Manifest) error {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return fmt.Errorf("plugin manager is closed")
	}
	m.mu.Unlock()

	var result error
	for _, manifest := range manifests {
		m.mu.Lock()
		if _, exists := m.builtins[manifest.Metadata.ID]; exists {
			m.mu.Unlock()
			result = errors.Join(result, fmt.Errorf("external plugin id %s collides with a builtin plugin", manifest.Metadata.ID))
			continue
		}
		m.manifests[manifest.Metadata.ID] = manifest
		m.mu.Unlock()
		status := statusFromManifest(manifest)
		if !manifest.Enabled() || !httpApproved(manifest) {
			status.State = StateDisabled
			m.setStatus(status)
			continue
		}
		extensionType := manifest.Spec.ExtensionPoints[0].Type
		if extensionType != ExtensionDatasource && extensionType != ExtensionWebSearch && extensionType != ExtensionDocumentParser && extensionType != ExtensionModelProvider && extensionType != ExtensionRetrievalEngine {
			status.State = StateIgnored
			status.LastError = "extension type is not implemented by this host"
			m.setStatus(status)
			result = errors.Join(result, fmt.Errorf("plugin %s: %s", manifest.Metadata.ID, status.LastError))
			continue
		}

		status.State = StateStarting
		m.setStatus(status)
		if extensionType == ExtensionRetrievalEngine {
			loaded, err := LoadRetrievalEngine(ctx, manifest)
			if err != nil {
				status.State, status.LastError = StateUnhealthy, err.Error()
				m.setStatus(status)
				result = errors.Join(result, err)
				continue
			}
			m.mu.Lock()
			m.retrievalEngines = append(m.retrievalEngines, loaded)
			m.mu.Unlock()
			status.State, status.LastError = StateHealthy, ""
			m.setStatus(status)
			continue
		}
		if extensionType == ExtensionModelProvider {
			loaded, err := LoadModelProvider(ctx, manifest)
			if err != nil {
				status.State, status.LastError = StateUnhealthy, err.Error()
				m.setStatus(status)
				result = errors.Join(result, err)
				continue
			}
			m.mu.Lock()
			m.modelProviders = append(m.modelProviders, loaded)
			m.mu.Unlock()
			status.State, status.LastError = StateHealthy, ""
			m.setStatus(status)
			continue
		}
		if extensionType == ExtensionDocumentParser {
			loaded, err := LoadDocumentParser(ctx, manifest)
			if err != nil {
				status.State, status.LastError = StateUnhealthy, err.Error()
				m.setStatus(status)
				result = errors.Join(result, err)
				continue
			}
			m.mu.Lock()
			m.documentParsers = append(m.documentParsers, loaded)
			m.mu.Unlock()
			status.State, status.LastError = StateHealthy, ""
			m.setStatus(status)
			continue
		}
		if extensionType == ExtensionWebSearch {
			loaded, err := LoadWebSearch(ctx, manifest)
			if err != nil {
				status.State, status.LastError = StateUnhealthy, err.Error()
				m.setStatus(status)
				result = errors.Join(result, err)
				continue
			}
			m.mu.Lock()
			m.webSearches = append(m.webSearches, loaded)
			m.mu.Unlock()
			status.State, status.LastError = StateHealthy, ""
			m.setStatus(status)
			continue
		}

		loaded, err := LoadDatasources(ctx, []*Manifest{manifest})
		if err != nil {
			status.State = StateUnhealthy
			status.LastError = err.Error()
			m.setStatus(status)
			result = errors.Join(result, err)
			continue
		}
		if len(loaded) == 0 {
			status.State = StateIgnored
			status.LastError = "manifest has no supported datasource extension"
			m.setStatus(status)
			continue
		}

		m.mu.Lock()
		m.datasources = append(m.datasources, loaded...)
		m.mu.Unlock()
		status.State = StateHealthy
		status.LastError = ""
		m.setStatus(status)
	}
	return result
}

func (m *Manager) StartHealthChecks(interval time.Duration) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.healthInterval = interval
	plugins := append([]*LoadedDatasource(nil), m.datasources...)
	webSearches := append([]*LoadedWebSearch(nil), m.webSearches...)
	documentParsers := append([]*LoadedDocumentParser(nil), m.documentParsers...)
	modelProviders := append([]*LoadedModelProvider(nil), m.modelProviders...)
	retrievalEngines := append([]*LoadedRetrievalEngine(nil), m.retrievalEngines...)
	builtins := make([]*BuiltinRegistration, 0, len(m.builtins))
	for _, registration := range m.builtins {
		builtins = append(builtins, registration)
	}
	m.mu.Unlock()

	for _, loaded := range plugins {
		m.startHealthCheck(loaded, interval)
	}
	for _, loaded := range webSearches {
		m.startWebHealthCheck(loaded, interval)
	}
	for _, loaded := range documentParsers {
		m.startDocumentParserHealthCheck(loaded, interval)
	}
	for _, loaded := range modelProviders {
		m.startModelProviderHealthCheck(loaded, interval)
	}
	for _, loaded := range retrievalEngines {
		m.startRetrievalEngineHealthCheck(loaded, interval)
	}
	for _, registration := range builtins {
		m.startBuiltinHealthCheck(registration, interval)
	}
}

func (m *Manager) startDocumentParserHealthCheck(plugin *LoadedDocumentParser, interval time.Duration) {
	m.startGenericHealthCheck(plugin.Manifest, interval, plugin.Health)
}

func (m *Manager) startModelProviderHealthCheck(plugin *LoadedModelProvider, interval time.Duration) {
	m.startGenericHealthCheck(plugin.Manifest, interval, plugin.Health)
}

func (m *Manager) startRetrievalEngineHealthCheck(plugin *LoadedRetrievalEngine, interval time.Duration) {
	m.startGenericHealthCheck(plugin.Manifest, interval, plugin.Health)
}

func (m *Manager) startGenericHealthCheck(manifest *Manifest, interval time.Duration, health func(context.Context) error) {
	m.mu.Lock()
	if m.closed || len(m.stopMonitors[manifest.Metadata.ID]) > 0 {
		m.mu.Unlock()
		return
	}
	m.mu.Unlock()
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
				probeCtx, stop := context.WithTimeout(ctx, 5*time.Second)
				err := health(probeCtx)
				stop()
				next := err != nil
				if next != unhealthy {
					status, _ := m.Status(manifest.Metadata.ID)
					if err == nil {
						status.State, status.LastError = StateHealthy, ""
					} else {
						status.State, status.LastError = StateUnhealthy, err.Error()
					}
					m.setStatus(status)
				}
				unhealthy = next
			}
		}
	}()
	m.mu.Lock()
	m.stopMonitors[manifest.Metadata.ID] = append(m.stopMonitors[manifest.Metadata.ID], func() error { cancel(); <-done; return nil })
	m.mu.Unlock()
}

func (m *Manager) startWebHealthCheck(plugin *LoadedWebSearch, interval time.Duration) {
	m.startGenericHealthCheck(plugin.Manifest, interval, plugin.Health)
}

func (m *Manager) startHealthCheck(plugin *LoadedDatasource, interval time.Duration) {
	m.mu.Lock()
	if m.closed || len(m.stopMonitors[plugin.Manifest.Metadata.ID]) > 0 {
		m.mu.Unlock()
		return
	}
	m.mu.Unlock()
	stop := plugin.StartHealthMonitor(interval, func(err error) {
		current, ok := m.Status(plugin.Manifest.Metadata.ID)
		if !ok {
			current = statusFromManifest(plugin.Manifest)
		}
		if err == nil {
			current.State = StateHealthy
			current.LastError = ""
		} else {
			current.State = StateUnhealthy
			current.LastError = err.Error()
		}
		m.setStatus(current)
	})
	m.mu.Lock()
	m.stopMonitors[plugin.Manifest.Metadata.ID] = append(m.stopMonitors[plugin.Manifest.Metadata.ID], stop)
	m.mu.Unlock()
}

// Enable starts a previously discovered plugin. Registration into a business
// registry is left to the caller, which can roll back with Disable on failure.
func (m *Manager) Enable(ctx context.Context, pluginID string) (*EnabledExtensions, error) {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	if m.IsBuiltin(pluginID) {
		return m.enableBuiltin(ctx, pluginID)
	}
	m.mu.RLock()
	manifest, ok := m.manifests[pluginID]
	closed := m.closed
	for _, loaded := range m.datasources {
		if loaded.Manifest.Metadata.ID == pluginID {
			m.mu.RUnlock()
			return nil, fmt.Errorf("plugin %s is already running", pluginID)
		}
	}
	for _, loaded := range m.webSearches {
		if loaded.Manifest.Metadata.ID == pluginID {
			m.mu.RUnlock()
			return nil, fmt.Errorf("plugin %s is already running", pluginID)
		}
	}
	for _, loaded := range m.documentParsers {
		if loaded.Manifest.Metadata.ID == pluginID {
			m.mu.RUnlock()
			return nil, fmt.Errorf("plugin %s is already running", pluginID)
		}
	}
	for _, loaded := range m.modelProviders {
		if loaded.Manifest.Metadata.ID == pluginID {
			m.mu.RUnlock()
			return nil, fmt.Errorf("plugin %s is already running", pluginID)
		}
	}
	for _, loaded := range m.retrievalEngines {
		if loaded.Manifest.Metadata.ID == pluginID {
			m.mu.RUnlock()
			return nil, fmt.Errorf("plugin %s is already running", pluginID)
		}
	}
	m.mu.RUnlock()
	if closed {
		return nil, fmt.Errorf("plugin manager is closed")
	}
	if !ok {
		return nil, fmt.Errorf("plugin %s was not discovered", pluginID)
	}

	copyManifest := *manifest
	if !httpApproved(manifest) {
		return nil, fmt.Errorf("HTTP permissions require administrator approval")
	}
	copySpec := manifest.Spec
	enabled := true
	copySpec.Enabled = &enabled
	copyManifest.Spec = copySpec
	status := statusFromManifest(manifest)
	status.State = StateStarting
	m.setStatus(status)
	if copyManifest.Spec.ExtensionPoints[0].Type == ExtensionRetrievalEngine {
		loaded, err := LoadRetrievalEngine(ctx, &copyManifest)
		if err != nil {
			status.State, status.LastError = StateUnhealthy, err.Error()
			m.setStatus(status)
			return nil, err
		}
		m.mu.Lock()
		m.retrievalEngines = append(m.retrievalEngines, loaded)
		interval := m.healthInterval
		m.mu.Unlock()
		status.State, status.LastError = StateHealthy, ""
		m.setStatus(status)
		if interval > 0 {
			m.startRetrievalEngineHealthCheck(loaded, interval)
		}
		return &EnabledExtensions{RetrievalEngines: []*LoadedRetrievalEngine{loaded}}, nil
	}
	if copyManifest.Spec.ExtensionPoints[0].Type == ExtensionModelProvider {
		loaded, err := LoadModelProvider(ctx, &copyManifest)
		if err != nil {
			status.State, status.LastError = StateUnhealthy, err.Error()
			m.setStatus(status)
			return nil, err
		}
		m.mu.Lock()
		m.modelProviders = append(m.modelProviders, loaded)
		interval := m.healthInterval
		m.mu.Unlock()
		status.State, status.LastError = StateHealthy, ""
		m.setStatus(status)
		if interval > 0 {
			m.startModelProviderHealthCheck(loaded, interval)
		}
		return &EnabledExtensions{ModelProviders: []*LoadedModelProvider{loaded}}, nil
	}
	if copyManifest.Spec.ExtensionPoints[0].Type == ExtensionDocumentParser {
		loaded, err := LoadDocumentParser(ctx, &copyManifest)
		if err != nil {
			status.State, status.LastError = StateUnhealthy, err.Error()
			m.setStatus(status)
			return nil, err
		}
		m.mu.Lock()
		m.documentParsers = append(m.documentParsers, loaded)
		interval := m.healthInterval
		m.mu.Unlock()
		status.State, status.LastError = StateHealthy, ""
		m.setStatus(status)
		if interval > 0 {
			m.startDocumentParserHealthCheck(loaded, interval)
		}
		return &EnabledExtensions{DocumentParsers: []*LoadedDocumentParser{loaded}}, nil
	}
	if copyManifest.Spec.ExtensionPoints[0].Type == ExtensionWebSearch {
		loaded, err := LoadWebSearch(ctx, &copyManifest)
		if err != nil {
			status.State, status.LastError = StateUnhealthy, err.Error()
			m.setStatus(status)
			return nil, err
		}
		m.mu.Lock()
		m.webSearches = append(m.webSearches, loaded)
		interval := m.healthInterval
		m.mu.Unlock()
		status.State, status.LastError = StateHealthy, ""
		m.setStatus(status)
		if interval > 0 {
			m.startWebHealthCheck(loaded, interval)
		}
		return &EnabledExtensions{WebSearches: []*LoadedWebSearch{loaded}}, nil
	}
	loaded, err := LoadDatasources(ctx, []*Manifest{&copyManifest})
	if err != nil {
		status.State = StateUnhealthy
		status.LastError = err.Error()
		m.setStatus(status)
		return nil, err
	}
	m.mu.Lock()
	m.datasources = append(m.datasources, loaded...)
	interval := m.healthInterval
	m.mu.Unlock()
	status.State = StateHealthy
	status.LastError = ""
	m.setStatus(status)
	if interval > 0 {
		for _, datasource := range loaded {
			m.startHealthCheck(datasource, interval)
		}
	}
	return &EnabledExtensions{Datasources: append([]*LoadedDatasource(nil), loaded...)}, nil
}

// Disable stops a running plugin and returns its connector types. Callers
// should remove those types from business registries before invoking it.
func (m *Manager) Disable(pluginID string) (*DisabledExtensions, error) {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	if m.IsBuiltin(pluginID) {
		return m.disableBuiltin(pluginID)
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, fmt.Errorf("plugin manager is closed")
	}
	var targets []*LoadedDatasource
	var webTargets []*LoadedWebSearch
	var parserTargets []*LoadedDocumentParser
	var modelTargets []*LoadedModelProvider
	var retrievalTargets []*LoadedRetrievalEngine
	remaining := m.datasources[:0]
	for _, loaded := range m.datasources {
		if loaded.Manifest.Metadata.ID == pluginID {
			targets = append(targets, loaded)
		} else {
			remaining = append(remaining, loaded)
		}
	}
	webRemaining := m.webSearches[:0]
	for _, loaded := range m.webSearches {
		if loaded.Manifest.Metadata.ID == pluginID {
			webTargets = append(webTargets, loaded)
		} else {
			webRemaining = append(webRemaining, loaded)
		}
	}
	parserRemaining := m.documentParsers[:0]
	for _, loaded := range m.documentParsers {
		if loaded.Manifest.Metadata.ID == pluginID {
			parserTargets = append(parserTargets, loaded)
		} else {
			parserRemaining = append(parserRemaining, loaded)
		}
	}
	modelRemaining := m.modelProviders[:0]
	for _, loaded := range m.modelProviders {
		if loaded.Manifest.Metadata.ID == pluginID {
			modelTargets = append(modelTargets, loaded)
		} else {
			modelRemaining = append(modelRemaining, loaded)
		}
	}
	retrievalRemaining := m.retrievalEngines[:0]
	for _, loaded := range m.retrievalEngines {
		if loaded.Manifest.Metadata.ID == pluginID {
			retrievalTargets = append(retrievalTargets, loaded)
		} else {
			retrievalRemaining = append(retrievalRemaining, loaded)
		}
	}
	if len(targets) == 0 && len(webTargets) == 0 && len(parserTargets) == 0 && len(modelTargets) == 0 && len(retrievalTargets) == 0 {
		m.mu.Unlock()
		return nil, fmt.Errorf("plugin %s is not running", pluginID)
	}
	m.datasources = remaining
	m.webSearches = webRemaining
	m.documentParsers = parserRemaining
	m.modelProviders = modelRemaining
	m.retrievalEngines = retrievalRemaining
	monitors := append([]func() error(nil), m.stopMonitors[pluginID]...)
	delete(m.stopMonitors, pluginID)
	m.mu.Unlock()

	var result error
	for i := len(monitors) - 1; i >= 0; i-- {
		result = errors.Join(result, monitors[i]())
	}
	disabled := &DisabledExtensions{DatasourceTypes: make([]string, 0, len(targets)), WebSearchTypes: make([]string, 0, len(webTargets)), DocumentParserTypes: make([]string, 0, len(parserTargets)), ModelProviderTypes: make([]string, 0, len(modelTargets)), RetrievalEngineTypes: make([]string, 0, len(retrievalTargets))}
	for i := len(targets) - 1; i >= 0; i-- {
		disabled.DatasourceTypes = append(disabled.DatasourceTypes, targets[i].Metadata.Type)
		result = errors.Join(result, targets[i].Close())
	}
	for i := len(webTargets) - 1; i >= 0; i-- {
		disabled.WebSearchTypes = append(disabled.WebSearchTypes, webTargets[i].Point.ID)
		result = errors.Join(result, webTargets[i].Close())
	}
	for i := len(parserTargets) - 1; i >= 0; i-- {
		disabled.DocumentParserTypes = append(disabled.DocumentParserTypes, parserTargets[i].Point.ID)
		result = errors.Join(result, parserTargets[i].Close())
	}
	for i := len(modelTargets) - 1; i >= 0; i-- {
		disabled.ModelProviderTypes = append(disabled.ModelProviderTypes, modelTargets[i].Point.ID)
		result = errors.Join(result, modelTargets[i].Close())
	}
	for i := len(retrievalTargets) - 1; i >= 0; i-- {
		disabled.RetrievalEngineTypes = append(disabled.RetrievalEngineTypes, retrievalTargets[i].Point.ID)
		result = errors.Join(result, retrievalTargets[i].Close())
	}
	var stoppedManifest *Manifest
	if len(targets) > 0 {
		stoppedManifest = targets[0].Manifest
	} else if len(webTargets) > 0 {
		stoppedManifest = webTargets[0].Manifest
	} else if len(parserTargets) > 0 {
		stoppedManifest = parserTargets[0].Manifest
	} else if len(modelTargets) > 0 {
		stoppedManifest = modelTargets[0].Manifest
	} else {
		stoppedManifest = retrievalTargets[0].Manifest
	}
	status := statusFromManifest(stoppedManifest)
	status.State = StateDisabled
	if result != nil {
		status.LastError = result.Error()
	}
	m.setStatus(status)
	return disabled, result
}

func (m *Manager) DatasourceTypes(pluginID string) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []string
	for _, loaded := range m.datasources {
		if loaded.Manifest.Metadata.ID == pluginID {
			result = append(result, loaded.Metadata.Type)
		}
	}
	return result
}

func (m *Manager) WebSearchTypes(pluginID string) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []string
	for _, loaded := range m.webSearches {
		if loaded.Manifest.Metadata.ID == pluginID {
			result = append(result, loaded.Point.ID)
		}
	}
	return result
}

func (m *Manager) DocumentParserTypes(pluginID string) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []string
	for _, loaded := range m.documentParsers {
		if loaded.Manifest.Metadata.ID == pluginID {
			result = append(result, loaded.Point.ID)
		}
	}
	return result
}

func (m *Manager) ModelProviderTypes(pluginID string) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []string
	for _, loaded := range m.modelProviders {
		if loaded.Manifest.Metadata.ID == pluginID {
			result = append(result, loaded.Point.ID)
		}
	}
	return result
}

func (m *Manager) RetrievalEngineTypes(pluginID string) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []string
	for _, loaded := range m.retrievalEngines {
		if loaded.Manifest.Metadata.ID == pluginID {
			result = append(result, loaded.Point.ID)
		}
	}
	return result
}

func (m *Manager) Datasources() []*LoadedDatasource {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]*LoadedDatasource(nil), m.datasources...)
}

func (m *Manager) WebSearches() []*LoadedWebSearch {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]*LoadedWebSearch(nil), m.webSearches...)
}

func (m *Manager) DocumentParsers() []*LoadedDocumentParser {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]*LoadedDocumentParser(nil), m.documentParsers...)
}

func (m *Manager) ModelProviders() []*LoadedModelProvider {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]*LoadedModelProvider(nil), m.modelProviders...)
}

func (m *Manager) RetrievalEngines() []*LoadedRetrievalEngine {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]*LoadedRetrievalEngine(nil), m.retrievalEngines...)
}

func (m *Manager) Status(pluginID string) (Status, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	status, ok := m.statuses[pluginID]
	status.ExtensionTypes = append([]string(nil), status.ExtensionTypes...)
	return status, ok
}

func (m *Manager) Statuses() []Status {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]Status, 0, len(m.statuses))
	for _, status := range m.statuses {
		status.ExtensionTypes = append([]string(nil), status.ExtensionTypes...)
		result = append(result, status)
	}
	slices.SortFunc(result, func(left, right Status) int {
		if left.PluginID < right.PluginID {
			return -1
		}
		if left.PluginID > right.PluginID {
			return 1
		}
		return 0
	})
	return result
}

func (m *Manager) Close() error {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	var monitors []func() error
	for _, pluginMonitors := range m.stopMonitors {
		monitors = append(monitors, pluginMonitors...)
	}
	plugins := append([]*LoadedDatasource(nil), m.datasources...)
	webSearches := append([]*LoadedWebSearch(nil), m.webSearches...)
	documentParsers := append([]*LoadedDocumentParser(nil), m.documentParsers...)
	modelProviders := append([]*LoadedModelProvider(nil), m.modelProviders...)
	retrievalEngines := append([]*LoadedRetrievalEngine(nil), m.retrievalEngines...)
	builtinIDs := make([]string, 0, len(m.builtins))
	for pluginID := range m.builtins {
		builtinIDs = append(builtinIDs, pluginID)
	}
	m.stopMonitors = make(map[string][]func() error)
	m.mu.Unlock()

	var result error
	for i := len(monitors) - 1; i >= 0; i-- {
		result = errors.Join(result, monitors[i]())
	}
	for i := len(plugins) - 1; i >= 0; i-- {
		result = errors.Join(result, plugins[i].Close())
		current, ok := m.Status(plugins[i].Manifest.Metadata.ID)
		if ok {
			current.State = StateStopped
			m.setStatus(current)
		}
	}
	for i := len(webSearches) - 1; i >= 0; i-- {
		result = errors.Join(result, webSearches[i].Close())
		current, ok := m.Status(webSearches[i].Manifest.Metadata.ID)
		if ok {
			current.State = StateStopped
			m.setStatus(current)
		}
	}
	for i := len(documentParsers) - 1; i >= 0; i-- {
		result = errors.Join(result, documentParsers[i].Close())
		current, ok := m.Status(documentParsers[i].Manifest.Metadata.ID)
		if ok {
			current.State = StateStopped
			m.setStatus(current)
		}
	}
	for i := len(modelProviders) - 1; i >= 0; i-- {
		result = errors.Join(result, modelProviders[i].Close())
		current, ok := m.Status(modelProviders[i].Manifest.Metadata.ID)
		if ok {
			current.State = StateStopped
			m.setStatus(current)
		}
	}
	for i := len(retrievalEngines) - 1; i >= 0; i-- {
		result = errors.Join(result, retrievalEngines[i].Close())
		current, ok := m.Status(retrievalEngines[i].Manifest.Metadata.ID)
		if ok {
			current.State = StateStopped
			m.setStatus(current)
		}
	}
	for _, pluginID := range builtinIDs {
		current, ok := m.Status(pluginID)
		if ok {
			current.State = StateStopped
			m.setStatus(current)
		}
	}
	return result
}

func (m *Manager) setStatus(status Status) {
	status.CheckedAt = time.Now().UTC()
	m.mu.Lock()
	m.statuses[status.PluginID] = status
	callback := m.onStateChange
	m.mu.Unlock()
	if callback != nil {
		callback(status)
	}
}

func statusFromManifest(manifest *Manifest) Status {
	types := make([]string, 0, len(manifest.Spec.ExtensionPoints))
	for _, point := range manifest.Spec.ExtensionPoints {
		if !slices.Contains(types, point.Type) {
			types = append(types, point.Type)
		}
	}
	return Status{
		Network: &manifest.Spec.Permissions.Network, HTTPPolicyDigest: httpPolicyDigest(manifest), HTTPApproved: httpApproved(manifest),
		PluginID: manifest.Metadata.ID, Name: manifest.Metadata.Name,
		Version: manifest.Metadata.Version, ExtensionTypes: types,
	}
}

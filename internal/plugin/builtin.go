package plugin

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// BuiltinRegistration adapts an in-process extension to the same lifecycle
// state machine used by external gRPC and OCI plugins. Enable and Disable own
// publication in the extension's business registry; Health is optional for
// metadata-only implementations that have no tenant-independent probe.
type BuiltinRegistration struct {
	PluginID      string
	Name          string
	Version       string
	ExtensionType string
	ExtensionID   string
	Enable        func(context.Context) error
	Disable       func() error
	Health        func(context.Context) error
}

func (r BuiltinRegistration) validate() error {
	if strings.TrimSpace(r.PluginID) == "" || strings.TrimSpace(r.Name) == "" {
		return fmt.Errorf("builtin plugin id and name are required")
	}
	if _, ok := knownExtensionTypes[r.ExtensionType]; !ok {
		return fmt.Errorf("builtin plugin %s has unknown extension type %q", r.PluginID, r.ExtensionType)
	}
	if strings.TrimSpace(r.ExtensionID) == "" {
		return fmt.Errorf("builtin plugin %s extension id is required", r.PluginID)
	}
	if r.Enable == nil || r.Disable == nil {
		return fmt.Errorf("builtin plugin %s enable and disable callbacks are required", r.PluginID)
	}
	return nil
}

// RegisterBuiltin publishes an in-process extension and records it as healthy
// only after its business-registry enable callback succeeds.
func (m *Manager) RegisterBuiltin(ctx context.Context, registration BuiltinRegistration) error {
	if err := registration.validate(); err != nil {
		return err
	}
	if registration.Version == "" {
		registration.Version = m.hostVersion
	}
	m.opMu.Lock()
	defer m.opMu.Unlock()

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return fmt.Errorf("plugin manager is closed")
	}
	if _, exists := m.builtins[registration.PluginID]; exists {
		m.mu.Unlock()
		return fmt.Errorf("plugin %s is already registered", registration.PluginID)
	}
	if _, exists := m.manifests[registration.PluginID]; exists {
		m.mu.Unlock()
		return fmt.Errorf("plugin id %s collides with an external manifest", registration.PluginID)
	}
	copyRegistration := registration
	m.builtins[registration.PluginID] = &copyRegistration
	m.builtinRunning[registration.PluginID] = false
	m.mu.Unlock()

	status := statusFromBuiltin(&copyRegistration)
	status.State = StateStarting
	m.setStatus(status)
	if err := registration.Enable(ctx); err != nil {
		status.State, status.LastError = StateUnhealthy, err.Error()
		m.setStatus(status)
		return fmt.Errorf("enable builtin plugin %s: %w", registration.PluginID, err)
	}
	m.mu.Lock()
	m.builtinRunning[registration.PluginID] = true
	m.mu.Unlock()
	status.State = StateHealthy
	m.setStatus(status)

	m.mu.RLock()
	interval := m.healthInterval
	m.mu.RUnlock()
	if interval > 0 {
		m.startBuiltinHealthCheck(&copyRegistration, interval)
	}
	return nil
}

// IsBuiltin reports whether the ID is an in-process extension whose registry
// callbacks are owned directly by the manager.
func (m *Manager) IsBuiltin(pluginID string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.builtins[pluginID]
	return ok
}

func (m *Manager) enableBuiltin(ctx context.Context, pluginID string) (*EnabledExtensions, error) {
	m.mu.RLock()
	registration, ok := m.builtins[pluginID]
	running := m.builtinRunning[pluginID]
	status := m.statuses[pluginID]
	closed := m.closed
	m.mu.RUnlock()
	if closed {
		return nil, fmt.Errorf("plugin manager is closed")
	}
	if !ok {
		return nil, fmt.Errorf("builtin plugin %s is not registered", pluginID)
	}
	if running {
		return nil, fmt.Errorf("plugin %s is already running", pluginID)
	}
	status.State, status.LastError = StateStarting, ""
	m.setStatus(status)
	if err := registration.Enable(ctx); err != nil {
		status.State, status.LastError = StateUnhealthy, err.Error()
		m.setStatus(status)
		return nil, fmt.Errorf("enable builtin plugin %s: %w", pluginID, err)
	}
	m.mu.Lock()
	m.builtinRunning[pluginID] = true
	m.mu.Unlock()
	status.State, status.LastError = StateHealthy, ""
	m.setStatus(status)
	m.mu.RLock()
	interval := m.healthInterval
	m.mu.RUnlock()
	if interval > 0 {
		m.startBuiltinHealthCheck(registration, interval)
	}
	return &EnabledExtensions{}, nil
}

func (m *Manager) disableBuiltin(pluginID string) (*DisabledExtensions, error) {
	m.mu.RLock()
	registration, ok := m.builtins[pluginID]
	running := m.builtinRunning[pluginID]
	status := m.statuses[pluginID]
	closed := m.closed
	m.mu.RUnlock()
	if closed {
		return nil, fmt.Errorf("plugin manager is closed")
	}
	if !ok {
		return nil, fmt.Errorf("builtin plugin %s is not registered", pluginID)
	}
	if !running {
		return nil, fmt.Errorf("plugin %s is not running", pluginID)
	}
	m.stopHealthChecks(pluginID)
	if err := registration.Disable(); err != nil {
		status.State, status.LastError = StateUnhealthy, err.Error()
		m.setStatus(status)
		return nil, fmt.Errorf("disable builtin plugin %s: %w", pluginID, err)
	}
	m.mu.Lock()
	m.builtinRunning[pluginID] = false
	m.mu.Unlock()
	status.State, status.LastError = StateDisabled, ""
	m.setStatus(status)
	return disabledFromBuiltin(registration), nil
}

func (m *Manager) startBuiltinHealthCheck(registration *BuiltinRegistration, interval time.Duration) {
	if registration.Health == nil {
		return
	}
	m.mu.Lock()
	if m.closed || len(m.stopMonitors[registration.PluginID]) > 0 {
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
				err := registration.Health(probeCtx)
				stop()
				nextUnhealthy := err != nil
				if nextUnhealthy == unhealthy {
					continue
				}
				status, ok := m.Status(registration.PluginID)
				if !ok || status.State == StateDisabled || status.State == StateStopped {
					continue
				}
				if err == nil {
					status.State, status.LastError = StateHealthy, ""
				} else {
					status.State, status.LastError = StateUnhealthy, err.Error()
				}
				m.setStatus(status)
				unhealthy = nextUnhealthy
			}
		}
	}()
	m.mu.Lock()
	m.stopMonitors[registration.PluginID] = append(m.stopMonitors[registration.PluginID], func() error {
		cancel()
		<-done
		return nil
	})
	m.mu.Unlock()
}

func (m *Manager) stopHealthChecks(pluginID string) {
	m.mu.Lock()
	monitors := append([]func() error(nil), m.stopMonitors[pluginID]...)
	delete(m.stopMonitors, pluginID)
	m.mu.Unlock()
	for i := len(monitors) - 1; i >= 0; i-- {
		_ = monitors[i]()
	}
}

func statusFromBuiltin(registration *BuiltinRegistration) Status {
	version := registration.Version
	if version == "" {
		version = "builtin"
	}
	return Status{
		PluginID:       registration.PluginID,
		Name:           registration.Name,
		Version:        version,
		ExtensionTypes: []string{registration.ExtensionType},
	}
}

func disabledFromBuiltin(registration *BuiltinRegistration) *DisabledExtensions {
	disabled := &DisabledExtensions{}
	switch registration.ExtensionType {
	case ExtensionDatasource:
		disabled.DatasourceTypes = []string{registration.ExtensionID}
	case ExtensionWebSearch:
		disabled.WebSearchTypes = []string{registration.ExtensionID}
	case ExtensionDocumentParser:
		disabled.DocumentParserTypes = []string{registration.ExtensionID}
	case ExtensionModelProvider:
		disabled.ModelProviderTypes = []string{registration.ExtensionID}
	case ExtensionRetrievalEngine:
		disabled.RetrievalEngineTypes = []string{registration.ExtensionID}
	}
	return disabled
}

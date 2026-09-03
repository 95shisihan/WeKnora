package plugin

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestManagerTracksDisabledPlugin(t *testing.T) {
	disabled := false
	manager := NewManager("0.7.2", nil)
	err := manager.Load(context.Background(), []*Manifest{
		{
			Metadata: Metadata{ID: "dev.example.disabled", Name: "Disabled", Version: "1.0.0"},
			Spec:     Spec{Enabled: &disabled, ExtensionPoints: []ExtensionPoint{{Type: "datasource"}}},
		},
	})
	require.NoError(t, err)

	statuses := manager.Statuses()
	require.Len(t, statuses, 1)
	require.Equal(t, StateDisabled, statuses[0].State)
	require.NoError(t, manager.Close())
}

func TestManagerMonitorsBuiltinHealth(t *testing.T) {
	manager := NewManager("0.7.2", nil)
	var unhealthy atomic.Bool
	require.NoError(t, manager.RegisterBuiltin(context.Background(), BuiltinRegistration{
		PluginID: "builtin.document_parser.health", Name: "Health",
		ExtensionType: ExtensionDocumentParser, ExtensionID: "health",
		Enable: func(context.Context) error { return nil }, Disable: func() error { return nil },
		Health: func(context.Context) error {
			if unhealthy.Load() {
				return errors.New("registry entry missing")
			}
			return nil
		},
	}))
	manager.StartHealthChecks(5 * time.Millisecond)
	unhealthy.Store(true)
	require.Eventually(t, func() bool {
		status, _ := manager.Status("builtin.document_parser.health")
		return status.State == StateUnhealthy
	}, time.Second, 10*time.Millisecond)
	unhealthy.Store(false)
	require.Eventually(t, func() bool {
		status, _ := manager.Status("builtin.document_parser.health")
		return status.State == StateHealthy
	}, time.Second, 10*time.Millisecond)
	require.NoError(t, manager.Close())
}

func TestManagerRunsBuiltinThroughUnifiedLifecycle(t *testing.T) {
	manager := NewManager("0.7.2", nil)
	enabled := 0
	disabled := 0
	require.NoError(t, manager.RegisterBuiltin(context.Background(), BuiltinRegistration{
		PluginID: "builtin.datasource.local", Name: "Local", Version: "0.7.2",
		ExtensionType: ExtensionDatasource, ExtensionID: "local",
		Enable:  func(context.Context) error { enabled++; return nil },
		Disable: func() error { disabled++; return nil },
	}))

	status, ok := manager.Status("builtin.datasource.local")
	require.True(t, ok)
	require.Equal(t, StateHealthy, status.State)
	require.Equal(t, 1, enabled)

	removed, err := manager.Disable("builtin.datasource.local")
	require.NoError(t, err)
	require.Equal(t, []string{"local"}, removed.DatasourceTypes)
	require.Equal(t, 1, disabled)
	status, _ = manager.Status("builtin.datasource.local")
	require.Equal(t, StateDisabled, status.State)

	_, err = manager.Enable(context.Background(), "builtin.datasource.local")
	require.NoError(t, err)
	require.Equal(t, 2, enabled)
	status, _ = manager.Status("builtin.datasource.local")
	require.Equal(t, StateHealthy, status.State)
	require.NoError(t, manager.Close())
}

func TestManagerBuiltinEnableFailureIsUnhealthy(t *testing.T) {
	manager := NewManager("0.7.2", nil)
	err := manager.RegisterBuiltin(context.Background(), BuiltinRegistration{
		PluginID: "builtin.web_search.broken", Name: "Broken",
		ExtensionType: ExtensionWebSearch, ExtensionID: "broken",
		Enable:  func(context.Context) error { return errors.New("not available") },
		Disable: func() error { return nil },
	})
	require.ErrorContains(t, err, "not available")
	status, ok := manager.Status("builtin.web_search.broken")
	require.True(t, ok)
	require.Equal(t, StateUnhealthy, status.State)
}

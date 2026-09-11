package datasource

import (
	"github.com/stretchr/testify/require"
	"testing"
)

type lifecycleConnector struct {
	Connector
	name string
}

func (c lifecycleConnector) Type() string { return c.name }

func TestBusyConnectorPreventsPartialUnregister(t *testing.T) {
	r := NewConnectorRegistry()
	require.NoError(t, r.Register(lifecycleConnector{name: "first"}))
	require.NoError(t, r.Register(lifecycleConnector{name: "second"}))
	_, release, err := r.Acquire("second")
	require.NoError(t, err)
	require.ErrorContains(t, r.UnregisterAll([]string{"first", "second"}), "busy")
	_, err = r.Get("first")
	require.NoError(t, err)
	release()
	release() // A duplicate release cannot permit a future active call to be lost.
	require.NoError(t, r.UnregisterAll([]string{"first", "second"}))
	_, releaseMissing, err := r.Acquire("second")
	require.ErrorIs(t, err, ErrConnectorNotFound)
	releaseMissing()
}

package plugin

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadDatasourcesRejectsUnenforcedNoNetwork(t *testing.T) {
	manifest := &Manifest{
		Metadata: Metadata{ID: "io.weknora.local", Name: "Local", Version: "0.1.0"},
		Spec: Spec{
			ExtensionPoints: []ExtensionPoint{{Type: "datasource", ID: "local", ProtocolVersion: "v1"}},
			Runtime:         Runtime{Type: "grpc", Address: "127.0.0.1:1", StartupTimeout: 1},
			Permissions:     Permissions{Network: NetworkPermission{Outbound: false}},
		},
	}
	_, err := LoadDatasources(context.Background(), []*Manifest{manifest})
	require.ErrorContains(t, err, "cannot enforce")
}

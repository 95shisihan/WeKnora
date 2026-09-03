package plugin

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
	"github.com/stretchr/testify/require"
)

type fakePluginContainerAPI struct {
	create  client.ContainerCreateOptions
	removed string
}

func (f *fakePluginContainerAPI) ContainerCreate(_ context.Context, options client.ContainerCreateOptions) (client.ContainerCreateResult, error) {
	f.create = options
	return client.ContainerCreateResult{ID: "container-id"}, nil
}
func (*fakePluginContainerAPI) ContainerStart(context.Context, string, client.ContainerStartOptions) (client.ContainerStartResult, error) {
	return client.ContainerStartResult{}, nil
}
func (f *fakePluginContainerAPI) ContainerRemove(_ context.Context, id string, _ client.ContainerRemoveOptions) (client.ContainerRemoveResult, error) {
	f.removed = id
	return client.ContainerRemoveResult{}, nil
}

type recordingSecuritySink struct{ events []SecurityEvent }

func (s *recordingSecuritySink) RecordSecurityEvent(_ context.Context, event SecurityEvent) {
	s.events = append(s.events, event)
}

func TestOCIRuntimeEnforcesNoNetworkAndReadOnlyMount(t *testing.T) {
	dir := t.TempDir()
	data := filepath.Join(dir, "data")
	require.NoError(t, os.Mkdir(data, 0o700))
	api := &fakePluginContainerAPI{}
	audit := &recordingSecuritySink{}
	runtime := &ociPluginRuntime{api: api, audit: audit}
	manifest := &Manifest{
		Metadata: Metadata{ID: "io.weknora.test", Version: "1.0.0"}, path: filepath.Join(dir, "plugin.yaml"),
		Spec: Spec{
			Runtime:     Runtime{Type: "oci", Image: "example/plugin:1", Mounts: []RuntimeMount{{Source: "data", Target: "/data", ReadOnly: true}}},
			Permissions: Permissions{Network: NetworkPermission{Outbound: false}},
		},
	}

	address, err := runtime.Start(context.Background(), manifest)
	require.NoError(t, err)
	require.Contains(t, address, "plugin.sock")
	require.Equal(t, container.NetworkMode("none"), api.create.HostConfig.NetworkMode)
	require.True(t, api.create.HostConfig.ReadonlyRootfs)
	require.Equal(t, []string{"ALL"}, api.create.HostConfig.CapDrop)
	require.Contains(t, api.create.HostConfig.SecurityOpt, "apparmor="+noNetworkAppArmor)
	require.Len(t, api.create.HostConfig.Mounts, 2)
	require.True(t, api.create.HostConfig.Mounts[1].ReadOnly)
	require.Equal(t, data, api.create.HostConfig.Mounts[1].Source)
	require.Len(t, audit.events, 1)
	require.Equal(t, "plugin.network_policy_applied", audit.events[0].Action)
	require.NoError(t, runtime.Close())
	require.Equal(t, "container-id", api.removed)
}

package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/client"
)

const (
	ociControlDir       = "/run/weknora-plugin"
	ociControlSocket    = ociControlDir + "/plugin.sock"
	defaultPluginMemory = 256 * 1024 * 1024
	defaultPluginCPU    = 1.0
	defaultPluginPIDs   = 128
	noNetworkAppArmor   = "weknora-plugin-no-network"
)

var safeContainerName = regexp.MustCompile(`[^a-zA-Z0-9_.-]+`)

type pluginContainerAPI interface {
	ContainerCreate(context.Context, client.ContainerCreateOptions) (client.ContainerCreateResult, error)
	ContainerStart(context.Context, string, client.ContainerStartOptions) (client.ContainerStartResult, error)
	ContainerRemove(context.Context, string, client.ContainerRemoveOptions) (client.ContainerRemoveResult, error)
}

type SecurityEvent struct {
	Time     time.Time      `json:"time"`
	PluginID string         `json:"plugin_id"`
	Action   string         `json:"action"`
	Outcome  string         `json:"outcome"`
	Details  map[string]any `json:"details,omitempty"`
}

type SecurityEventSink interface {
	RecordSecurityEvent(context.Context, SecurityEvent)
}

type jsonSecurityEventSink struct{}

func (jsonSecurityEventSink) RecordSecurityEvent(_ context.Context, event SecurityEvent) {
	raw, _ := json.Marshal(event)
	log.Printf("plugin-security-audit %s", raw)
}

type ociPluginRuntime struct {
	api          pluginContainerAPI
	clientCloser io.Closer
	audit        SecurityEventSink
	containerID  string
	controlDir   string
}

func newOCIPluginRuntime() (*ociPluginRuntime, error) {
	cli, err := client.New(client.FromEnv)
	if err != nil {
		return nil, fmt.Errorf("create Docker client: %w", err)
	}
	return &ociPluginRuntime{api: cli, clientCloser: cli, audit: jsonSecurityEventSink{}}, nil
}

func (r *ociPluginRuntime) Start(ctx context.Context, manifest *Manifest) (string, error) {
	controlDir, err := os.MkdirTemp("", "weknora-plugin-")
	if err != nil {
		return "", fmt.Errorf("create plugin control directory: %w", err)
	}
	if err := os.Chmod(controlDir, 0o700); err != nil {
		_ = os.RemoveAll(controlDir)
		return "", fmt.Errorf("secure plugin control directory: %w", err)
	}
	r.controlDir = controlDir

	mounts := []mount.Mount{{
		Type: mount.TypeBind, Source: controlDir, Target: ociControlDir, ReadOnly: false,
	}}
	for _, declared := range manifest.Spec.Runtime.Mounts {
		source := declared.Source
		if !filepath.IsAbs(source) {
			source = filepath.Join(manifest.Dir(), source)
		}
		source, err = filepath.Abs(source)
		if err != nil {
			return "", r.failStart(fmt.Errorf("resolve mount %q: %w", declared.Source, err))
		}
		if _, err := os.Stat(source); err != nil {
			return "", r.failStart(fmt.Errorf("mount source %q: %w", source, err))
		}
		mounts = append(mounts, mount.Mount{
			Type: mount.TypeBind, Source: source, Target: declared.Target, ReadOnly: true,
		})
	}

	memory, cpu, pids := effectivePluginResources(manifest.Spec.Runtime.Resources)
	networkMode := container.NetworkMode("bridge")
	securityOptions := []string{"no-new-privileges"}
	if !manifest.Spec.Permissions.Network.Outbound {
		networkMode = "none"
		// The AppArmor profile both denies AF_INET/AF_INET6 socket creation
		// and emits an audit record for every attempt. Docker network=none is
		// retained as a second, independent isolation boundary.
		securityOptions = append(securityOptions, "apparmor="+noNetworkAppArmor)
	}
	initProcess := true
	created, err := r.api.ContainerCreate(ctx, client.ContainerCreateOptions{
		Image: manifest.Spec.Runtime.Image,
		Name: "weknora-plugin-" + safeContainerName.ReplaceAllString(manifest.Metadata.ID, "-") + "-" +
			safeContainerName.ReplaceAllString(filepath.Base(controlDir), "-"),
		Config: &container.Config{
			Cmd: manifest.Spec.Runtime.Command,
			Env: []string{
				"WEKNORA_PLUGIN_ADDRESS=unix://" + ociControlSocket,
				"WEKNORA_PLUGIN_ID=" + manifest.Metadata.ID,
				"WEKNORA_PLUGIN_VERSION=" + manifest.Metadata.Version,
			},
			Labels: map[string]string{
				"io.weknora.managed": "true", "io.weknora.plugin.id": manifest.Metadata.ID,
				"io.weknora.plugin.version": manifest.Metadata.Version,
			},
		},
		HostConfig: &container.HostConfig{
			Mounts: mounts, NetworkMode: networkMode, ReadonlyRootfs: true,
			CapDrop: []string{"ALL"}, SecurityOpt: securityOptions,
			Tmpfs: map[string]string{"/tmp": "rw,noexec,nosuid,nodev,size=64m"}, Init: &initProcess,
			Resources: container.Resources{
				Memory: memory, MemorySwap: memory, NanoCPUs: int64(cpu * 1e9), PidsLimit: &pids,
			},
		},
	})
	if err != nil {
		r.recordPolicyEvent(ctx, manifest, networkMode, "failed", err.Error())
		return "", r.failStart(fmt.Errorf("create plugin container: %w", err))
	}
	r.containerID = created.ID
	if _, err := r.api.ContainerStart(ctx, created.ID, client.ContainerStartOptions{}); err != nil {
		r.recordPolicyEvent(ctx, manifest, networkMode, "failed", err.Error())
		return "", r.failStart(fmt.Errorf("start plugin container: %w", err))
	}
	r.recordPolicyEvent(ctx, manifest, networkMode, "success", "")
	// gRPC's unix resolver accepts a URI with a slash-normalized absolute path.
	hostSocket := filepath.ToSlash(filepath.Join(controlDir, "plugin.sock"))
	if !strings.HasPrefix(hostSocket, "/") {
		hostSocket = "/" + hostSocket
	}
	return "unix://" + hostSocket, nil
}

func (r *ociPluginRuntime) recordPolicyEvent(ctx context.Context, manifest *Manifest, networkMode container.NetworkMode, outcome, message string) {
	if r.audit == nil {
		return
	}
	details := map[string]any{
		"outbound": manifest.Spec.Permissions.Network.Outbound, "network_mode": string(networkMode),
		"attempt_audit": !manifest.Spec.Permissions.Network.Outbound, "audit_backend": noNetworkAppArmor,
	}
	if message != "" {
		details["error"] = message
	}
	r.audit.RecordSecurityEvent(ctx, SecurityEvent{
		Time: time.Now().UTC(), PluginID: manifest.Metadata.ID,
		Action: "plugin.network_policy_applied", Outcome: outcome, Details: details,
	})
}

func effectivePluginResources(resources RuntimeResources) (int64, float64, int64) {
	memory, cpu, pids := resources.MemoryBytes, resources.CPULimit, resources.PidsLimit
	if memory == 0 {
		memory = defaultPluginMemory
	}
	if cpu == 0 {
		cpu = defaultPluginCPU
	}
	if pids == 0 {
		pids = defaultPluginPIDs
	}
	return memory, cpu, pids
}

func (r *ociPluginRuntime) failStart(err error) error {
	_ = r.Close()
	return err
}

func (r *ociPluginRuntime) Close() error {
	var result error
	if r.containerID != "" {
		_, err := r.api.ContainerRemove(context.Background(), r.containerID, client.ContainerRemoveOptions{Force: true, RemoveVolumes: true})
		result = err
		r.containerID = ""
	}
	if r.controlDir != "" {
		result = errorsJoin(result, os.RemoveAll(r.controlDir))
		r.controlDir = ""
	}
	if r.clientCloser != nil {
		result = errorsJoin(result, r.clientCloser.Close())
		r.clientCloser = nil
	}
	return result
}

func errorsJoin(left, right error) error {
	if left == nil {
		return right
	}
	if right == nil {
		return left
	}
	return fmt.Errorf("%v; %w", left, right)
}

package plugin

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/Tencent/WeKnora/internal/plugin/httpbroker"
	"github.com/Tencent/WeKnora/plugin/sdk/transport"
	"google.golang.org/grpc"
)

type managedRuntime struct {
	address    string
	process    *exec.Cmd
	closer     io.Closer
	unregister func()
	stopHTTP   func()
}

func startManagedRuntime(ctx context.Context, manifest *Manifest) (*managedRuntime, error) {
	// Defense in depth for callers constructing manifests programmatically.
	var broker *httpbroker.Executor
	if p := manifest.Spec.Permissions.Network.HTTP; p != nil {
		if !httpApproved(manifest) {
			return nil, fmt.Errorf("HTTP permissions require administrator approval")
		}
		if manifest.Spec.Permissions.Network.Outbound {
			return nil, fmt.Errorf("controlled HTTP requires OS-enforced no networking")
		}
		var err error
		broker, err = httpbroker.New(*p)
		if err != nil {
			return nil, err
		}
	}
	r, err := startRuntime(ctx, manifest)
	if err != nil {
		return nil, err
	}
	if broker != nil {
		lifetime, stop := context.WithCancel(context.Background())
		broker.Audit = func(host, code string) {
			jsonSecurityEventSink{}.RecordSecurityEvent(context.Background(), SecurityEvent{Time: time.Now().UTC(), PluginID: manifest.Metadata.ID, Action: "plugin.http_request", Outcome: code, Details: map[string]any{"host": host}})
		}
		unregister := transport.RegisterHostService(r.address, func(startup context.Context, conn *grpc.ClientConn) error {
			channel, cancel := context.WithCancel(lifetime)
			// Startup cancellation must stop the handshake, not a healthy channel.
			stopStartup := context.AfterFunc(startup, cancel)
			err := broker.Open(channel, conn)
			if !stopStartup() || err != nil {
				cancel()
				if err == nil {
					err = startup.Err()
				}
			}
			return err
		})
		r.stopHTTP = func() { stop(); unregister() }
	}
	return r, nil
}

func startRuntime(ctx context.Context, manifest *Manifest) (*managedRuntime, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	instance := &managedRuntime{address: manifest.Spec.Runtime.Address}
	if manifest.Spec.Runtime.Type == "oci" {
		runtime, err := newOCIPluginRuntime()
		if err != nil {
			return nil, err
		}
		instance.closer = runtime
		instance.address, err = runtime.Start(ctx, manifest)
		if err != nil {
			_ = instance.Close()
			return nil, err
		}
		return instance, nil
	}
	if !manifest.Spec.Permissions.Network.Outbound {
		if manifest.Spec.Runtime.Address != "stdio://" || len(manifest.Spec.Runtime.Command) == 0 {
			return nil, fmt.Errorf("plugin %s: cannot enforce no networking on an unmanaged or TCP process; use runtime.address: stdio:// and runtime.command", manifest.Metadata.ID)
		}
		return startNativeNoNetwork(manifest)
	}
	if len(manifest.Spec.Runtime.Command) == 0 {
		if manifest.Spec.Runtime.Address == "stdio://" {
			return nil, fmt.Errorf("stdio transport requires runtime.command")
		}
		return instance, nil
	}
	commandPath, err := resolveCommand(manifest.Dir(), manifest.Spec.Runtime.Command[0])
	if err != nil {
		return nil, err
	}
	instance.process = exec.Command(commandPath, manifest.Spec.Runtime.Command[1:]...)
	instance.process.Dir = manifest.Dir()
	instance.process.Env = append(os.Environ(),
		"WEKNORA_PLUGIN_ADDRESS="+instance.address,
		"WEKNORA_PLUGIN_ID="+manifest.Metadata.ID,
		"WEKNORA_PLUGIN_VERSION="+manifest.Metadata.Version,
	)
	instance.process.Stdout, instance.process.Stderr = os.Stdout, os.Stderr
	if manifest.Spec.Runtime.Address == "stdio://" {
		instance.process.Stdout = nil
		reader, err := instance.process.StdoutPipe()
		if err != nil {
			return nil, err
		}
		writer, err := instance.process.StdinPipe()
		if err != nil {
			_ = reader.Close()
			return nil, err
		}
		instance.address = newStdioAddress()
		instance.unregister = transport.Register(instance.address, &transport.PipeConn{Reader: reader, Writer: writer}, nil)
	}
	if err := instance.process.Start(); err != nil {
		if instance.unregister != nil {
			instance.unregister()
		}
		return nil, fmt.Errorf("start process: %w", err)
	}
	return instance, nil
}

func (r *managedRuntime) Close() error {
	if r == nil {
		return nil
	}
	var result error
	if r.stopHTTP != nil {
		r.stopHTTP()
	}
	if r.process != nil && r.process.Process != nil {
		result = errors.Join(result, r.process.Process.Kill())
		_, _ = r.process.Process.Wait()
	}
	if r.closer != nil {
		result = errors.Join(result, r.closer.Close())
	}
	if r.unregister != nil {
		r.unregister()
	}
	return result
}

func newStdioAddress() string {
	var id [16]byte
	_, _ = rand.Read(id[:])
	return "passthrough:///weknora-stdio-" + hex.EncodeToString(id[:])
}

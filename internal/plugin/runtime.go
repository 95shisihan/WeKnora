package plugin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
)

type managedRuntime struct {
	address string
	process *exec.Cmd
	closer  io.Closer
}

func startManagedRuntime(ctx context.Context, manifest *Manifest) (*managedRuntime, error) {
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
	if len(manifest.Spec.Runtime.Command) == 0 {
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
	if err := instance.process.Start(); err != nil {
		return nil, fmt.Errorf("start process: %w", err)
	}
	return instance, nil
}

func (r *managedRuntime) Close() error {
	if r == nil {
		return nil
	}
	var result error
	if r.process != nil && r.process.Process != nil {
		result = errors.Join(result, r.process.Process.Kill())
		_, _ = r.process.Process.Wait()
	}
	if r.closer != nil {
		result = errors.Join(result, r.closer.Close())
	}
	return result
}

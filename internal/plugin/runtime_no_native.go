//go:build !windows || (!amd64 && !arm64)

package plugin

import "fmt"

func startNativeNoNetwork(manifest *Manifest) (*managedRuntime, error) {
	return nil, fmt.Errorf("plugin %s: cannot enforce native no-network processes on this platform", manifest.Metadata.ID)
}

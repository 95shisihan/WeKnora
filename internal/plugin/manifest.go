// Package plugin contains the host-side metadata and discovery primitives for
// out-of-process WeKnora extensions.
package plugin

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/blang/semver/v4"
	"gopkg.in/yaml.v3"
)

const (
	APIVersionV1Alpha1       = "plugins.weknora.io/v1alpha1"
	KindPlugin               = "Plugin"
	ProtocolVersionV1        = "v1"
	ExtensionDatasource      = "datasource"
	ExtensionDocumentParser  = "document_parser"
	ExtensionWebSearch       = "web_search"
	ExtensionModelProvider   = "model_provider"
	ExtensionRetrievalEngine = "retrieval_engine"
)

var knownExtensionTypes = map[string]struct{}{
	ExtensionDatasource: {}, ExtensionDocumentParser: {}, ExtensionWebSearch: {},
	ExtensionModelProvider: {}, ExtensionRetrievalEngine: {},
}

type Manifest struct {
	APIVersion string   `yaml:"apiVersion" json:"apiVersion"`
	Kind       string   `yaml:"kind" json:"kind"`
	Metadata   Metadata `yaml:"metadata" json:"metadata"`
	Spec       Spec     `yaml:"spec" json:"spec"`
	path       string
}

type Metadata struct {
	ID          string `yaml:"id" json:"id"`
	Name        string `yaml:"name" json:"name"`
	Version     string `yaml:"version" json:"version"`
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
}

type Spec struct {
	Enabled         *bool            `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	ExtensionPoints []ExtensionPoint `yaml:"extensionPoints" json:"extensionPoints"`
	Compatibility   Compatibility    `yaml:"compatibility" json:"compatibility"`
	Runtime         Runtime          `yaml:"runtime" json:"runtime"`
	ConfigSchema    map[string]any   `yaml:"configSchema,omitempty" json:"configSchema,omitempty"`
	Permissions     Permissions      `yaml:"permissions" json:"permissions"`
}

type ExtensionPoint struct {
	Type            string   `yaml:"type" json:"type"`
	ID              string   `yaml:"id" json:"id"`
	ProtocolVersion string   `yaml:"protocolVersion" json:"protocolVersion"`
	Name            string   `yaml:"name,omitempty" json:"name,omitempty"`
	Description     string   `yaml:"description,omitempty" json:"description,omitempty"`
	Icon            string   `yaml:"icon,omitempty" json:"icon,omitempty"`
	Priority        int      `yaml:"priority,omitempty" json:"priority,omitempty"`
	AuthType        string   `yaml:"authType,omitempty" json:"authType,omitempty"`
	Capabilities    []string `yaml:"capabilities,omitempty" json:"capabilities,omitempty"`
}

type Compatibility struct {
	WeKnora   string `yaml:"weknora" json:"weknora"`
	PluginAPI string `yaml:"pluginAPI" json:"pluginAPI"`
}

type Runtime struct {
	Type               string           `yaml:"type" json:"type"`
	Address            string           `yaml:"address,omitempty" json:"address,omitempty"`
	Image              string           `yaml:"image,omitempty" json:"image,omitempty"`
	Command            []string         `yaml:"command,omitempty" json:"command,omitempty"`
	Mounts             []RuntimeMount   `yaml:"mounts,omitempty" json:"mounts,omitempty"`
	Resources          RuntimeResources `yaml:"resources,omitempty" json:"resources,omitempty"`
	StartupTimeout     time.Duration    `yaml:"-" json:"-"`
	StartupTimeoutText string           `yaml:"startupTimeout,omitempty" json:"startupTimeout,omitempty"`
}

type RuntimeMount struct {
	Source   string `yaml:"source" json:"source"`
	Target   string `yaml:"target" json:"target"`
	ReadOnly bool   `yaml:"readOnly" json:"readOnly"`
}

type RuntimeResources struct {
	MemoryBytes int64   `yaml:"memoryBytes,omitempty" json:"memoryBytes,omitempty"`
	CPULimit    float64 `yaml:"cpuLimit,omitempty" json:"cpuLimit,omitempty"`
	PidsLimit   int64   `yaml:"pidsLimit,omitempty" json:"pidsLimit,omitempty"`
}

type Permissions struct {
	Network    NetworkPermission    `yaml:"network" json:"network"`
	Filesystem FilesystemPermission `yaml:"filesystem" json:"filesystem"`
}

type NetworkPermission struct {
	Outbound bool     `yaml:"outbound" json:"outbound"`
	Allow    []string `yaml:"allow,omitempty" json:"allow,omitempty"`
}

type FilesystemPermission struct {
	Read  []string `yaml:"read,omitempty" json:"read,omitempty"`
	Write []string `yaml:"write,omitempty" json:"write,omitempty"`
}

func LoadManifest(path string, weknoraVersion string) (*Manifest, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read plugin manifest: %w", err)
	}
	var manifest Manifest
	if err := yaml.Unmarshal(raw, &manifest); err != nil {
		return nil, fmt.Errorf("decode plugin manifest %s: %w", path, err)
	}
	manifest.path = path
	if err := manifest.Validate(weknoraVersion); err != nil {
		return nil, fmt.Errorf("plugin manifest %s: %w", path, err)
	}
	return &manifest, nil
}

func (m *Manifest) Validate(weknoraVersion string) error {
	if m.APIVersion != APIVersionV1Alpha1 || m.Kind != KindPlugin {
		return fmt.Errorf("unsupported apiVersion/kind %q/%q", m.APIVersion, m.Kind)
	}
	if strings.TrimSpace(m.Metadata.ID) == "" || strings.TrimSpace(m.Metadata.Name) == "" {
		return fmt.Errorf("metadata.id and metadata.name are required")
	}
	if _, err := semver.Parse(strings.TrimPrefix(m.Metadata.Version, "v")); err != nil {
		return fmt.Errorf("metadata.version must be semantic: %w", err)
	}
	if len(m.Spec.ExtensionPoints) == 0 {
		return fmt.Errorf("at least one extension point is required")
	}
	if len(m.Spec.ExtensionPoints) > 1 {
		return fmt.Errorf("v1alpha1 supports one extension point per plugin runtime")
	}
	seenPointIDs := make(map[string]struct{}, len(m.Spec.ExtensionPoints))
	datasourceCount := 0
	for _, point := range m.Spec.ExtensionPoints {
		if point.Type == "" || point.ID == "" {
			return fmt.Errorf("extension point type and id are required")
		}
		if point.ProtocolVersion != ProtocolVersionV1 {
			return fmt.Errorf("extension %s requests unsupported protocol %q", point.ID, point.ProtocolVersion)
		}
		if _, ok := knownExtensionTypes[point.Type]; !ok {
			return fmt.Errorf("extension %s has unknown type %q", point.ID, point.Type)
		}
		if _, exists := seenPointIDs[point.ID]; exists {
			return fmt.Errorf("duplicate extension point id %q", point.ID)
		}
		seenPointIDs[point.ID] = struct{}{}
		if point.Type == ExtensionDatasource {
			datasourceCount++
		}
	}
	if datasourceCount > 1 {
		return fmt.Errorf("v1alpha1 supports at most one datasource extension per plugin runtime")
	}
	switch m.Spec.Runtime.Type {
	case "grpc":
		if strings.TrimSpace(m.Spec.Runtime.Address) == "" {
			return fmt.Errorf("runtime.address is required for grpc")
		}
	case "oci":
		if strings.TrimSpace(m.Spec.Runtime.Image) == "" {
			return fmt.Errorf("runtime.image is required for oci")
		}
		for i, mount := range m.Spec.Runtime.Mounts {
			if strings.TrimSpace(mount.Source) == "" || !path.IsAbs(mount.Target) || mount.Target == "/" {
				return fmt.Errorf("runtime.mounts[%d] requires a source and an absolute non-root target", i)
			}
			if !mount.ReadOnly {
				return fmt.Errorf("runtime.mounts[%d] must be readOnly in v1alpha1", i)
			}
			if !containsString(m.Spec.Permissions.Filesystem.Read, mount.Source) {
				return fmt.Errorf("runtime.mounts[%d].source must be declared in permissions.filesystem.read", i)
			}
		}
	default:
		return fmt.Errorf("runtime.type must be grpc or oci")
	}
	if m.Spec.Runtime.Resources.MemoryBytes < 0 || m.Spec.Runtime.Resources.CPULimit < 0 || m.Spec.Runtime.Resources.PidsLimit < 0 {
		return fmt.Errorf("runtime resource limits cannot be negative")
	}
	if len(m.Spec.Permissions.Filesystem.Write) > 0 {
		return fmt.Errorf("filesystem write permissions are not supported in v1alpha1")
	}
	if len(m.Spec.Permissions.Network.Allow) > 0 {
		return fmt.Errorf("network allowlists are not enforceable in v1alpha1")
	}
	if m.Spec.Runtime.StartupTimeoutText == "" {
		m.Spec.Runtime.StartupTimeout = 10 * time.Second
	} else {
		d, err := time.ParseDuration(m.Spec.Runtime.StartupTimeoutText)
		if err != nil || d <= 0 {
			return fmt.Errorf("runtime.startupTimeout must be a positive duration")
		}
		m.Spec.Runtime.StartupTimeout = d
	}
	if m.Spec.Compatibility.PluginAPI != "" && m.Spec.Compatibility.PluginAPI != ProtocolVersionV1 {
		return fmt.Errorf("unsupported plugin API %q", m.Spec.Compatibility.PluginAPI)
	}
	if m.Spec.Compatibility.WeKnora != "" && weknoraVersion != "" {
		rangeFn, err := semver.ParseRange(m.Spec.Compatibility.WeKnora)
		if err != nil {
			return fmt.Errorf("invalid compatibility.weknora range: %w", err)
		}
		version, err := semver.Parse(strings.TrimPrefix(strings.TrimSpace(weknoraVersion), "v"))
		if err != nil {
			return fmt.Errorf("invalid host version %q: %w", weknoraVersion, err)
		}
		if !rangeFn(version) {
			return fmt.Errorf("host version %s is outside %s", version, m.Spec.Compatibility.WeKnora)
		}
	}
	return nil
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func (m *Manifest) Enabled() bool { return m.Spec.Enabled == nil || *m.Spec.Enabled }
func (m *Manifest) Dir() string   { return filepath.Dir(m.path) }

func Discover(dirs []string, weknoraVersion string) ([]*Manifest, error) {
	var manifests []*Manifest
	seen := make(map[string]string)
	for _, root := range dirs {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			return nil, fmt.Errorf("scan plugin directory %s: %w", root, err)
		}
		for _, entry := range entries {
			var path string
			if entry.IsDir() {
				path = filepath.Join(root, entry.Name(), "plugin.yaml")
			} else if entry.Name() == "plugin.yaml" {
				path = filepath.Join(root, entry.Name())
			} else {
				continue
			}
			if _, err := os.Stat(path); err != nil {
				if os.IsNotExist(err) {
					continue
				}
				return nil, err
			}
			manifest, err := LoadManifest(path, weknoraVersion)
			if err != nil {
				return nil, err
			}
			if previous, ok := seen[manifest.Metadata.ID]; ok {
				return nil, fmt.Errorf("duplicate plugin id %q in %s and %s", manifest.Metadata.ID, previous, path)
			}
			seen[manifest.Metadata.ID] = path
			manifests = append(manifests, manifest)
		}
	}
	return manifests, nil
}

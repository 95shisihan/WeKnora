package plugin

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	MaxPluginArchiveBytes   int64 = 64 << 20
	maxPluginExpandedBytes  int64 = 256 << 20
	maxPluginEntryBytes     int64 = 128 << 20
	maxPluginArchiveEntries       = 2048
)

var blockedPluginSourceExtensions = map[string]struct{}{
	".bat": {}, ".bash": {}, ".c": {}, ".cc": {}, ".cmd": {}, ".cpp": {},
	".cs": {}, ".css": {}, ".cxx": {}, ".fish": {}, ".go": {}, ".h": {},
	".hpp": {}, ".html": {}, ".java": {}, ".js": {}, ".jsx": {}, ".kt": {},
	".kts": {}, ".lua": {}, ".php": {}, ".proto": {}, ".ps1": {}, ".py": {},
	".pyw": {}, ".rb": {}, ".rs": {}, ".scala": {}, ".sh": {}, ".sql": {},
	".svelte": {}, ".swift": {}, ".ts": {}, ".tsx": {}, ".vbs": {}, ".vue": {},
	".zsh": {},
}

var blockedPluginBuildFiles = map[string]struct{}{
	".env": {}, "cargo.lock": {}, "cargo.toml": {}, "dockerfile": {},
	"go.mod": {}, "go.sum": {}, "gradlew": {}, "makefile": {},
	"package-lock.json": {}, "package.json": {}, "pnpm-lock.yaml": {},
	"poetry.lock": {}, "pom.xml": {}, "pyproject.toml": {},
	"requirements.txt": {}, "yarn.lock": {},
}

// DefaultInstallDir is the durable directory used for plugins installed from
// the admin API. Deployments may override it without changing discovery dirs.
func DefaultInstallDir() string {
	if configured := strings.TrimSpace(os.Getenv("WEKNORA_PLUGIN_INSTALL_DIR")); configured != "" {
		return configured
	}
	return filepath.Join("data", "plugins")
}

// unpackPluginArchive validates and extracts one plugin bundle into dest. A
// bundle may contain plugin.yaml at its root or inside one wrapping directory.
// Every file must belong to that same plugin root.
func unpackPluginArchive(archive []byte, dest string) error {
	if int64(len(archive)) > MaxPluginArchiveBytes {
		return fmt.Errorf("plugin bundle cannot exceed %d MB", MaxPluginArchiveBytes>>20)
	}
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return fmt.Errorf("plugin bundle is not a readable zip archive: %w", err)
	}
	if len(reader.File) == 0 {
		return fmt.Errorf("plugin bundle is empty")
	}
	if len(reader.File) > maxPluginArchiveEntries {
		return fmt.Errorf("plugin bundle holds more than %d entries", maxPluginArchiveEntries)
	}

	prefix, err := pluginArchivePrefix(reader.File)
	if err != nil {
		return err
	}
	var expanded int64
	for _, entry := range reader.File {
		name, err := cleanPluginArchiveName(entry.Name)
		if err != nil {
			return err
		}
		if prefix != "" {
			if name == strings.TrimSuffix(prefix, "/") && entry.FileInfo().IsDir() {
				continue
			}
			if !strings.HasPrefix(name, prefix) {
				return fmt.Errorf("entry %q is outside the plugin directory", entry.Name)
			}
			name = strings.TrimPrefix(name, prefix)
		}
		if name == "" {
			continue
		}

		info := entry.FileInfo()
		if info.Mode()&os.ModeSymlink != 0 || (!info.IsDir() && !info.Mode().IsRegular()) {
			return fmt.Errorf("entry %q has an unsupported file type", entry.Name)
		}
		target := filepath.Join(dest, filepath.FromSlash(name))
		rel, err := filepath.Rel(dest, target)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("entry %q escapes the plugin directory", entry.Name)
		}
		if info.IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("create plugin directory: %w", err)
			}
			continue
		}
		if strings.EqualFold(filepath.Base(target), httpApprovalFile) {
			return fmt.Errorf("plugin archive cannot supply host HTTP approval")
		}
		if info.Size() > maxPluginEntryBytes {
			return fmt.Errorf("entry %q is too large", entry.Name)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("create plugin directory: %w", err)
		}
		rc, err := entry.Open()
		if err != nil {
			return fmt.Errorf("open plugin entry %q: %w", entry.Name, err)
		}
		mode := info.Mode().Perm()
		if mode == 0 {
			mode = 0o644
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
		if err != nil {
			_ = rc.Close()
			return fmt.Errorf("create plugin entry %q: %w", entry.Name, err)
		}
		written, copyErr := io.Copy(out, io.LimitReader(rc, maxPluginEntryBytes+1))
		closeErr := out.Close()
		_ = rc.Close()
		if copyErr != nil {
			return fmt.Errorf("extract plugin entry %q: %w", entry.Name, copyErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close plugin entry %q: %w", entry.Name, closeErr)
		}
		if written > maxPluginEntryBytes {
			return fmt.Errorf("entry %q is too large", entry.Name)
		}
		expanded += written
		if expanded > maxPluginExpandedBytes {
			return fmt.Errorf("expanded plugin bundle cannot exceed %d MB", maxPluginExpandedBytes>>20)
		}
	}
	return nil
}

// validateCompiledPluginArtifact ensures an uploaded package is a deployable
// artifact, not a source tree that the host would interpret or compile. OCI
// packages point at an already-built image; grpc packages must carry a native
// executable matching the host operating system.
func validateCompiledPluginArtifact(manifest *Manifest) error {
	root := manifest.Dir()
	err := filepath.WalkDir(root, func(filePath string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		name := strings.ToLower(entry.Name())
		if _, blocked := blockedPluginBuildFiles[name]; blocked {
			return fmt.Errorf("source or build file %q is not allowed; upload compiled artifacts only", entry.Name())
		}
		if _, blocked := blockedPluginSourceExtensions[strings.ToLower(filepath.Ext(name))]; blocked {
			rel, _ := filepath.Rel(root, filePath)
			return fmt.Errorf("source file %q is not allowed; upload compiled artifacts only", filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		return err
	}
	if manifest.Spec.Runtime.Type != "grpc" {
		return nil
	}
	if len(manifest.Spec.Runtime.Command) == 0 || strings.TrimSpace(manifest.Spec.Runtime.Command[0]) == "" {
		return fmt.Errorf("uploaded grpc plugins must include a compiled runtime.command executable")
	}
	executable, err := resolveCommand(root, manifest.Spec.Runtime.Command[0])
	if err != nil {
		return err
	}
	binary, err := os.Open(executable)
	if err != nil {
		return fmt.Errorf("open runtime.command: %w", err)
	}
	defer func() { _ = binary.Close() }()
	header := make([]byte, 4)
	n, err := io.ReadFull(binary, header)
	if err != nil || n < 4 {
		return fmt.Errorf("runtime.command is not a recognized compiled executable")
	}
	if !compiledBinaryMatchesHost(header, runtime.GOOS) {
		return fmt.Errorf("runtime.command is not a compiled executable for %s", runtime.GOOS)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(executable)
		if err != nil || info.Mode().Perm()&0o111 == 0 {
			return fmt.Errorf("runtime.command must have an executable permission bit")
		}
	}
	return nil
}

func compiledBinaryMatchesHost(header []byte, goos string) bool {
	if len(header) < 4 {
		return false
	}
	switch goos {
	case "windows":
		return header[0] == 'M' && header[1] == 'Z'
	case "linux", "freebsd", "netbsd", "openbsd":
		return header[0] == 0x7f && header[1] == 'E' && header[2] == 'L' && header[3] == 'F'
	case "darwin":
		magic := uint32(header[0])<<24 | uint32(header[1])<<16 | uint32(header[2])<<8 | uint32(header[3])
		return magic == 0xfeedface || magic == 0xfeedfacf || magic == 0xcefaedfe || magic == 0xcffaedfe ||
			magic == 0xcafebabe || magic == 0xbebafeca
	default:
		return false
	}
}

func pluginArchivePrefix(entries []*zip.File) (string, error) {
	prefix := ""
	found := 0
	for _, entry := range entries {
		name, err := cleanPluginArchiveName(entry.Name)
		if err != nil {
			return "", err
		}
		if name == "plugin.yaml" {
			prefix = ""
			found++
			continue
		}
		if path.Base(name) != "plugin.yaml" {
			continue
		}
		dir := path.Dir(name)
		if dir == "." || strings.Contains(dir, "/") {
			continue
		}
		prefix = dir + "/"
		found++
	}
	if found == 0 {
		return "", fmt.Errorf("plugin.yaml is missing from the bundle root")
	}
	if found > 1 {
		return "", fmt.Errorf("plugin bundle contains more than one plugin.yaml")
	}
	return prefix, nil
}

func cleanPluginArchiveName(raw string) (string, error) {
	// ZIP names use forward slashes, but treating backslashes as separators is
	// required on Windows to catch entries such as ..\\payload.exe.
	name := path.Clean(strings.ReplaceAll(raw, "\\", "/"))
	if name == "." || name == ".." || path.IsAbs(name) || strings.HasPrefix(name, "../") {
		return "", fmt.Errorf("entry %q escapes the archive root", raw)
	}
	if strings.Contains(strings.SplitN(name, "/", 2)[0], ":") {
		return "", fmt.Errorf("entry %q uses an invalid drive path", raw)
	}
	return name, nil
}

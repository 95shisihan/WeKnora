package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	pluginproto "example.org/weknora-local-directory/proto"
	"example.org/weknora-local-directory/sdk/transport"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

const connectorType = "local_directory"

type server struct {
	pluginproto.UnimplementedDatasourcePluginServer
	pluginID string
	version  string
}

type dataSourceConfig struct {
	ResourceIDs []string       `json:"resource_ids"`
	Settings    map[string]any `json:"settings"`
}

type syncCursor struct {
	LastSyncTime    time.Time      `json:"last_sync_time"`
	ConnectorCursor cursorContents `json:"connector_cursor"`
}

type cursorContents struct {
	Files map[string]string `json:"files"`
}

type fileSnapshot struct {
	RelativePath string
	AbsolutePath string
	Hash         string
	Info         fs.FileInfo
}

func main() {
	address := strings.TrimSpace(os.Getenv("WEKNORA_PLUGIN_ADDRESS"))
	if address == "" {
		address = "127.0.0.1:50101"
	}
	listener, cleanup, err := listen(address)
	if err != nil {
		panic(err)
	}
	defer cleanup()
	grpcServer := grpc.NewServer()
	pluginproto.RegisterDatasourcePluginServer(grpcServer, &server{
		pluginID: envOr("WEKNORA_PLUGIN_ID", "io.weknora.local-directory"),
		version:  envOr("WEKNORA_PLUGIN_VERSION", "0.1.0"),
	})
	healthServer := health.NewServer()
	healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)
	grpc_health_v1.RegisterHealthServer(grpcServer, healthServer)
	if err := grpcServer.Serve(listener); err != nil {
		panic(err)
	}
}

func listen(address string) (net.Listener, func(), error) {
	if address == "stdio://" {
		listener := transport.StdioListener()
		return listener, func() { _ = listener.Close() }, nil
	}
	if strings.HasPrefix(address, "unix://") {
		socketPath := strings.TrimPrefix(address, "unix://")
		if socketPath == "" {
			return nil, func() {}, fmt.Errorf("empty unix socket path")
		}
		if err := os.Remove(socketPath); err != nil && !os.IsNotExist(err) {
			return nil, func() {}, err
		}
		listener, err := net.Listen("unix", socketPath)
		if err != nil {
			return nil, func() {}, err
		}
		// The host process and container root can have different numeric UIDs.
		// The parent control directory is mode 0700 and randomly named, so the
		// socket can be connectable by the host without exposing it globally.
		if err := os.Chmod(socketPath, 0o666); err != nil {
			_ = listener.Close()
			return nil, func() {}, err
		}
		return listener, func() { _ = os.Remove(socketPath) }, nil
	}
	listener, err := net.Listen("tcp", address)
	return listener, func() {}, err
}

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func (s *server) GetInfo(context.Context, *pluginproto.GetInfoRequest) (*pluginproto.GetInfoResponse, error) {
	return &pluginproto.GetInfoResponse{Id: s.pluginID, Version: s.version, ProtocolVersion: "v1", ConnectorType: connectorType}, nil
}

func (*server) Validate(_ context.Context, request *pluginproto.ConfigRequest) (*pluginproto.Empty, error) {
	config, root, err := parseConfig(request.GetConfigJson())
	_ = config
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if _, err := os.ReadDir(root); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "read root directory: %v", err)
	}
	return &pluginproto.Empty{}, nil
}

func (*server) ListResources(_ context.Context, request *pluginproto.ListResourcesRequest) (*pluginproto.ListResourcesResponse, error) {
	_, root, err := parseConfig(request.GetConfigJson())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	parent, err := secureJoin(root, request.GetParentId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	entries, err := os.ReadDir(parent)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list directory: %v", err)
	}
	resources := make([]*pluginproto.Resource, 0, len(entries))
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		rel := filepath.ToSlash(filepath.Join(request.GetParentId(), entry.Name()))
		if !entry.IsDir() && !supported(rel) {
			continue
		}
		kind := "file"
		if entry.IsDir() {
			kind = "directory"
		}
		resources = append(resources, &pluginproto.Resource{
			ExternalId: rel, Name: entry.Name(), Type: kind, ParentId: filepath.ToSlash(request.GetParentId()),
			HasChildren: entry.IsDir(), ModifiedAtUnixMilli: info.ModTime().UnixMilli(),
		})
	}
	return &pluginproto.ListResourcesResponse{Resources: resources}, nil
}

func (*server) ResolveResourceAncestors(_ context.Context, request *pluginproto.ResolveResourceAncestorsRequest) (*pluginproto.ResolveResourceAncestorsResponse, error) {
	response := &pluginproto.ResolveResourceAncestorsResponse{}
	for _, resourceID := range request.GetResourceIds() {
		clean := filepath.ToSlash(filepath.Clean(resourceID))
		if clean == "." || strings.HasPrefix(clean, "../") {
			continue
		}
		var ancestors []string
		for parent := filepath.ToSlash(filepath.Dir(clean)); parent != "." && parent != "/"; parent = filepath.ToSlash(filepath.Dir(parent)) {
			ancestors = append(ancestors, parent)
		}
		response.Ancestors = append(response.Ancestors, &pluginproto.Ancestors{ResourceId: resourceID, ExternalIds: ancestors})
	}
	return response, nil
}

func (*server) Fetch(request *pluginproto.FetchRequest, stream grpc.ServerStreamingServer[pluginproto.FetchEvent]) error {
	config, root, err := parseConfig(request.GetConfigJson())
	if err != nil {
		return status.Error(codes.InvalidArgument, err.Error())
	}
	selected := request.GetResourceIds()
	if len(selected) == 0 {
		selected = config.ResourceIDs
	}
	previous, err := parseCursor(request.GetCursorJson())
	if err != nil {
		return status.Error(codes.InvalidArgument, err.Error())
	}
	if request.GetFull() {
		previous.Files = map[string]string{}
	}
	snapshots, err := scan(root, selected)
	if err != nil {
		return status.Errorf(codes.Internal, "scan root: %v", err)
	}
	next := cursorContents{Files: make(map[string]string, len(snapshots))}
	// A checkpoint is a recoverable history, not a partial current snapshot.
	// Keep old hashes for files not yet sent and retain deletion candidates
	// until the final cursor commits the complete scan and deletion pass.
	checkpoint := cursorContents{Files: make(map[string]string, len(previous.Files))}
	for path, hash := range previous.Files {
		checkpoint.Files[path] = hash
	}
	for index, snapshot := range snapshots {
		next.Files[snapshot.RelativePath] = snapshot.Hash
		if previous.Files[snapshot.RelativePath] == snapshot.Hash {
			continue
		}
		content, err := os.ReadFile(snapshot.AbsolutePath)
		if err != nil {
			return status.Errorf(codes.Internal, "read %s: %v", snapshot.RelativePath, err)
		}
		if err := stream.Send(&pluginproto.FetchEvent{Payload: &pluginproto.FetchEvent_Item{Item: &pluginproto.FetchedItem{
			ExternalId: snapshot.RelativePath, Title: filepath.Base(snapshot.RelativePath), Content: content,
			ContentType: contentType(snapshot.RelativePath), FileName: filepath.Base(snapshot.RelativePath),
			UpdatedAtUnixMilli: snapshot.Info.ModTime().UnixMilli(), SourceResourceId: snapshot.RelativePath,
			Metadata: map[string]string{"channel": connectorType, "relative_path": snapshot.RelativePath, "sha256": snapshot.Hash},
		}}}); err != nil {
			return err
		}
		checkpoint.Files[snapshot.RelativePath] = snapshot.Hash
		if (index+1)%100 == 0 {
			raw, _ := encodeCursor(checkpoint)
			if err := stream.Send(&pluginproto.FetchEvent{Payload: &pluginproto.FetchEvent_CheckpointCursorJson{CheckpointCursorJson: raw}}); err != nil {
				return err
			}
		}
	}
	for path := range previous.Files {
		if _, exists := next.Files[path]; exists || !selectedPath(path, selected) {
			continue
		}
		if err := stream.Send(&pluginproto.FetchEvent{Payload: &pluginproto.FetchEvent_Item{Item: &pluginproto.FetchedItem{
			ExternalId: path, Title: filepath.Base(path), FileName: filepath.Base(path), IsDeleted: true,
			SourceResourceId: path, Metadata: map[string]string{"channel": connectorType, "relative_path": path},
		}}}); err != nil {
			return err
		}
	}
	finalRaw, _ := encodeCursor(next)
	return stream.Send(&pluginproto.FetchEvent{Payload: &pluginproto.FetchEvent_FinalCursorJson{FinalCursorJson: finalRaw}})
}

func parseConfig(raw []byte) (dataSourceConfig, string, error) {
	var config dataSourceConfig
	if err := json.Unmarshal(raw, &config); err != nil {
		return config, "", fmt.Errorf("decode config: %w", err)
	}
	rootValue, ok := config.Settings["root"].(string)
	if !ok || strings.TrimSpace(rootValue) == "" {
		return config, "", errors.New("settings.root is required")
	}
	root, err := filepath.Abs(rootValue)
	if err != nil {
		return config, "", fmt.Errorf("resolve root: %w", err)
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return config, "", errors.New("settings.root must be an existing directory")
	}
	return config, root, nil
}

func secureJoin(root, relative string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(relative))
	if clean == "." {
		return root, nil
	}
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", errors.New("resource path escapes root")
	}
	joined := filepath.Join(root, clean)
	rel, err := filepath.Rel(root, joined)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("resource path escapes root")
	}
	return joined, nil
}

func parseCursor(raw []byte) (cursorContents, error) {
	result := cursorContents{Files: map[string]string{}}
	if len(raw) == 0 {
		return result, nil
	}
	var cursor syncCursor
	if err := json.Unmarshal(raw, &cursor); err != nil {
		return result, fmt.Errorf("decode cursor: %w", err)
	}
	if cursor.ConnectorCursor.Files != nil {
		result.Files = cursor.ConnectorCursor.Files
	}
	return result, nil
}

func encodeCursor(contents cursorContents) ([]byte, error) {
	return json.Marshal(syncCursor{LastSyncTime: time.Now().UTC(), ConnectorCursor: contents})
}

func scan(root string, selected []string) ([]fileSnapshot, error) {
	var snapshots []fileSnapshot
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if !supported(rel) || !selectedPath(rel, selected) {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(content)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		snapshots = append(snapshots, fileSnapshot{RelativePath: rel, AbsolutePath: path, Hash: hex.EncodeToString(sum[:]), Info: info})
		return nil
	})
	sort.Slice(snapshots, func(i, j int) bool { return snapshots[i].RelativePath < snapshots[j].RelativePath })
	return snapshots, err
}

func selectedPath(path string, selected []string) bool {
	if len(selected) == 0 {
		return true
	}
	path = filepath.ToSlash(filepath.Clean(path))
	for _, value := range selected {
		value = strings.TrimSuffix(filepath.ToSlash(filepath.Clean(value)), "/")
		if value == "." || path == value || strings.HasPrefix(path, value+"/") {
			return true
		}
	}
	return false
}

var extensions = map[string]string{
	".txt": "text/plain", ".md": "text/markdown", ".markdown": "text/markdown",
	".html": "text/html", ".htm": "text/html", ".pdf": "application/pdf",
	".doc": "application/msword", ".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	".xls": "application/vnd.ms-excel", ".xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	".ppt": "application/vnd.ms-powerpoint", ".pptx": "application/vnd.openxmlformats-officedocument.presentationml.presentation",
}

func supported(path string) bool     { _, ok := extensions[strings.ToLower(filepath.Ext(path))]; return ok }
func contentType(path string) string { return extensions[strings.ToLower(filepath.Ext(path))] }

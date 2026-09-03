package documentparsergrpc

import (
	"context"
	"fmt"
	"io"
	"sync"

	"github.com/Tencent/WeKnora/internal/infrastructure/docparser"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	pluginproto "github.com/Tencent/WeKnora/plugin/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"
)

const maxDocumentMessageSize = 128 * 1024 * 1024

type Connector struct {
	pluginID, version, engineName string
	description                   string
	fileTypes                     []string
	mu                            sync.RWMutex
	conn                          *grpc.ClientConn
	client                        pluginproto.DocumentParserPluginClient
}

func Dial(ctx context.Context, address, pluginID, version, engineName string) (*Connector, error) {
	conn, err := grpc.NewClient(address,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallSendMsgSize(maxDocumentMessageSize),
			grpc.MaxCallRecvMsgSize(maxDocumentMessageSize),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("dial document parser plugin %s: %w", pluginID, err)
	}
	connector := &Connector{pluginID: pluginID, version: version, engineName: engineName, conn: conn, client: pluginproto.NewDocumentParserPluginClient(conn)}
	if err := connector.Health(ctx); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return connector, nil
}

func (c *Connector) Health(ctx context.Context) error {
	if _, err := grpc_health_v1.NewHealthClient(c.conn).Check(ctx, &grpc_health_v1.HealthCheckRequest{}); err != nil {
		return fmt.Errorf("plugin %s health check: %w", c.pluginID, err)
	}
	info, err := c.client.GetInfo(ctx, &pluginproto.DocumentParserInfoRequest{})
	if err != nil {
		return fmt.Errorf("plugin %s info: %w", c.pluginID, err)
	}
	if info.GetId() != c.pluginID || info.GetVersion() != c.version || info.GetProtocolVersion() != "v1" || info.GetEngineName() != c.engineName {
		return fmt.Errorf("document parser plugin identity mismatch")
	}
	c.mu.Lock()
	c.description = info.GetDescription()
	c.fileTypes = append(c.fileTypes[:0], info.GetFileTypes()...)
	c.mu.Unlock()
	return nil
}

func (c *Connector) Close() error { return c.conn.Close() }

func (c *Connector) Name() string { return c.engineName }
func (c *Connector) Description() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.description
}
func (c *Connector) FileTypes(bool) []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return append([]string(nil), c.fileTypes...)
}
func (c *Connector) CheckAvailable(bool, map[string]string) (bool, string) { return true, "" }
func (c *Connector) NewReader(context.Context, docparser.ReaderDeps) (interfaces.DocReader, error) {
	return &reader{client: c.client}, nil
}

type reader struct {
	client pluginproto.DocumentParserPluginClient
}

func (r *reader) Read(ctx context.Context, request *types.ReadRequest) (*types.ReadResult, error) {
	stream, err := r.client.Parse(ctx, &pluginproto.DocumentParseRequest{
		FileContent: request.FileContent, FileName: request.FileName, FileType: request.FileType,
		Url: request.URL, Title: request.Title, RequestId: request.RequestID,
		ConfigOverrides: request.ParserEngineOverrides,
	})
	if err != nil {
		return nil, fmt.Errorf("document parser plugin parse: %w", err)
	}
	result := &types.ReadResult{}
	gotMeta := false
	for {
		frame, recvErr := stream.Recv()
		if recvErr == io.EOF {
			break
		}
		if recvErr != nil {
			return nil, fmt.Errorf("document parser plugin stream: %w", recvErr)
		}
		if meta := frame.GetMeta(); meta != nil {
			if gotMeta {
				return nil, fmt.Errorf("document parser plugin returned multiple metadata frames")
			}
			gotMeta = true
			result.MarkdownContent = meta.GetMarkdownContent()
			result.ImageDirPath = meta.GetImageDirPath()
			result.Metadata = meta.GetMetadata()
			result.Error = meta.GetError()
			result.IsAudio = meta.GetIsAudio()
			continue
		}
		if image := frame.GetImage(); image != nil {
			result.ImageRefs = append(result.ImageRefs, types.ImageRef{
				Filename: image.GetFilename(), OriginalRef: image.GetOriginalRef(), MimeType: image.GetMimeType(),
				StorageKey: image.GetStorageKey(), ImageData: image.GetImageData(), IsOriginal: image.GetIsOriginal(),
			})
			continue
		}
		if audio := frame.GetAudio(); audio != nil {
			result.AudioData = append(result.AudioData, audio.GetData()...)
		}
	}
	if !gotMeta {
		return nil, fmt.Errorf("document parser plugin returned no metadata frame")
	}
	return result, nil
}

var _ docparser.EngineRegistration = (*Connector)(nil)
var _ interfaces.DocReader = (*reader)(nil)

package datasourcegrpc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
	pluginproto "github.com/Tencent/WeKnora/plugin/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"
)

type Connector struct {
	connectorType string
	pluginID      string
	version       string
	conn          *grpc.ClientConn
	client        pluginproto.DatasourcePluginClient
}

func Dial(ctx context.Context, address, pluginID, version, connectorType string) (*Connector, error) {
	conn, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("dial datasource plugin %s: %w", pluginID, err)
	}
	connector := &Connector{
		connectorType: connectorType,
		pluginID:      pluginID,
		version:       version,
		conn:          conn,
		client:        pluginproto.NewDatasourcePluginClient(conn),
	}
	if err := connector.probe(ctx); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return connector, nil
}

func (c *Connector) probe(ctx context.Context) error {
	if _, err := grpc_health_v1.NewHealthClient(c.conn).Check(ctx, &grpc_health_v1.HealthCheckRequest{}); err != nil {
		return fmt.Errorf("plugin %s health check: %w", c.pluginID, err)
	}
	info, err := c.client.GetInfo(ctx, &pluginproto.GetInfoRequest{})
	if err != nil {
		return fmt.Errorf("plugin %s info: %w", c.pluginID, err)
	}
	if info.GetId() != c.pluginID || info.GetVersion() != c.version ||
		info.GetProtocolVersion() != "v1" || info.GetConnectorType() != c.connectorType {
		return fmt.Errorf("plugin identity mismatch: manifest=%s/%s/%s runtime=%s/%s/%s/%s",
			c.pluginID, c.version, c.connectorType, info.GetId(), info.GetVersion(),
			info.GetProtocolVersion(), info.GetConnectorType())
	}
	return nil
}

func (c *Connector) Close() error { return c.conn.Close() }
func (c *Connector) Type() string { return c.connectorType }

// Health verifies both transport health and runtime identity. Re-checking the
// identity catches a container/process replacement at the same endpoint.
func (c *Connector) Health(ctx context.Context) error { return c.probe(ctx) }

func configJSON(config *types.DataSourceConfig) ([]byte, error) {
	raw, err := json.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("marshal datasource plugin config: %w", err)
	}
	return raw, nil
}

func (c *Connector) Validate(ctx context.Context, config *types.DataSourceConfig) error {
	raw, err := configJSON(config)
	if err != nil {
		return err
	}
	_, err = c.client.Validate(ctx, &pluginproto.ConfigRequest{ConfigJson: raw})
	return err
}

func (c *Connector) ListResources(ctx context.Context, config *types.DataSourceConfig, parentID string) ([]types.Resource, error) {
	raw, err := configJSON(config)
	if err != nil {
		return nil, err
	}
	response, err := c.client.ListResources(ctx, &pluginproto.ListResourcesRequest{ConfigJson: raw, ParentId: parentID})
	if err != nil {
		return nil, err
	}
	resources := make([]types.Resource, 0, len(response.GetResources()))
	for _, resource := range response.GetResources() {
		var metadata map[string]interface{}
		if len(resource.GetMetadataJson()) > 0 {
			if err := json.Unmarshal(resource.GetMetadataJson(), &metadata); err != nil {
				return nil, fmt.Errorf("decode plugin resource metadata: %w", err)
			}
		}
		resources = append(resources, types.Resource{
			ExternalID: resource.GetExternalId(), Name: resource.GetName(), Type: resource.GetType(),
			Description: resource.GetDescription(), URL: resource.GetUrl(), ParentID: resource.GetParentId(),
			HasChildren: resource.GetHasChildren(), Metadata: metadata,
			ModifiedAt: time.UnixMilli(resource.GetModifiedAtUnixMilli()).UTC(),
		})
	}
	return resources, nil
}

func (c *Connector) ResolveResourceAncestors(ctx context.Context, config *types.DataSourceConfig, resourceIDs []string) ([]string, error) {
	raw, err := configJSON(config)
	if err != nil {
		return nil, err
	}
	response, err := c.client.ResolveResourceAncestors(ctx, &pluginproto.ResolveResourceAncestorsRequest{
		ConfigJson: raw, ResourceIds: resourceIDs,
	})
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{})
	var result []string
	for _, group := range response.GetAncestors() {
		for _, id := range group.GetExternalIds() {
			if _, ok := seen[id]; !ok {
				seen[id] = struct{}{}
				result = append(result, id)
			}
		}
	}
	return result, nil
}

func (c *Connector) FetchAll(ctx context.Context, config *types.DataSourceConfig, resourceIDs []string) ([]types.FetchedItem, error) {
	return c.collect(ctx, config, resourceIDs, nil, true)
}

func (c *Connector) FetchIncremental(ctx context.Context, config *types.DataSourceConfig, cursor *types.SyncCursor) ([]types.FetchedItem, *types.SyncCursor, error) {
	items, next, err := c.collectWithCursor(ctx, config, config.ResourceIDs, cursor, false)
	return items, next, err
}

func (c *Connector) collect(ctx context.Context, config *types.DataSourceConfig, resourceIDs []string, cursor *types.SyncCursor, full bool) ([]types.FetchedItem, error) {
	items, _, err := c.collectWithCursor(ctx, config, resourceIDs, cursor, full)
	return items, err
}

func (c *Connector) collectWithCursor(ctx context.Context, config *types.DataSourceConfig, resourceIDs []string, cursor *types.SyncCursor, full bool) ([]types.FetchedItem, *types.SyncCursor, error) {
	collector := &collectHandler{}
	next, err := c.fetch(ctx, config, resourceIDs, cursor, full, collector)
	return collector.items, next, err
}

type collectHandler struct{ items []types.FetchedItem }

func (h *collectHandler) Emit(_ context.Context, item types.FetchedItem) error {
	h.items = append(h.items, item)
	return nil
}
func (*collectHandler) Checkpoint(context.Context, *types.SyncCursor) error { return nil }

func (c *Connector) FetchStream(ctx context.Context, config *types.DataSourceConfig, cursor *types.SyncCursor, h datasource.StreamHandler) (*types.SyncCursor, error) {
	return c.fetch(ctx, config, config.ResourceIDs, cursor, cursor == nil, h)
}

func (c *Connector) fetch(ctx context.Context, config *types.DataSourceConfig, resourceIDs []string, cursor *types.SyncCursor, full bool, h datasource.StreamHandler) (*types.SyncCursor, error) {
	configRaw, err := configJSON(config)
	if err != nil {
		return nil, err
	}
	var cursorRaw []byte
	if cursor != nil {
		cursorRaw, err = json.Marshal(cursor)
		if err != nil {
			return nil, fmt.Errorf("marshal plugin cursor: %w", err)
		}
	}
	stream, err := c.client.Fetch(ctx, &pluginproto.FetchRequest{
		ConfigJson: configRaw, ResourceIds: resourceIDs, CursorJson: cursorRaw, Full: full,
	})
	if err != nil {
		return nil, err
	}
	var final *types.SyncCursor
	for {
		event, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return final, err
		}
		switch payload := event.GetPayload().(type) {
		case *pluginproto.FetchEvent_Item:
			if err := h.Emit(ctx, fromProtoItem(payload.Item)); err != nil {
				return final, err
			}
		case *pluginproto.FetchEvent_CheckpointCursorJson:
			checkpoint, err := decodeCursor(payload.CheckpointCursorJson)
			if err != nil {
				return final, err
			}
			if err := h.Checkpoint(ctx, checkpoint); err != nil {
				return final, err
			}
		case *pluginproto.FetchEvent_FinalCursorJson:
			final, err = decodeCursor(payload.FinalCursorJson)
			if err != nil {
				return nil, err
			}
		}
	}
	return final, nil
}

func decodeCursor(raw []byte) (*types.SyncCursor, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var cursor types.SyncCursor
	if err := json.Unmarshal(raw, &cursor); err != nil {
		return nil, fmt.Errorf("decode plugin cursor: %w", err)
	}
	return &cursor, nil
}

func fromProtoItem(item *pluginproto.FetchedItem) types.FetchedItem {
	return types.FetchedItem{
		ExternalID: item.GetExternalId(), Title: item.GetTitle(), Content: item.GetContent(),
		ContentType: item.GetContentType(), FileName: item.GetFileName(), URL: item.GetUrl(),
		UpdatedAt: time.UnixMilli(item.GetUpdatedAtUnixMilli()).UTC(), Metadata: item.GetMetadata(),
		IsDeleted: item.GetIsDeleted(), SourceResourceID: item.GetSourceResourceId(),
		ReplacesSubtree: item.GetReplacesSubtree(), SubtreeKeep: item.GetSubtreeKeep(),
	}
}

var _ datasource.StreamingConnector = (*Connector)(nil)

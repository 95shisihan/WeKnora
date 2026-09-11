package websearchgrpc

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/infrastructure/web_search"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	pluginproto "github.com/Tencent/WeKnora/plugin/proto"
	"github.com/Tencent/WeKnora/plugin/sdk/transport"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"
)

type Connector struct {
	pluginID, version, providerType string
	conn                            *grpc.ClientConn
	client                          pluginproto.WebSearchPluginClient
}

func Dial(ctx context.Context, address, pluginID, version, providerType string) (*Connector, error) {
	conn, err := grpc.NewClient(address, append(transport.Options(address), grpc.WithTransportCredentials(insecure.NewCredentials()))...)
	if err != nil {
		return nil, fmt.Errorf("dial web search plugin %s: %w", pluginID, err)
	}
	connector := &Connector{pluginID: pluginID, version: version, providerType: providerType, conn: conn, client: pluginproto.NewWebSearchPluginClient(conn)}
	if err := connector.Health(ctx); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if err := transport.AttachHostServices(ctx, address, conn); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("attach host HTTP: %w", err)
	}
	return connector, nil
}

func (c *Connector) Health(ctx context.Context) error {
	healthResponse, err := grpc_health_v1.NewHealthClient(c.conn).Check(ctx, &grpc_health_v1.HealthCheckRequest{})
	if err != nil {
		return fmt.Errorf("plugin %s health check: %w", c.pluginID, err)
	}
	if healthResponse.GetStatus() != grpc_health_v1.HealthCheckResponse_SERVING {
		return fmt.Errorf("plugin %s health check: status %s, expected SERVING", c.pluginID, healthResponse.GetStatus())
	}
	info, err := c.client.GetInfo(ctx, &pluginproto.WebSearchInfoRequest{})
	if err != nil {
		return fmt.Errorf("plugin %s info: %w", c.pluginID, err)
	}
	if info.GetId() != c.pluginID || info.GetVersion() != c.version || info.GetProtocolVersion() != "v1" || info.GetProviderType() != c.providerType {
		return fmt.Errorf("web search plugin identity mismatch")
	}
	return nil
}

func (c *Connector) Close() error { return c.conn.Close() }

func (c *Connector) Factory() web_search.ProviderFactory {
	return func(params types.WebSearchProviderParameters) (interfaces.WebSearchProvider, error) {
		return &provider{name: c.providerType, params: params, client: c.client}, nil
	}
}

type provider struct {
	name   string
	params types.WebSearchProviderParameters
	client pluginproto.WebSearchPluginClient
}

func (p *provider) Name() string { return p.name }
func (p *provider) Search(ctx context.Context, query string, maxResults int, includeDate bool) ([]*types.WebSearchResult, error) {
	parameters, err := json.Marshal(p.params)
	if err != nil {
		return nil, fmt.Errorf("marshal web search plugin parameters: %w", err)
	}
	response, err := p.client.Search(ctx, &pluginproto.WebSearchRequest{ParametersJson: parameters, Query: query, MaxResults: int32(maxResults), IncludeDate: includeDate})
	if err != nil {
		return nil, err
	}
	results := make([]*types.WebSearchResult, 0, len(response.GetResults()))
	for _, item := range response.GetResults() {
		result := &types.WebSearchResult{Title: item.GetTitle(), URL: item.GetUrl(), Snippet: item.GetSnippet(), Content: item.GetContent(), Source: item.GetSource()}
		if item.GetHasPublishedAt() {
			value := time.UnixMilli(item.GetPublishedAtUnixMilli())
			result.PublishedAt = &value
		}
		results = append(results, result)
	}
	return results, nil
}

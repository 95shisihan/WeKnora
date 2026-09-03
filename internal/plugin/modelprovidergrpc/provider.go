package modelprovidergrpc

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/models/provider"
	"github.com/Tencent/WeKnora/internal/types"
	pluginproto "github.com/Tencent/WeKnora/plugin/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"
)

type Connector struct {
	pluginID, version, providerName string
	conn                            *grpc.ClientConn
	client                          pluginproto.ModelProviderPluginClient
	mu                              sync.RWMutex
	info                            provider.ProviderInfo
}

func Dial(ctx context.Context, address, pluginID, version, providerName string) (*Connector, error) {
	conn, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("dial model provider plugin %s: %w", pluginID, err)
	}
	connector := &Connector{pluginID: pluginID, version: version, providerName: providerName, conn: conn, client: pluginproto.NewModelProviderPluginClient(conn)}
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
	remote, err := c.client.GetInfo(ctx, &pluginproto.ModelProviderInfoRequest{})
	if err != nil {
		return fmt.Errorf("plugin %s info: %w", c.pluginID, err)
	}
	if remote.GetId() != c.pluginID || remote.GetVersion() != c.version || remote.GetProtocolVersion() != "v1" || remote.GetProviderName() != c.providerName {
		return fmt.Errorf("model provider plugin identity mismatch")
	}
	if remote.GetTransport() != "openai_compatible" {
		return fmt.Errorf("model provider plugin %s requests unsupported transport %q", c.pluginID, remote.GetTransport())
	}
	info, err := convertInfo(remote)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.info = info
	c.mu.Unlock()
	return nil
}

func convertInfo(remote *pluginproto.ModelProviderInfoResponse) (provider.ProviderInfo, error) {
	info := provider.ProviderInfo{
		Name: provider.ProviderName(remote.GetProviderName()), DisplayName: remote.GetDisplayName(),
		Description: remote.GetDescription(), RequiresAuth: remote.GetRequiresAuth(), DefaultURLs: make(map[types.ModelType]string),
	}
	for key, value := range remote.GetDefaultUrls() {
		modelType := types.ModelType(key)
		if !knownModelType(modelType) {
			return provider.ProviderInfo{}, fmt.Errorf("model provider %s declares unknown model type %q", info.Name, key)
		}
		info.DefaultURLs[modelType] = value
	}
	for _, value := range remote.GetModelTypes() {
		modelType := types.ModelType(value)
		if !knownModelType(modelType) {
			return provider.ProviderInfo{}, fmt.Errorf("model provider %s declares unknown model type %q", info.Name, value)
		}
		info.ModelTypes = append(info.ModelTypes, modelType)
	}
	for _, field := range remote.GetExtraFields() {
		converted := provider.ExtraFieldConfig{Key: field.GetKey(), Label: field.GetLabel(), Type: field.GetType(), Required: field.GetRequired(), Default: field.GetDefaultValue(), Placeholder: field.GetPlaceholder()}
		for _, option := range field.GetOptions() {
			converted.Options = append(converted.Options, struct {
				Label string `json:"label"`
				Value string `json:"value"`
			}{Label: option.GetLabel(), Value: option.GetValue()})
		}
		info.ExtraFields = append(info.ExtraFields, converted)
	}
	return info, nil
}

func knownModelType(value types.ModelType) bool {
	switch value {
	case types.ModelTypeEmbedding, types.ModelTypeRerank, types.ModelTypeKnowledgeQA, types.ModelTypeVLLM, types.ModelTypeASR:
		return true
	default:
		return false
	}
}

func (c *Connector) Info() provider.ProviderInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.info
}

func (c *Connector) ValidateConfig(config *provider.Config) error {
	raw, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf("marshal model provider config: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := c.client.ValidateConfig(ctx, &pluginproto.ModelProviderValidateRequest{ConfigJson: raw}); err != nil {
		return fmt.Errorf("model provider %s config validation: %w", c.providerName, err)
	}
	return nil
}

func (c *Connector) Close() error { return c.conn.Close() }

var _ provider.Provider = (*Connector)(nil)

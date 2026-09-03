package retrievalgrpc

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sync"

	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	pluginproto "github.com/Tencent/WeKnora/plugin/proto"
	retrievalsdk "github.com/Tencent/WeKnora/plugin/sdk/retrieval"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

type Connector struct {
	pluginID, version string
	engineType        types.RetrieverEngineType
	conn              *grpc.ClientConn
	client            pluginproto.RetrievalEnginePluginClient
	mu                sync.RWMutex
	support           []types.RetrieverType
}

func Dial(ctx context.Context, address, pluginID, version, engineType string) (*Connector, error) {
	conn, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("dial retrieval plugin %s: %w", pluginID, err)
	}
	c := &Connector{pluginID: pluginID, version: version, engineType: types.RetrieverEngineType(engineType), conn: conn, client: pluginproto.NewRetrievalEnginePluginClient(conn)}
	if err := c.Health(ctx); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return c, nil
}

func (c *Connector) Health(ctx context.Context) error {
	if _, err := grpc_health_v1.NewHealthClient(c.conn).Check(ctx, &grpc_health_v1.HealthCheckRequest{}); err != nil {
		return fmt.Errorf("plugin %s health check: %w", c.pluginID, err)
	}
	raw, err := c.client.GetInfo(ctx, &emptypb.Empty{})
	if err != nil {
		return fmt.Errorf("plugin %s info: %w", c.pluginID, err)
	}
	var info retrievalsdk.Info
	if err := json.Unmarshal(raw.GetValue(), &info); err != nil {
		return fmt.Errorf("plugin %s invalid info: %w", c.pluginID, err)
	}
	if info.ID != c.pluginID || info.Version != c.version || info.ProtocolVersion != "v1" || info.EngineType != string(c.engineType) {
		return fmt.Errorf("retrieval plugin identity mismatch")
	}
	if len(info.Support) == 0 {
		return fmt.Errorf("retrieval plugin %s declares no supported retrieval mode", c.pluginID)
	}
	support := make([]types.RetrieverType, 0, len(info.Support))
	for _, value := range info.Support {
		t := types.RetrieverType(value)
		if t != types.KeywordsRetrieverType && t != types.VectorRetrieverType {
			return fmt.Errorf("retrieval plugin %s declares unsupported mode %q", c.pluginID, value)
		}
		if !slices.Contains(support, t) {
			support = append(support, t)
		}
	}
	c.mu.Lock()
	c.support = support
	c.mu.Unlock()
	return nil
}

func (c *Connector) EngineType() types.RetrieverEngineType { return c.engineType }
func (c *Connector) Support() []types.RetrieverType {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return append([]types.RetrieverType(nil), c.support...)
}

func (c *Connector) Index(ctx context.Context, embedder embedding.Embedder, info *types.IndexInfo, retrieverTypes []types.RetrieverType) error {
	record, err := makeRecord(ctx, embedder, info, retrieverTypes)
	if err != nil {
		return err
	}
	return c.upsert(ctx, []retrievalsdk.IndexRecord{record})
}

func (c *Connector) BatchIndex(ctx context.Context, embedder embedding.Embedder, infos []*types.IndexInfo, retrieverTypes []types.RetrieverType) error {
	if len(infos) == 0 {
		return nil
	}
	records := make([]retrievalsdk.IndexRecord, len(infos))
	if slices.Contains(retrieverTypes, types.VectorRetrieverType) {
		texts := make([]string, len(infos))
		for i, info := range infos {
			texts[i] = info.Content
		}
		embeddings, err := embedder.BatchEmbedWithPool(ctx, embedder, texts)
		if err != nil {
			return err
		}
		if len(embeddings) != len(infos) {
			return fmt.Errorf("embedder returned %d vectors for %d records", len(embeddings), len(infos))
		}
		for i := range infos {
			records[i] = retrievalsdk.IndexRecord{Index: toWireIndex(infos[i]), Embedding: embeddings[i]}
		}
	} else {
		for i := range infos {
			records[i] = retrievalsdk.IndexRecord{Index: toWireIndex(infos[i])}
		}
	}
	return c.upsert(ctx, records)
}

func makeRecord(ctx context.Context, embedder embedding.Embedder, info *types.IndexInfo, modes []types.RetrieverType) (retrievalsdk.IndexRecord, error) {
	record := retrievalsdk.IndexRecord{Index: toWireIndex(info)}
	if slices.Contains(modes, types.VectorRetrieverType) {
		vector, err := embedder.Embed(ctx, info.Content)
		if err != nil {
			return retrievalsdk.IndexRecord{}, err
		}
		record.Embedding = vector
	}
	return record, nil
}

func (c *Connector) upsert(ctx context.Context, records []retrievalsdk.IndexRecord) error {
	raw, err := json.Marshal(retrievalsdk.UpsertRequest{Records: records})
	if err != nil {
		return err
	}
	_, err = c.client.Upsert(ctx, wrapperspb.Bytes(raw))
	return err
}

func (c *Connector) Retrieve(ctx context.Context, params types.RetrieveParams) ([]*types.RetrieveResult, error) {
	raw, err := json.Marshal(retrievalsdk.SearchRequest{Params: toWireParams(params)})
	if err != nil {
		return nil, err
	}
	response, err := c.client.Search(ctx, wrapperspb.Bytes(raw))
	if err != nil {
		return nil, err
	}
	var wire retrievalsdk.SearchResponse
	if err := json.Unmarshal(response.GetValue(), &wire); err != nil {
		return nil, fmt.Errorf("decode retrieval response: %w", err)
	}
	results := make([]*types.RetrieveResult, 0, len(wire.Results))
	for _, item := range wire.Results {
		converted := make([]*types.IndexWithScore, len(item.Results))
		for i, value := range item.Results {
			converted[i] = fromWireResult(value)
		}
		result := &types.RetrieveResult{Results: converted, RetrieverEngineType: types.RetrieverEngineType(item.RetrieverEngineType), RetrieverType: types.RetrieverType(item.RetrieverType)}
		if item.Error != "" {
			result.Error = fmt.Errorf("%s", item.Error)
		}
		results = append(results, result)
	}
	return results, nil
}

func (c *Connector) delete(ctx context.Context, kind string, ids []string, dimension int, knowledgeType string) error {
	raw, err := json.Marshal(retrievalsdk.DeleteRequest{Kind: kind, IDs: ids, Dimension: dimension, KnowledgeType: knowledgeType})
	if err != nil {
		return err
	}
	_, err = c.client.Delete(ctx, wrapperspb.Bytes(raw))
	return err
}
func (c *Connector) DeleteByChunkIDList(ctx context.Context, ids []string, dimension int, knowledgeType string) error {
	return c.delete(ctx, "chunk", ids, dimension, knowledgeType)
}
func (c *Connector) DeleteBySourceIDList(ctx context.Context, ids []string, dimension int, knowledgeType string) error {
	return c.delete(ctx, "source", ids, dimension, knowledgeType)
}
func (c *Connector) DeleteByKnowledgeIDList(ctx context.Context, ids []string, dimension int, knowledgeType string) error {
	return c.delete(ctx, "knowledge", ids, dimension, knowledgeType)
}

func (c *Connector) CopyIndices(ctx context.Context, source string, kbMap, chunkMap map[string]string, target string, dimension int, knowledgeType string) error {
	raw, err := json.Marshal(retrievalsdk.CopyRequest{SourceKnowledgeBaseID: source, SourceToTargetKBIDMap: kbMap, SourceToTargetChunkIDMap: chunkMap, TargetKnowledgeBaseID: target, Dimension: dimension, KnowledgeType: knowledgeType})
	if err != nil {
		return err
	}
	_, err = c.client.Copy(ctx, wrapperspb.Bytes(raw))
	return err
}
func (c *Connector) update(ctx context.Context, request retrievalsdk.UpdateRequest) error {
	raw, err := json.Marshal(request)
	if err != nil {
		return err
	}
	_, err = c.client.Update(ctx, wrapperspb.Bytes(raw))
	return err
}
func (c *Connector) BatchUpdateChunkEnabledStatus(ctx context.Context, values map[string]bool) error {
	return c.update(ctx, retrievalsdk.UpdateRequest{Kind: "enabled", Enabled: values})
}
func (c *Connector) BatchUpdateChunkTagID(ctx context.Context, values map[string]string) error {
	return c.update(ctx, retrievalsdk.UpdateRequest{Kind: "tag", Tags: values})
}

func (c *Connector) EstimateStorageSize(ctx context.Context, embedder embedding.Embedder, infos []*types.IndexInfo, modes []types.RetrieverType) int64 {
	dimension := 0
	if slices.Contains(modes, types.VectorRetrieverType) && embedder != nil {
		dimension = embedder.GetDimensions()
	}
	wireModes := make([]string, len(modes))
	for i, mode := range modes {
		wireModes[i] = string(mode)
	}
	wireInfos := make([]*retrievalsdk.IndexInfo, len(infos))
	for i, info := range infos {
		wireInfos[i] = toWireIndex(info)
	}
	raw, err := json.Marshal(retrievalsdk.EstimateRequest{Records: wireInfos, Dimension: dimension, RetrieverTypes: wireModes})
	if err != nil {
		return 0
	}
	value, err := c.client.Estimate(ctx, wrapperspb.Bytes(raw))
	if err != nil {
		return 0
	}
	return value.GetValue()
}

func (c *Connector) Close() error { return c.conn.Close() }

var _ interfaces.RetrieveEngineService = (*Connector)(nil)

func toWireIndex(value *types.IndexInfo) *retrievalsdk.IndexInfo {
	if value == nil {
		return nil
	}
	return &retrievalsdk.IndexInfo{ID: value.ID, Content: value.Content, SourceID: value.SourceID, SourceType: int(value.SourceType), ChunkID: value.ChunkID, KnowledgeID: value.KnowledgeID, KnowledgeBaseID: value.KnowledgeBaseID, KnowledgeType: value.KnowledgeType, TagID: value.TagID, IsEnabled: value.IsEnabled, IsRecommended: value.IsRecommended}
}
func toWireParams(value types.RetrieveParams) retrievalsdk.RetrieveParams {
	return retrievalsdk.RetrieveParams{Query: value.Query, Embedding: value.Embedding, KnowledgeBaseIDs: value.KnowledgeBaseIDs, KnowledgeIDs: value.KnowledgeIDs, TagIDs: value.TagIDs, ExcludeKnowledgeIDs: value.ExcludeKnowledgeIDs, ExcludeChunkIDs: value.ExcludeChunkIDs, TopK: value.TopK, Threshold: value.Threshold, KnowledgeType: value.KnowledgeType, AdditionalParams: value.AdditionalParams, RetrieverType: string(value.RetrieverType)}
}
func fromWireResult(value *retrievalsdk.IndexWithScore) *types.IndexWithScore {
	if value == nil {
		return nil
	}
	return &types.IndexWithScore{ID: value.ID, Content: value.Content, SourceID: value.SourceID, SourceType: types.SourceType(value.SourceType), ChunkID: value.ChunkID, KnowledgeID: value.KnowledgeID, KnowledgeBaseID: value.KnowledgeBaseID, TagID: value.TagID, Score: value.Score, MatchType: types.MatchType(value.MatchType), IsEnabled: value.IsEnabled}
}

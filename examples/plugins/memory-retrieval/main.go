package main

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"slices"
	"sort"
	"strings"
	"sync"

	pluginproto "github.com/Tencent/WeKnora/plugin/proto"
	retrievalsdk "github.com/Tencent/WeKnora/plugin/sdk/retrieval"
	"github.com/Tencent/WeKnora/plugin/sdk/transport"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

type server struct {
	pluginproto.UnimplementedRetrievalEnginePluginServer
	mu      sync.RWMutex
	records map[string]retrievalsdk.IndexRecord
}

func newServer() *server { return &server{records: make(map[string]retrievalsdk.IndexRecord)} }

func (s *server) GetInfo(context.Context, *emptypb.Empty) (*wrapperspb.BytesValue, error) {
	value, _ := json.Marshal(retrievalsdk.Info{ID: env("WEKNORA_PLUGIN_ID", "io.weknora.memory-retrieval"), Version: env("WEKNORA_PLUGIN_VERSION", "0.1.0"), ProtocolVersion: "v1", EngineType: "memory_plugin", Support: []string{"keywords", "vector"}})
	return wrapperspb.Bytes(value), nil
}

func (s *server) Upsert(_ context.Context, value *wrapperspb.BytesValue) (*emptypb.Empty, error) {
	var request retrievalsdk.UpsertRequest
	if err := decode(value, &request); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, record := range request.Records {
		if record.Index == nil || record.Index.ID == "" {
			return nil, status.Error(codes.InvalidArgument, "record index and ID are required")
		}
		s.records[record.Index.ID] = record
	}
	return &emptypb.Empty{}, nil
}

func (s *server) Search(_ context.Context, value *wrapperspb.BytesValue) (*wrapperspb.BytesValue, error) {
	var request retrievalsdk.SearchRequest
	if err := decode(value, &request); err != nil {
		return nil, err
	}
	p := request.Params
	s.mu.RLock()
	matches := make([]*retrievalsdk.IndexWithScore, 0, len(s.records))
	for _, record := range s.records {
		item := record.Index
		if !item.IsEnabled || !allowed(item, p) {
			continue
		}
		score := keywordScore(p.Query, item.Content)
		matchType := 1
		if p.RetrieverType == "vector" {
			score = cosine(p.Embedding, record.Embedding)
			matchType = 0
		}
		if score < p.Threshold {
			continue
		}
		matches = append(matches, &retrievalsdk.IndexWithScore{ID: item.ID, Content: item.Content, SourceID: item.SourceID, SourceType: item.SourceType, ChunkID: item.ChunkID, KnowledgeID: item.KnowledgeID, KnowledgeBaseID: item.KnowledgeBaseID, TagID: item.TagID, Score: score, MatchType: matchType, IsEnabled: item.IsEnabled})
	}
	s.mu.RUnlock()
	sort.Slice(matches, func(i, j int) bool { return matches[i].Score > matches[j].Score })
	if p.TopK > 0 && len(matches) > p.TopK {
		matches = matches[:p.TopK]
	}
	response, _ := json.Marshal(retrievalsdk.SearchResponse{Results: []retrievalsdk.SearchResult{{Results: matches, RetrieverEngineType: "memory_plugin", RetrieverType: p.RetrieverType}}})
	return wrapperspb.Bytes(response), nil
}

func allowed(item *retrievalsdk.IndexInfo, p retrievalsdk.RetrieveParams) bool {
	if len(p.KnowledgeBaseIDs) > 0 && !slices.Contains(p.KnowledgeBaseIDs, item.KnowledgeBaseID) {
		return false
	}
	if len(p.KnowledgeIDs) > 0 && !slices.Contains(p.KnowledgeIDs, item.KnowledgeID) {
		return false
	}
	if len(p.TagIDs) > 0 && !slices.Contains(p.TagIDs, item.TagID) {
		return false
	}
	if slices.Contains(p.ExcludeKnowledgeIDs, item.KnowledgeID) || slices.Contains(p.ExcludeChunkIDs, item.ChunkID) {
		return false
	}
	return p.KnowledgeType == "" || p.KnowledgeType == item.KnowledgeType
}

func keywordScore(query, content string) float64 {
	terms := strings.Fields(strings.ToLower(query))
	if len(terms) == 0 {
		return 0
	}
	lower := strings.ToLower(content)
	hits := 0
	for _, term := range terms {
		if strings.Contains(lower, term) {
			hits++
		}
	}
	return float64(hits) / float64(len(terms))
}
func cosine(a, b []float32) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, aa, bb float64
	for i := range a {
		x, y := float64(a[i]), float64(b[i])
		dot += x * y
		aa += x * x
		bb += y * y
	}
	if aa == 0 || bb == 0 {
		return 0
	}
	return dot / math.Sqrt(aa*bb)
}

func (s *server) Delete(_ context.Context, value *wrapperspb.BytesValue) (*emptypb.Empty, error) {
	var request retrievalsdk.DeleteRequest
	if err := decode(value, &request); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, record := range s.records {
		var candidate string
		switch request.Kind {
		case "chunk":
			candidate = record.Index.ChunkID
		case "source":
			candidate = record.Index.SourceID
		case "knowledge":
			candidate = record.Index.KnowledgeID
		default:
			return nil, status.Error(codes.InvalidArgument, "unknown delete kind")
		}
		if slices.Contains(request.IDs, candidate) {
			delete(s.records, id)
		}
	}
	return &emptypb.Empty{}, nil
}

func (s *server) Copy(_ context.Context, value *wrapperspb.BytesValue) (*emptypb.Empty, error) {
	var request retrievalsdk.CopyRequest
	if err := decode(value, &request); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, record := range s.records {
		if record.Index.KnowledgeBaseID != request.SourceKnowledgeBaseID {
			continue
		}
		chunkID, ok := request.SourceToTargetChunkIDMap[record.Index.ChunkID]
		if !ok {
			continue
		}
		copyIndex := *record.Index
		copyIndex.ID = chunkID
		copyIndex.ChunkID = chunkID
		copyIndex.KnowledgeBaseID = request.TargetKnowledgeBaseID
		if target, ok := request.SourceToTargetKBIDMap[copyIndex.KnowledgeID]; ok {
			copyIndex.KnowledgeID = target
		}
		s.records[copyIndex.ID] = retrievalsdk.IndexRecord{Index: &copyIndex, Embedding: append([]float32(nil), record.Embedding...)}
	}
	return &emptypb.Empty{}, nil
}

func (s *server) Update(_ context.Context, value *wrapperspb.BytesValue) (*emptypb.Empty, error) {
	var request retrievalsdk.UpdateRequest
	if err := decode(value, &request); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, record := range s.records {
		switch request.Kind {
		case "enabled":
			if enabled, ok := request.Enabled[record.Index.ChunkID]; ok {
				copyIndex := *record.Index
				copyIndex.IsEnabled = enabled
				record.Index = &copyIndex
				s.records[id] = record
			}
		case "tag":
			if tag, ok := request.Tags[record.Index.ChunkID]; ok {
				copyIndex := *record.Index
				copyIndex.TagID = tag
				record.Index = &copyIndex
				s.records[id] = record
			}
		default:
			return nil, status.Error(codes.InvalidArgument, "unknown update kind")
		}
	}
	return &emptypb.Empty{}, nil
}

func (s *server) Estimate(_ context.Context, value *wrapperspb.BytesValue) (*wrapperspb.Int64Value, error) {
	var request retrievalsdk.EstimateRequest
	if err := decode(value, &request); err != nil {
		return nil, err
	}
	var size int64
	for _, item := range request.Records {
		size += int64(len(item.Content) + len(item.ID) + request.Dimension*4)
	}
	return wrapperspb.Int64(size), nil
}

func decode(value *wrapperspb.BytesValue, target any) error {
	if err := json.Unmarshal(value.GetValue(), target); err != nil {
		return status.Error(codes.InvalidArgument, err.Error())
	}
	return nil
}

func main() {
	address := env("WEKNORA_PLUGIN_ADDRESS", "127.0.0.1:50105")
	listener, err := transport.Listen(address)
	if err != nil {
		panic(err)
	}
	grpcServer := grpc.NewServer()
	pluginproto.RegisterRetrievalEnginePluginServer(grpcServer, newServer())
	healthServer := health.NewServer()
	healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)
	grpc_health_v1.RegisterHealthServer(grpcServer, healthServer)
	if err := grpcServer.Serve(listener); err != nil {
		panic(err)
	}
}

func env(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

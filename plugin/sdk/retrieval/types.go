// Package retrieval defines the stable v1 JSON payloads carried by the
// RetrievalEnginePlugin protobuf service. It intentionally has no dependency
// on WeKnora internal packages, so plugins in independent repositories can
// import it.
package retrieval

type Info struct {
	ID              string   `json:"id"`
	Version         string   `json:"version"`
	ProtocolVersion string   `json:"protocol_version"`
	EngineType      string   `json:"engine_type"`
	Support         []string `json:"support"`
}

type IndexInfo struct {
	ID              string `json:"id"`
	Content         string `json:"content"`
	SourceID        string `json:"source_id"`
	SourceType      int    `json:"source_type"`
	ChunkID         string `json:"chunk_id"`
	KnowledgeID     string `json:"knowledge_id"`
	KnowledgeBaseID string `json:"knowledge_base_id"`
	KnowledgeType   string `json:"knowledge_type"`
	TagID           string `json:"tag_id"`
	IsEnabled       bool   `json:"is_enabled"`
	IsRecommended   bool   `json:"is_recommended"`
}

type IndexRecord struct {
	Index     *IndexInfo `json:"index"`
	Embedding []float32  `json:"embedding,omitempty"`
}
type UpsertRequest struct {
	Records []IndexRecord `json:"records"`
}

type RetrieveParams struct {
	Query               string                 `json:"query"`
	Embedding           []float32              `json:"embedding,omitempty"`
	KnowledgeBaseIDs    []string               `json:"knowledge_base_ids,omitempty"`
	KnowledgeIDs        []string               `json:"knowledge_ids,omitempty"`
	TagIDs              []string               `json:"tag_ids,omitempty"`
	ExcludeKnowledgeIDs []string               `json:"exclude_knowledge_ids,omitempty"`
	ExcludeChunkIDs     []string               `json:"exclude_chunk_ids,omitempty"`
	TopK                int                    `json:"top_k"`
	Threshold           float64                `json:"threshold"`
	KnowledgeType       string                 `json:"knowledge_type,omitempty"`
	AdditionalParams    map[string]interface{} `json:"additional_params,omitempty"`
	RetrieverType       string                 `json:"retriever_type"`
}
type SearchRequest struct {
	Params RetrieveParams `json:"params"`
}
type IndexWithScore struct {
	ID              string  `json:"id"`
	Content         string  `json:"content"`
	SourceID        string  `json:"source_id"`
	SourceType      int     `json:"source_type"`
	ChunkID         string  `json:"chunk_id"`
	KnowledgeID     string  `json:"knowledge_id"`
	KnowledgeBaseID string  `json:"knowledge_base_id"`
	TagID           string  `json:"tag_id"`
	Score           float64 `json:"score"`
	MatchType       int     `json:"match_type"`
	IsEnabled       bool    `json:"is_enabled"`
}
type SearchResult struct {
	Results             []*IndexWithScore `json:"results"`
	RetrieverEngineType string            `json:"retriever_engine_type"`
	RetrieverType       string            `json:"retriever_type"`
	Error               string            `json:"error,omitempty"`
}
type SearchResponse struct {
	Results []SearchResult `json:"results"`
}

type DeleteRequest struct {
	Kind          string   `json:"kind"`
	IDs           []string `json:"ids"`
	Dimension     int      `json:"dimension"`
	KnowledgeType string   `json:"knowledge_type"`
}
type CopyRequest struct {
	SourceKnowledgeBaseID    string            `json:"source_knowledge_base_id"`
	SourceToTargetKBIDMap    map[string]string `json:"source_to_target_kb_id_map"`
	SourceToTargetChunkIDMap map[string]string `json:"source_to_target_chunk_id_map"`
	TargetKnowledgeBaseID    string            `json:"target_knowledge_base_id"`
	Dimension                int               `json:"dimension"`
	KnowledgeType            string            `json:"knowledge_type"`
}
type UpdateRequest struct {
	Kind    string            `json:"kind"`
	Enabled map[string]bool   `json:"enabled,omitempty"`
	Tags    map[string]string `json:"tags,omitempty"`
}
type EstimateRequest struct {
	Records        []*IndexInfo `json:"records"`
	Dimension      int          `json:"dimension"`
	RetrieverTypes []string     `json:"retriever_types"`
}

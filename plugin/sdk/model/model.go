// Package model defines the vendor-independent model inference JSON contract.
// It is public: plugins never need to import WeKnora/internal packages.
package model

import (
	"bytes"
	"encoding/json"
	"fmt"
)

const Transport = "grpc_inference"
const MaxMessageBytes = 32 << 20

// Config is request-scoped. Never persist it globally or log its credentials.
type Config struct {
	ModelName     string            `json:"model_name"`
	ModelID       string            `json:"model_id"`
	BaseURL       string            `json:"base_url"`
	APIKey        string            `json:"api_key"`
	Extra         map[string]any    `json:"extra,omitempty"`
	CustomHeaders map[string]string `json:"custom_headers,omitempty"`
}

type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}
type ToolCall struct {
	ID               string                     `json:"id"`
	Type             string                     `json:"type"`
	Function         FunctionCall               `json:"function"`
	ProviderMetadata map[string]json.RawMessage `json:"provider_metadata,omitempty"`
}
type ImageURL struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}
type ContentPart struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *ImageURL `json:"image_url,omitempty"`
}
type Message struct {
	Role             string        `json:"role"`
	Content          string        `json:"content"`
	MultiContent     []ContentPart `json:"multi_content,omitempty"`
	Images           []string      `json:"images,omitempty"`
	Name             string        `json:"name,omitempty"`
	ToolCallID       string        `json:"tool_call_id,omitempty"`
	ToolCalls        []ToolCall    `json:"tool_calls,omitempty"`
	ReasoningContent string        `json:"reasoning_content,omitempty"`
}
type ChatInput struct {
	Messages []Message `json:"messages"`
	// Options follows the documented ChatOptions JSON schema. Preserve unknown
	// keys when forwarding; explicitly reject options your vendor cannot support.
	Options json.RawMessage `json:"options,omitempty"`
}
type Usage struct {
	PromptTokens     int    `json:"prompt_tokens"`
	CompletionTokens int    `json:"completion_tokens"`
	TotalTokens      int    `json:"total_tokens"`
	CachedTokens     int    `json:"cached_tokens,omitempty"`
	CacheReadTokens  int    `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens int    `json:"cache_write_tokens,omitempty"`
	CacheMissTokens  int    `json:"cache_miss_tokens,omitempty"`
	CacheReported    bool   `json:"cache_reported"`
	CacheStatus      string `json:"cache_status,omitempty"`
}
type ChatOutput struct {
	Content          string     `json:"content"`
	ReasoningContent string     `json:"reasoning_content,omitempty"`
	ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
	FinishReason     string     `json:"finish_reason,omitempty"`
	Usage            Usage      `json:"usage"`
}

// StreamOutput contains text deltas; tool_calls are complete calls (not JSON
// fragments). A thinking done marker only closes thinking. Exactly one final
// answer/error done marker terminates the entire stream.
type StreamOutput struct {
	ResponseType string     `json:"response_type"` // answer, thinking, tool_call, error
	Content      string     `json:"content"`
	Done         bool       `json:"done"`
	ToolCalls    []ToolCall `json:"tool_calls,omitempty"`
	Usage        *Usage     `json:"usage,omitempty"`
	FinishReason string     `json:"finish_reason,omitempty"`
}

func (s StreamOutput) Terminal() bool {
	return s.Done && (s.ResponseType == "answer" || s.ResponseType == "error")
}
func (s StreamOutput) Validate() error {
	switch s.ResponseType {
	case "answer", "thinking", "tool_call":
	case "error":
		if !s.Done {
			return fmt.Errorf("error event must terminate stream")
		}
	default:
		return fmt.Errorf("invalid model stream response_type")
	}
	return nil
}

type EmbedInput struct {
	Texts                     []string `json:"texts"`
	Dimensions                int      `json:"dimensions"`
	SupportsDimensionOverride bool     `json:"supports_dimension_override"`
	TruncatePromptTokens      int      `json:"truncate_prompt_tokens"`
}
type EmbedOutput struct {
	Vectors [][]float32 `json:"vectors"`
}
type RerankInput struct {
	Query     string   `json:"query"`
	Documents []string `json:"documents"`
}
type Document struct {
	Text string `json:"text"`
}
type Rank struct {
	Index          int      `json:"index"`
	Document       Document `json:"document"`
	RelevanceScore float64  `json:"relevance_score"`
}
type RerankOutput struct {
	Results []Rank `json:"results"`
}
type VisionInput struct {
	Images [][]byte `json:"images"` // JSON base64; no implicit filesystem reads
	Prompt string   `json:"prompt"`
}
type TextOutput struct {
	Text string `json:"text"`
}
type TranscribeInput struct {
	Audio    []byte `json:"audio"`
	FileName string `json:"file_name"`
	Language string `json:"language,omitempty"`
}
type Segment struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text"`
}
type TranscribeOutput struct {
	Text     string    `json:"text"`
	Segments []Segment `json:"segments,omitempty"`
}

// Decode rejects empty/null/scalar payloads and never embeds sensitive payloads
// in errors. Unknown object fields remain forward-compatible.
func Decode(raw []byte, dst any) error {
	b := bytes.TrimSpace(raw)
	if len(b) == 0 || len(b) > MaxMessageBytes || b[0] != '{' {
		return fmt.Errorf("invalid model JSON object or size")
	}
	if err := json.Unmarshal(b, dst); err != nil {
		return fmt.Errorf("invalid model JSON schema")
	}
	return nil
}
func Extra(values map[string]string) map[string]any {
	if values == nil {
		return nil
	}
	out := make(map[string]any, len(values))
	for k, v := range values {
		out[k] = v
	}
	return out
}

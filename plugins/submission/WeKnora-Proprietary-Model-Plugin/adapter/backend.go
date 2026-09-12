// Package adapter demonstrates an intentionally non-OpenAI vendor protocol:
// HMAC authentication, a custom request envelope and newline-delimited events.
// The reference endpoint is exercised by tests; it is not a real model vendor.
package adapter

import (
	"bufio"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	pb "example.org/weknora-proprietary-model/proto"
	"example.org/weknora-proprietary-model/sdk/model"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Backend struct {
	model.UnimplementedBackend
	Client *http.Client
}

func (*Backend) ValidateConfig(_ context.Context, config model.Config) error {
	u, err := url.Parse(config.BaseURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || config.APIKey == "" || config.ModelName == "" {
		return status.Error(codes.InvalidArgument, "model_name, api_key and HTTP(S) base_url required")
	}
	return nil
}

type VendorRequest struct {
	Engine    string          `json:"engine"`
	Action    string          `json:"action"`
	Arguments json.RawMessage `json:"arguments"`
}
type VendorResult struct {
	Reply        string           `json:"reply"`
	Reasoning    string           `json:"reasoning"`
	Calls        []model.ToolCall `json:"calls"`
	Stop         string           `json:"stop"`
	InputTokens  int              `json:"input_tokens"`
	OutputTokens int              `json:"output_tokens"`
	Vectors      [][]float32      `json:"encoded"`
	Ranks        []model.Rank     `json:"ranked"`
	Segments     []model.Segment  `json:"segments"`
}
type VendorEvent struct {
	Kind         string           `json:"kind"`
	Piece        string           `json:"piece"`
	Calls        []model.ToolCall `json:"calls,omitempty"`
	Stop         string           `json:"stop,omitempty"`
	InputTokens  int              `json:"input_tokens,omitempty"`
	OutputTokens int              `json:"output_tokens,omitempty"`
}

func (b *Backend) request(ctx context.Context, config model.Config, action string, input json.RawMessage) (*http.Response, error) {
	raw, err := json.Marshal(VendorRequest{Engine: config.ModelName, Action: action, Arguments: input})
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid vendor input")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(config.BaseURL, "/")+"/vendor/infer", bytes.NewReader(raw))
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid endpoint")
	}
	for name, value := range config.CustomHeaders {
		req.Header.Set(name, value)
	}
	mac := hmac.New(sha256.New, []byte(config.APIKey))
	_, _ = mac.Write(raw)
	req.Header.Set("X-Vendor-Signature", hex.EncodeToString(mac.Sum(nil)))
	req.Header.Set("Content-Type", "application/json")
	client := b.Client
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, status.FromContextError(ctx.Err()).Err()
		}
		return nil, status.Error(codes.Unavailable, "vendor request failed")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_ = resp.Body.Close()
		code := codes.Unavailable
		switch resp.StatusCode {
		case 401, 403:
			code = codes.PermissionDenied
		case 429:
			code = codes.ResourceExhausted
		case 400:
			code = codes.InvalidArgument
		}
		return nil, status.Error(code, "vendor rejected request")
	}
	return resp, nil
}
func (b *Backend) Infer(ctx context.Context, op pb.ModelOperation, config model.Config, input json.RawMessage) (any, error) {
	action := map[pb.ModelOperation]string{pb.ModelOperation_MODEL_OPERATION_CHAT: "speak", pb.ModelOperation_MODEL_OPERATION_EMBED: "encode", pb.ModelOperation_MODEL_OPERATION_RERANK: "rank", pb.ModelOperation_MODEL_OPERATION_VISION: "see", pb.ModelOperation_MODEL_OPERATION_TRANSCRIBE: "hear"}[op]
	if action == "" {
		return nil, status.Error(codes.Unimplemented, "unknown operation")
	}
	resp, err := b.request(ctx, config, action, input)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, model.MaxMessageBytes+1))
	if err != nil {
		return nil, status.Error(codes.Unavailable, "vendor response interrupted")
	}
	var result VendorResult
	if model.Decode(raw, &result) != nil {
		return nil, status.Error(codes.DataLoss, "invalid vendor response")
	}
	switch op {
	case pb.ModelOperation_MODEL_OPERATION_CHAT:
		return model.ChatOutput{Content: result.Reply, ReasoningContent: result.Reasoning, ToolCalls: result.Calls, FinishReason: result.Stop, Usage: model.Usage{PromptTokens: result.InputTokens, CompletionTokens: result.OutputTokens, TotalTokens: result.InputTokens + result.OutputTokens}}, nil
	case pb.ModelOperation_MODEL_OPERATION_EMBED:
		return model.EmbedOutput{Vectors: result.Vectors}, nil
	case pb.ModelOperation_MODEL_OPERATION_RERANK:
		return model.RerankOutput{Results: result.Ranks}, nil
	case pb.ModelOperation_MODEL_OPERATION_VISION:
		return model.TextOutput{Text: result.Reply}, nil
	default:
		return model.TranscribeOutput{Text: result.Reply, Segments: result.Segments}, nil
	}
}
func (b *Backend) InferStream(ctx context.Context, config model.Config, input model.ChatInput, emit func(model.StreamOutput) error) error {
	raw, err := json.Marshal(input)
	if err != nil {
		return status.Error(codes.InvalidArgument, "invalid chat input")
	}
	resp, err := b.request(ctx, config, "speak_stream", raw)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		var event VendorEvent
		if model.Decode(scanner.Bytes(), &event) != nil {
			return status.Error(codes.DataLoss, "invalid vendor event")
		}
		output := model.StreamOutput{Content: event.Piece}
		switch event.Kind {
		case "text":
			output.ResponseType = "answer"
		case "reason":
			output.ResponseType = "thinking"
		case "reason_end":
			output.ResponseType, output.Done = "thinking", true
		case "calls":
			output.ResponseType, output.ToolCalls = "tool_call", event.Calls
		case "end":
			output.ResponseType, output.Done, output.FinishReason = "answer", true, event.Stop
			output.Usage = &model.Usage{PromptTokens: event.InputTokens, CompletionTokens: event.OutputTokens, TotalTokens: event.InputTokens + event.OutputTokens}
		case "failure":
			return status.Error(codes.Unavailable, "vendor stream failed")
		default:
			return status.Error(codes.DataLoss, "unknown vendor event")
		}
		if err := emit(output); err != nil {
			return err
		}
		if output.Terminal() {
			return nil
		}
	}
	if ctx.Err() != nil {
		return status.FromContextError(ctx.Err()).Err()
	}
	return status.Error(codes.DataLoss, "vendor stream interrupted")
}

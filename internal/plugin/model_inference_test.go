package plugin

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/examples/plugins/proprietary-model/adapter"
	"github.com/Tencent/WeKnora/internal/models/asr"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/models/provider"
	"github.com/Tencent/WeKnora/internal/models/rerank"
	"github.com/Tencent/WeKnora/internal/models/vlm"
	"github.com/Tencent/WeKnora/internal/types"
	pb "github.com/Tencent/WeKnora/plugin/proto"
	"github.com/Tencent/WeKnora/plugin/sdk/model"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

type inferenceFixture struct {
	server   *httptest.Server
	requests chan adapter.VendorRequest
	canceled chan string
}

func newInferenceFixture(t *testing.T) *inferenceFixture {
	f := &inferenceFixture{requests: make(chan adapter.VendorRequest, 100), canceled: make(chan string, 10)}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			return
		}
		var req adapter.VendorRequest
		if r.URL.Path != "/vendor/infer" || r.Method != "POST" || json.Unmarshal(raw, &req) != nil {
			http.Error(w, "wrong protocol", 400)
			return
		}
		secret := "test-secret"
		if tenant := r.Header.Get("X-Tenant"); tenant != "" {
			secret = tenant + "-secret"
		}
		mac := hmac.New(sha256.New, []byte(secret))
		_, _ = mac.Write(raw)
		if !hmac.Equal([]byte(r.Header.Get("X-Vendor-Signature")), []byte(hex.EncodeToString(mac.Sum(nil)))) || r.Header.Get("Authorization") != "" {
			http.Error(w, "bad signature", 403)
			return
		}
		f.requests <- req
		if req.Engine == "denied" {
			http.Error(w, "secret-vendor-debug-body", 403)
			return
		}
		if req.Engine == "deadline" {
			<-r.Context().Done()
			f.canceled <- req.Engine
			return
		}
		result := adapter.VendorResult{}
		switch req.Action {
		case "speak":
			var input model.ChatInput
			if model.Decode(req.Arguments, &input) != nil || len(input.Messages) == 0 {
				http.Error(w, "missing messages", 400)
				return
			}
			result = adapter.VendorResult{Reply: "custom:" + input.Messages[len(input.Messages)-1].Content, Reasoning: "reasoning preserved", Stop: "tool_calls", InputTokens: 7, OutputTokens: 3, Calls: []model.ToolCall{{ID: "call-1", Type: "function", Function: model.FunctionCall{Name: "lookup", Arguments: `{"id":42}`}, ProviderMetadata: map[string]json.RawMessage{"vendor_state": json.RawMessage(`"opaque-state"`)}}}}
		case "speak_stream":
			w.Header().Set("Content-Type", "application/x-ndjson")
			send := func(event adapter.VendorEvent) { _ = json.NewEncoder(w).Encode(event); w.(http.Flusher).Flush() }
			send(adapter.VendorEvent{Kind: "reason", Piece: "think"})
			if req.Engine == "cancel" {
				<-r.Context().Done()
				f.canceled <- req.Engine
				return
			}
			send(adapter.VendorEvent{Kind: "reason_end"})
			send(adapter.VendorEvent{Kind: "text", Piece: "custom stream"})
			if req.Engine == "broken" {
				return
			}
			if req.Engine == "failure" {
				send(adapter.VendorEvent{Kind: "failure", Piece: "secret-vendor-debug-body"})
				return
			}
			send(adapter.VendorEvent{Kind: "calls", Calls: []model.ToolCall{{ID: "call-stream", Type: "function", Function: model.FunctionCall{Name: "lookup", Arguments: `{"stream":true}`}}}})
			send(adapter.VendorEvent{Kind: "end", Stop: "tool_calls", InputTokens: 7, OutputTokens: 3})
			return
		case "encode":
			var input model.EmbedInput
			_ = model.Decode(req.Arguments, &input)
			for range input.Texts {
				result.Vectors = append(result.Vectors, []float32{1, 2, 3})
			}
			if req.Engine == "bad-count" {
				result.Vectors = nil
			}
			if req.Engine == "bad-dim" {
				result.Vectors = [][]float32{{1}}
			}
		case "rank":
			result.Ranks = []model.Rank{{Index: 1, RelevanceScore: 0.9}, {Index: 0, RelevanceScore: 0.1}}
			if req.Engine == "bad-rank" {
				result.Ranks[0].Index = 900
			}
		case "see":
			var input model.VisionInput
			_ = model.Decode(req.Arguments, &input)
			result.Reply = fmt.Sprintf("images:%d:%s", len(input.Images), input.Prompt)
		case "hear":
			var input model.TranscribeInput
			_ = model.Decode(req.Arguments, &input)
			result.Reply = fmt.Sprintf("audio:%d:%s:%s", len(input.Audio), input.FileName, input.Language)
			result.Segments = []model.Segment{{Start: 0, End: 1, Text: "spoken"}}
		default:
			http.Error(w, "unknown vendor operation", 400)
			return
		}
		_ = json.NewEncoder(w).Encode(result)
	}))
	t.Cleanup(f.server.Close)
	return f
}

func startInferenceManager(t *testing.T, native bool) (*Manager, *Manifest) {
	var manifest *Manifest
	if native {
		source := os.Getenv("WEKNORA_PROPRIETARY_MODEL_EXE")
		if source == "" {
			t.Skip("build proprietary model EXE and set WEKNORA_PROPRIETARY_MODEL_EXE")
		}
		dir := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "bin"), 0700))
		require.NoError(t, copyExecutable(source, filepath.Join(dir, "bin", "proprietary-model.exe")))
		raw, err := os.ReadFile("../../examples/plugins/proprietary-model/plugin.yaml")
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(dir, "plugin.yaml"), raw, 0600))
		manifest, err = LoadManifest(filepath.Join(dir, "plugin.yaml"), "0.7.2")
		require.NoError(t, err)
	} else {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		server := grpc.NewServer(grpc.MaxRecvMsgSize(model.MaxMessageBytes), grpc.MaxSendMsgSize(model.MaxMessageBytes))
		pb.RegisterModelProviderPluginServer(server, &model.Server{Info: &pb.ModelProviderInfoResponse{Id: "io.weknora.proprietary-model", Version: "0.1.0", ProtocolVersion: "v1", ProviderName: "proprietary_model", ModelTypes: []string{"KnowledgeQA", "Embedding", "Rerank", "VLLM", "ASR"}}, Backend: &adapter.Backend{}})
		h := health.NewServer()
		h.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)
		grpc_health_v1.RegisterHealthServer(server, h)
		go func() { _ = server.Serve(listener) }()
		t.Cleanup(server.Stop)
		manifest = &Manifest{Metadata: Metadata{ID: "io.weknora.proprietary-model", Version: "0.1.0"}, Spec: Spec{ExtensionPoints: []ExtensionPoint{{Type: ExtensionModelProvider, ID: "proprietary_model", ProtocolVersion: "v1"}}, Runtime: Runtime{Type: "grpc", Address: listener.Addr().String(), StartupTimeout: 3 * time.Second}, Permissions: Permissions{Network: NetworkPermission{Outbound: true}}}}
	}
	manager := NewManager("0.7.2", nil)
	t.Cleanup(func() { _ = provider.UnregisterExternal("proprietary_model"); _ = manager.Close() })
	require.NoError(t, manager.Load(context.Background(), []*Manifest{manifest}))
	require.Len(t, manager.ModelProviders(), 1)
	require.NoError(t, provider.RegisterExternal(manager.ModelProviders()[0].Connector))
	return manager, manifest
}

func TestModelInferenceFactories(t *testing.T)       { runInferenceFactories(t, false) }
func TestNativeModelInferenceFactories(t *testing.T) { runInferenceFactories(t, true) }

func runInferenceFactories(t *testing.T, native bool) {
	f := newInferenceFixture(t)
	manager, manifest := startInferenceManager(t, native)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	base := f.server.URL
	chatConfig := func(name string) *chat.ChatConfig {
		return &chat.ChatConfig{Source: types.ModelSourceRemote, Provider: "proprietary_model", BaseURL: base, APIKey: "test-secret", ModelName: name, ModelID: "chat-model"}
	}
	c, err := chat.NewChat(chatConfig("normal"), nil)
	require.NoError(t, err)
	response, err := c.Chat(ctx, []chat.Message{{Role: "user", Content: "hello", MultiContent: []chat.MessageContentPart{{Type: "image_url", ImageURL: &chat.ImageURL{URL: "data:image/png;base64,AA=="}}}}}, &chat.ChatOptions{Temperature: 0.3, Tools: []chat.Tool{{Type: "function", Function: chat.FunctionDef{Name: "lookup", Parameters: json.RawMessage(`{"type":"object"}`)}}}})
	require.NoError(t, err)
	require.Equal(t, "custom:hello", response.Content)
	require.Equal(t, 10, response.Usage.TotalTokens)
	require.Equal(t, "reasoning preserved", response.ReasoningContent)
	require.Equal(t, `{"id":42}`, response.ToolCalls[0].Function.Arguments)
	require.JSONEq(t, `"opaque-state"`, string(response.ToolCalls[0].ProviderMetadata["vendor_state"]))
	received := <-f.requests
	var forwarded model.ChatInput
	require.NoError(t, model.Decode(received.Arguments, &forwarded))
	require.Len(t, forwarded.Messages[0].MultiContent, 1)
	require.Contains(t, string(forwarded.Options), "lookup")
	stream, err := c.ChatStream(ctx, []chat.Message{{Role: "user", Content: "hello"}}, nil)
	require.NoError(t, err)
	var events []types.StreamResponse
	for event := range stream {
		events = append(events, event)
	}
	require.Len(t, events, 5)
	require.Equal(t, types.ResponseTypeThinking, events[0].ResponseType)
	require.True(t, events[1].Done)
	require.Equal(t, "custom stream", events[2].Content)
	require.Equal(t, "call-stream", events[3].ToolCalls[0].ID)
	require.True(t, events[4].Done)
	require.Equal(t, 10, events[4].Usage.TotalTokens)
	e, err := embedding.NewEmbedder(embedding.Config{Source: types.ModelSourceRemote, Provider: "proprietary_model", BaseURL: base, APIKey: "test-secret", ModelName: "normal", Dimensions: 3}, nil, nil)
	require.NoError(t, err)
	rows, err := e.BatchEmbedWithPool(ctx, e, []string{"one", "two"})
	require.NoError(t, err)
	require.Equal(t, [][]float32{{1, 2, 3}, {1, 2, 3}}, rows)
	r, err := rerank.NewReranker(&rerank.RerankerConfig{Provider: "proprietary_model", BaseURL: base, APIKey: "test-secret", ModelName: "normal"})
	require.NoError(t, err)
	ranked, err := r.Rerank(ctx, "q", []string{"first", "second"})
	require.NoError(t, err)
	require.Equal(t, "second", ranked[0].Document.Text)
	v, err := vlm.NewVLM(&vlm.Config{Source: types.ModelSourceRemote, Provider: "proprietary_model", BaseURL: base, APIKey: "test-secret", ModelName: "normal"}, nil)
	require.NoError(t, err)
	description, err := v.Predict(ctx, [][]byte{{0, 255, 1}}, "describe")
	require.NoError(t, err)
	require.Equal(t, "images:1:describe", description)
	a, err := asr.NewASR(&asr.Config{Provider: "proprietary_model", BaseURL: base, APIKey: "test-secret", ModelName: "normal", Language: "zh"})
	require.NoError(t, err)
	transcript, err := a.Transcribe(ctx, []byte{0, 255, 1}, "sample.wav")
	require.NoError(t, err)
	require.Equal(t, "audio:3:sample.wav:zh", transcript.Text)
	require.Len(t, transcript.Segments, 1)
	// Simultaneous tenants share the connector, but never configuration/secrets.
	var wg sync.WaitGroup
	failures := make(chan error, 2)
	for _, tenant := range []string{"tenant-a", "tenant-b"} {
		wg.Add(1)
		go func(tenant string) {
			defer wg.Done()
			cfg := chatConfig(tenant)
			cfg.APIKey = tenant + "-secret"
			cfg.CustomHeaders = map[string]string{"X-Tenant": tenant}
			client, err := chat.NewRemoteChat(cfg)
			if err == nil {
				_, err = client.Chat(ctx, []chat.Message{{Role: "user", Content: tenant}}, nil)
			}
			failures <- err
		}(tenant)
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		require.NoError(t, err)
	}
	_, err = manager.Disable(manifest.Metadata.ID)
	require.NoError(t, err)
	// Production disable also unregisters the provider. Existing instances must
	// fail through the closed gRPC channel, new ones through the registry tombstone.
	require.NoError(t, provider.UnregisterExternal("proprietary_model"))
	_, err = c.Chat(ctx, []chat.Message{{Role: "user", Content: "after disable"}}, nil)
	require.Error(t, err)
	_, err = chat.NewRemoteChat(chatConfig("normal"))
	require.ErrorContains(t, err, "disabled or unavailable")
	enabled, err := manager.Enable(ctx, manifest.Metadata.ID)
	require.NoError(t, err)
	require.Len(t, enabled.ModelProviders, 1)
	require.NoError(t, provider.RegisterExternal(enabled.ModelProviders[0].Connector))
	c, err = chat.NewRemoteChat(chatConfig("normal"))
	require.NoError(t, err)
	_, err = c.Chat(ctx, []chat.Message{{Role: "user", Content: "reenabled"}}, nil)
	require.NoError(t, err)
	t.Log("Five model factories reached HMAC vendor protocol; tools/reasoning/usage, NDJSON stream, binary media, per-request credentials and disable/re-enable verified")
}

func TestModelInferenceFailuresAndCancellation(t *testing.T) {
	f := newInferenceFixture(t)
	_, _ = startInferenceManager(t, false)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client := func(name string) chat.Chat {
		c, err := chat.NewRemoteChat(&chat.ChatConfig{Provider: "proprietary_model", BaseURL: f.server.URL, APIKey: "test-secret", ModelName: name})
		require.NoError(t, err)
		return c
	}
	messages := []chat.Message{{Role: "user", Content: "hello"}}
	_, err := client("denied").Chat(ctx, messages, nil)
	require.Equal(t, codes.PermissionDenied, status.Code(err))
	require.NotContains(t, err.Error(), "secret-vendor")
	for _, name := range []string{"broken", "failure"} {
		t.Run(name, func(t *testing.T) {
			stream, err := client(name).ChatStream(ctx, messages, nil)
			require.NoError(t, err)
			var last types.StreamResponse
			for event := range stream {
				last = event
			}
			require.Equal(t, types.ResponseTypeError, last.ResponseType)
			require.True(t, last.Done)
			require.NotContains(t, last.Content, "secret-vendor")
		})
	}
	cancelCtx, stop := context.WithCancel(ctx)
	stream, err := client("cancel").ChatStream(cancelCtx, messages, nil)
	require.NoError(t, err)
	select {
	case <-stream:
	case <-ctx.Done():
		t.Fatal("no initial streaming event")
	}
	stop()
	for range stream {
	}
	select {
	case name := <-f.canceled:
		require.Equal(t, "cancel", name)
	case <-time.After(3 * time.Second):
		t.Fatal("cancellation did not reach vendor HTTP")
	}
	deadlineCtx, deadlineStop := context.WithTimeout(ctx, 100*time.Millisecond)
	defer deadlineStop()
	_, err = client("deadline").Chat(deadlineCtx, messages, nil)
	require.Equal(t, codes.DeadlineExceeded, status.Code(err))
	select {
	case name := <-f.canceled:
		require.Equal(t, "deadline", name)
	case <-time.After(3 * time.Second):
		t.Fatal("deadline did not cancel vendor")
	}
	for _, name := range []string{"bad-count", "bad-dim"} {
		e, err := embedding.NewEmbedder(embedding.Config{Source: types.ModelSourceRemote, Provider: "proprietary_model", BaseURL: f.server.URL, APIKey: "test-secret", ModelName: name, Dimensions: 3}, nil, nil)
		require.NoError(t, err)
		_, err = e.Embed(ctx, "one")
		require.Error(t, err)
	}
	r, err := rerank.NewReranker(&rerank.RerankerConfig{Provider: "proprietary_model", BaseURL: f.server.URL, APIKey: "test-secret", ModelName: "bad-rank"})
	require.NoError(t, err)
	_, err = r.Rerank(ctx, "q", []string{"one", "two"})
	require.Error(t, err)
}

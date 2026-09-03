package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"

	pluginproto "github.com/Tencent/WeKnora/plugin/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

type server struct {
	pluginproto.UnimplementedModelProviderPluginServer
}

func (server) GetInfo(context.Context, *pluginproto.ModelProviderInfoRequest) (*pluginproto.ModelProviderInfoResponse, error) {
	return &pluginproto.ModelProviderInfoResponse{
		Id: env("WEKNORA_PLUGIN_ID", "io.weknora.mock-model-provider"), Version: env("WEKNORA_PLUGIN_VERSION", "0.1.0"), ProtocolVersion: "v1",
		ProviderName: "mock_openai", DisplayName: "Mock OpenAI-Compatible", Description: "Model provider plugin conformance example",
		DefaultUrls: map[string]string{"KnowledgeQA": "https://example.invalid/v1", "Embedding": "https://example.invalid/v1", "Rerank": "https://example.invalid/v1"},
		ModelTypes:  []string{"KnowledgeQA", "Embedding", "Rerank"}, RequiresAuth: true, Transport: "openai_compatible",
		ExtraFields: []*pluginproto.ModelProviderConfigField{{Key: "organization", Label: "Organization", Type: "string", DefaultValue: "default"}},
	}, nil
}

func (server) ValidateConfig(_ context.Context, request *pluginproto.ModelProviderValidateRequest) (*pluginproto.ModelProviderValidateResponse, error) {
	if err := validateConfig(request.GetConfigJson()); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	return &pluginproto.ModelProviderValidateResponse{}, nil
}

func validateConfig(raw []byte) error {
	var config struct {
		BaseURL   string `json:"base_url"`
		APIKey    string `json:"api_key"`
		ModelName string `json:"model_name"`
	}
	if err := json.Unmarshal(raw, &config); err != nil {
		return fmt.Errorf("invalid config JSON: %w", err)
	}
	if strings.TrimSpace(config.BaseURL) == "" || strings.TrimSpace(config.APIKey) == "" || strings.TrimSpace(config.ModelName) == "" {
		return fmt.Errorf("base_url, api_key and model_name are required")
	}
	return nil
}

func main() {
	address := env("WEKNORA_PLUGIN_ADDRESS", "127.0.0.1:50104")
	network, target := "tcp", address
	if strings.HasPrefix(address, "unix://") {
		network, target = "unix", strings.TrimPrefix(address, "unix://")
		_ = os.Remove(target)
	}
	listener, err := net.Listen(network, target)
	if err != nil {
		panic(err)
	}
	grpcServer := grpc.NewServer()
	pluginproto.RegisterModelProviderPluginServer(grpcServer, server{})
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

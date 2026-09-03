package main

import (
	"context"
	"net"
	"os"
	"strings"

	pluginproto "github.com/Tencent/WeKnora/plugin/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
)

type server struct {
	pluginproto.UnimplementedWebSearchPluginServer
}

func (server) GetInfo(context.Context, *pluginproto.WebSearchInfoRequest) (*pluginproto.WebSearchInfoResponse, error) {
	return &pluginproto.WebSearchInfoResponse{Id: env("WEKNORA_PLUGIN_ID", "io.weknora.mock-search"), Version: env("WEKNORA_PLUGIN_VERSION", "0.1.0"), ProtocolVersion: "v1", ProviderType: "mock_search"}, nil
}
func (server) Search(_ context.Context, request *pluginproto.WebSearchRequest) (*pluginproto.WebSearchResponse, error) {
	if request.GetMaxResults() <= 0 {
		return &pluginproto.WebSearchResponse{}, nil
	}
	return &pluginproto.WebSearchResponse{Results: []*pluginproto.WebSearchResult{{Title: "Result for " + request.GetQuery(), Url: "https://example.invalid/search?q=" + request.GetQuery(), Snippet: "Deterministic plugin result", Source: "mock_search"}}}, nil
}

func main() {
	address := env("WEKNORA_PLUGIN_ADDRESS", "127.0.0.1:50102")
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
	pluginproto.RegisterWebSearchPluginServer(grpcServer, server{})
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

package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	pluginproto "github.com/Tencent/WeKnora/plugin/proto"
	"github.com/Tencent/WeKnora/plugin/sdk/transport"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
)

type server struct {
	pluginproto.UnimplementedDocumentParserPluginServer
}

func (server) GetInfo(context.Context, *pluginproto.DocumentParserInfoRequest) (*pluginproto.DocumentParserInfoResponse, error) {
	return &pluginproto.DocumentParserInfoResponse{
		Id: env("WEKNORA_PLUGIN_ID", "io.weknora.plain-text-parser"), Version: env("WEKNORA_PLUGIN_VERSION", "0.1.0"),
		ProtocolVersion: "v1", EngineName: "plain_text_plugin", Description: "Out-of-process plain text and Markdown parser",
		FileTypes: []string{"txt", "md", "markdown"},
	}, nil
}

func (server) Parse(request *pluginproto.DocumentParseRequest, stream grpc.ServerStreamingServer[pluginproto.DocumentParseFrame]) error {
	markdown, err := parse(request)
	if err != nil {
		return err
	}
	return stream.Send(&pluginproto.DocumentParseFrame{Payload: &pluginproto.DocumentParseFrame_Meta{Meta: &pluginproto.DocumentParseMeta{
		MarkdownContent: markdown,
		Metadata:        map[string]string{"parser": "plain_text_plugin", "file_type": request.GetFileType()},
	}}})
}

func parse(request *pluginproto.DocumentParseRequest) (string, error) {
	switch strings.ToLower(strings.TrimPrefix(request.GetFileType(), ".")) {
	case "txt", "md", "markdown":
		return string(request.GetFileContent()), nil
	default:
		return "", fmt.Errorf("unsupported file type %q", request.GetFileType())
	}
}

func main() {
	address := env("WEKNORA_PLUGIN_ADDRESS", "127.0.0.1:50103")
	listener, err := transport.Listen(address)
	if err != nil {
		panic(err)
	}
	grpcServer := grpc.NewServer(grpc.MaxRecvMsgSize(128 * 1024 * 1024))
	pluginproto.RegisterDocumentParserPluginServer(grpcServer, server{})
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

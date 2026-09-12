package main

import (
	"log"
	"os"

	pb "github.com/Tencent/WeKnora/plugin/proto"
	"github.com/Tencent/WeKnora/plugin/sdk/model"
	"github.com/Tencent/WeKnora/plugin/sdk/transport"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
)

func main() {
	listener, err := transport.Listen(os.Getenv("WEKNORA_PLUGIN_ADDRESS"))
	if err != nil {
		log.Fatal(err)
	}
	server := grpc.NewServer(grpc.MaxRecvMsgSize(model.MaxMessageBytes), grpc.MaxSendMsgSize(model.MaxMessageBytes))
	pb.RegisterModelProviderPluginServer(server, &model.Server{Info: &pb.ModelProviderInfoResponse{Id: "dev.example.model-plugin", Version: "0.1.0", ProtocolVersion: "v1", ProviderName: "example_model", DisplayName: "Minimal model plugin", RequiresAuth: false, ModelTypes: []string{"KnowledgeQA"}}, Backend: &model.UnimplementedBackend{}})
	h := health.NewServer()
	h.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)
	grpc_health_v1.RegisterHealthServer(server, h)
	if err := server.Serve(listener); err != nil {
		log.Fatal(err)
	}
}

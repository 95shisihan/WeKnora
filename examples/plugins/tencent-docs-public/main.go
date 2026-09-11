// Public Tencent Docs datasource using host-controlled HTTPS.
package main

import (
	"context"

	"os"

	pb "github.com/Tencent/WeKnora/plugin/proto"
	"github.com/Tencent/WeKnora/plugin/sdk/hosthttp"
	"github.com/Tencent/WeKnora/plugin/sdk/transport"
	"google.golang.org/grpc"

	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
)

const pluginID = "dev.example.tencent-docs-public"
const connectorType = "tencent_docs_public_probe"

type requester interface {
	Do(context.Context, hosthttp.Request) (*hosthttp.Response, error)
}

type server struct {
	pb.UnimplementedDatasourcePluginServer
	http requester
}

func (s *server) GetInfo(context.Context, *pb.GetInfoRequest) (*pb.GetInfoResponse, error) {
	return &pb.GetInfoResponse{Id: pluginID, Version: "0.2.0", ProtocolVersion: "v1", ConnectorType: connectorType}, nil
}

func main() {
	address := os.Getenv("WEKNORA_PLUGIN_ADDRESS")
	if address == "" {
		address = "stdio://"
	}
	l, err := transport.Listen(address)
	if err != nil {
		panic(err)
	}
	defer l.Close()
	g := grpc.NewServer()
	pb.RegisterDatasourcePluginServer(g, &server{http: hosthttp.Register(g)})
	h := health.NewServer()
	h.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)
	grpc_health_v1.RegisterHealthServer(g, h)
	if err := g.Serve(l); err != nil {
		panic(err)
	}
}

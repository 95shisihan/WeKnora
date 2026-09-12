// Controlled HTTP datasource smoke example. Compile; upload with plugin.yaml.
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"

	pb "example.org/weknora-controlled-http/proto"
	"example.org/weknora-controlled-http/sdk/hosthttp"
	"example.org/weknora-controlled-http/sdk/transport"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

type server struct {
	pb.UnimplementedDatasourcePluginServer
	http *hosthttp.Client
}

func (s *server) GetInfo(context.Context, *pb.GetInfoRequest) (*pb.GetInfoResponse, error) {
	return &pb.GetInfoResponse{Id: "io.weknora.controlled-http", Version: "0.1.0", ProtocolVersion: "v1", ConnectorType: "controlled_http"}, nil
}
func (s *server) Validate(ctx context.Context, req *pb.ConfigRequest) (*pb.Empty, error) {
	var config struct {
		Settings struct {
			URL string `json:"url"`
		} `json:"settings"`
	}
	if json.Unmarshal(req.ConfigJson, &config) != nil || config.Settings.URL == "" {
		return nil, status.Error(codes.InvalidArgument, "url is required")
	}
	response, err := s.http.Do(ctx, hosthttp.Request{URL: config.Settings.URL, Method: "GET", Headers: http.Header{"User-Agent": []string{"Mozilla/5.0"}}})
	if err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}
	if response.StatusCode != 200 {
		return nil, status.Errorf(codes.FailedPrecondition, "HTTP %d", response.StatusCode)
	}
	return &pb.Empty{}, nil
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
	client := hosthttp.Register(g)
	pb.RegisterDatasourcePluginServer(g, &server{http: client})
	h := health.NewServer()
	h.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)
	grpc_health_v1.RegisterHealthServer(g, h)
	if err := g.Serve(l); err != nil {
		panic(err)
	}
}

package pluginproto

import (
	"context"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

type modelProviderTestServer struct {
	UnimplementedModelProviderPluginServer
}

func (modelProviderTestServer) GetInfo(context.Context, *ModelProviderInfoRequest) (*ModelProviderInfoResponse, error) {
	return &ModelProviderInfoResponse{Id: "dev.test.model", Version: "1.0.0", ProtocolVersion: "v1", ProviderName: "test_model", Transport: "openai_compatible"}, nil
}
func (modelProviderTestServer) ValidateConfig(context.Context, *ModelProviderValidateRequest) (*ModelProviderValidateResponse, error) {
	return &ModelProviderValidateResponse{}, nil
}

func TestModelProviderGRPCRoundTrip(t *testing.T) {
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	RegisterModelProviderPluginServer(server, modelProviderTestServer{})
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	conn, err := grpc.NewClient("passthrough:///bufnet", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	client := NewModelProviderPluginClient(conn)
	info, err := client.GetInfo(context.Background(), &ModelProviderInfoRequest{})
	if err != nil || info.GetProviderName() != "test_model" {
		t.Fatalf("GetInfo = (%v, %v)", info, err)
	}
	if _, err := client.ValidateConfig(context.Background(), &ModelProviderValidateRequest{ConfigJson: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
}

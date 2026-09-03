package pluginproto

import (
	"context"
	"net"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

type retrievalTestServer struct {
	UnimplementedRetrievalEnginePluginServer
}

func (retrievalTestServer) GetInfo(context.Context, *emptypb.Empty) (*wrapperspb.BytesValue, error) {
	return wrapperspb.Bytes([]byte(`{"protocol_version":"v1"}`)), nil
}
func (retrievalTestServer) Upsert(_ context.Context, request *wrapperspb.BytesValue) (*emptypb.Empty, error) {
	if len(request.GetValue()) == 0 {
		return nil, context.Canceled
	}
	return &emptypb.Empty{}, nil
}

func TestRetrievalEngineGRPCRoundTrip(t *testing.T) {
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	RegisterRetrievalEnginePluginServer(server, retrievalTestServer{})
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	conn, err := grpc.NewClient("passthrough:///bufnet", grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }), grpc.WithInsecure())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	client := NewRetrievalEnginePluginClient(conn)
	info, err := client.GetInfo(context.Background(), &emptypb.Empty{})
	require.NoError(t, err)
	require.Contains(t, string(info.GetValue()), "v1")
	_, err = client.Upsert(context.Background(), wrapperspb.Bytes([]byte(`{"records":[]}`)))
	require.NoError(t, err)
}

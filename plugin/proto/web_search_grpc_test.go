package pluginproto

import (
	"context"
	"net"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

type testWebSearchServer struct {
	UnimplementedWebSearchPluginServer
}

func (testWebSearchServer) GetInfo(context.Context, *WebSearchInfoRequest) (*WebSearchInfoResponse, error) {
	return &WebSearchInfoResponse{Id: "dev.test", Version: "1.0.0", ProtocolVersion: "v1", ProviderType: "test_search"}, nil
}
func (testWebSearchServer) Search(_ context.Context, request *WebSearchRequest) (*WebSearchResponse, error) {
	return &WebSearchResponse{Results: []*WebSearchResult{{Title: request.GetQuery(), Url: "https://example.test", Source: "test_search"}}}, nil
}

func TestWebSearchPluginRoundTrip(t *testing.T) {
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	RegisterWebSearchPluginServer(server, testWebSearchServer{})
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	conn, err := grpc.NewClient("passthrough:///web-search-test", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	client := NewWebSearchPluginClient(conn)
	info, err := client.GetInfo(context.Background(), &WebSearchInfoRequest{})
	require.NoError(t, err)
	require.Equal(t, "test_search", info.GetProviderType())
	response, err := client.Search(context.Background(), &WebSearchRequest{Query: "hello", MaxResults: 1})
	require.NoError(t, err)
	require.Len(t, response.GetResults(), 1)
	require.Equal(t, "hello", response.GetResults()[0].GetTitle())
}

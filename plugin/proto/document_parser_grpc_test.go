package pluginproto

import (
	"context"
	"io"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

type documentParserTestServer struct {
	UnimplementedDocumentParserPluginServer
}

func (documentParserTestServer) GetInfo(context.Context, *DocumentParserInfoRequest) (*DocumentParserInfoResponse, error) {
	return &DocumentParserInfoResponse{Id: "dev.test.parser", Version: "1.0.0", ProtocolVersion: "v1", EngineName: "test_parser"}, nil
}

func (documentParserTestServer) Parse(_ *DocumentParseRequest, stream grpc.ServerStreamingServer[DocumentParseFrame]) error {
	if err := stream.Send(&DocumentParseFrame{Payload: &DocumentParseFrame_Meta{Meta: &DocumentParseMeta{MarkdownContent: "parsed"}}}); err != nil {
		return err
	}
	return stream.Send(&DocumentParseFrame{Payload: &DocumentParseFrame_Image{Image: &DocumentImageRef{Filename: "image.png", ImageData: []byte("png")}}})
}

func TestDocumentParserGRPCRoundTrip(t *testing.T) {
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	RegisterDocumentParserPluginServer(server, documentParserTestServer{})
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)

	conn, err := grpc.NewClient("passthrough:///bufnet", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
		return listener.Dial()
	}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	client := NewDocumentParserPluginClient(conn)
	info, err := client.GetInfo(context.Background(), &DocumentParserInfoRequest{})
	if err != nil || info.GetEngineName() != "test_parser" {
		t.Fatalf("GetInfo = (%v, %v)", info, err)
	}
	stream, err := client.Parse(context.Background(), &DocumentParseRequest{FileName: "test.txt", FileContent: []byte("input")})
	if err != nil {
		t.Fatal(err)
	}
	first, err := stream.Recv()
	if err != nil || first.GetMeta().GetMarkdownContent() != "parsed" {
		t.Fatalf("first frame = (%v, %v)", first, err)
	}
	second, err := stream.Recv()
	if err != nil || second.GetImage().GetFilename() != "image.png" {
		t.Fatalf("second frame = (%v, %v)", second, err)
	}
	if _, err := stream.Recv(); err != io.EOF {
		t.Fatalf("stream end = %v, want EOF", err)
	}
}

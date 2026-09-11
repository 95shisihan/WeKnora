package modelprovidergrpc

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"

	pb "github.com/Tencent/WeKnora/plugin/proto"
	"github.com/Tencent/WeKnora/plugin/sdk/model"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

type wireProbe struct {
	pb.UnimplementedModelProviderPluginServer
	transport string
}

func (s *wireProbe) GetInfo(context.Context, *pb.ModelProviderInfoRequest) (*pb.ModelProviderInfoResponse, error) {
	return &pb.ModelProviderInfoResponse{Id: "test.model", Version: "1", ProtocolVersion: "v1", ProviderName: "wire_model", Transport: s.transport, ModelTypes: []string{"KnowledgeQA"}}, nil
}
func (*wireProbe) Infer(_ context.Context, req *pb.ModelInferenceRequest) (*pb.ModelInferenceResponse, error) {
	var cfg model.Config
	_ = json.Unmarshal(req.ConfigJson, &cfg)
	switch cfg.ModelName {
	case "null":
		return &pb.ModelInferenceResponse{OutputJson: []byte(`null`)}, nil
	case "missing":
		return &pb.ModelInferenceResponse{OutputJson: []byte(`{}`)}, nil
	case "wrong":
		return &pb.ModelInferenceResponse{OutputJson: []byte(`{"content":42}`)}, nil
	default:
		return nil, status.Error(codes.PermissionDenied, "credential-secret-must-not-escape")
	}
}
func (*wireProbe) InferStream(req *pb.ModelInferenceRequest, stream grpc.ServerStreamingServer[pb.ModelInferenceResponse]) error {
	var cfg model.Config
	_ = json.Unmarshal(req.ConfigJson, &cfg)
	if cfg.ModelName == "eof" {
		return nil
	}
	return stream.Send(&pb.ModelInferenceResponse{OutputJson: []byte(`{"response_type":"install_prompt","content":"not a model event","done":true}`)})
}
func TestInferenceContractRejectsUnsupportedAndMalformedResponses(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := grpc.NewServer()
	pb.RegisterModelProviderPluginServer(server, &wireProbe{transport: model.Transport})
	h := health.NewServer()
	h.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)
	grpc_health_v1.RegisterHealthServer(server, h)
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()
	c, err := Dial(context.Background(), listener.Addr().String(), "test.model", "1", "wire_model")
	require.NoError(t, err)
	defer c.Close()
	for _, name := range []string{"null", "missing", "wrong"} {
		var out model.ChatOutput
		err = c.Infer(context.Background(), pb.ModelOperation_MODEL_OPERATION_CHAT, model.Config{ModelName: name}, model.ChatInput{}, &out)
		require.Equal(t, codes.DataLoss, status.Code(err), name)
	}
	var out model.ChatOutput
	err = c.Infer(context.Background(), pb.ModelOperation_MODEL_OPERATION_CHAT, model.Config{}, model.ChatInput{}, &out)
	require.Equal(t, codes.PermissionDenied, status.Code(err))
	require.NotContains(t, err.Error(), "credential-secret")
	err = c.Infer(context.Background(), pb.ModelOperation_MODEL_OPERATION_EMBED, model.Config{}, model.EmbedInput{}, &out)
	require.Equal(t, codes.Unimplemented, status.Code(err))
	err = c.Infer(context.Background(), pb.ModelOperation_MODEL_OPERATION_CHAT, model.Config{}, model.ChatInput{Messages: []model.Message{{Content: strings.Repeat("x", model.MaxMessageBytes)}}}, &out)
	require.Equal(t, codes.ResourceExhausted, status.Code(err))
	for _, name := range []string{"eof", "injected"} {
		emitted := false
		err = c.InferStream(context.Background(), model.Config{ModelName: name}, model.ChatInput{}, func(context.Context, model.StreamOutput) error { emitted = true; return nil })
		require.Error(t, err)
		require.False(t, emitted)
	}
}

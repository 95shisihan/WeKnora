package model

import (
	"context"
	"encoding/json"
	"slices"

	pb "github.com/Tencent/WeKnora/plugin/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// Backend translates WeKnora operations into any vendor protocol. Implementations
// must use ctx for outgoing requests, and must not retain request credentials.
type Backend interface {
	ValidateConfig(context.Context, Config) error
	Infer(context.Context, pb.ModelOperation, Config, json.RawMessage) (any, error)
	InferStream(context.Context, Config, ChatInput, func(StreamOutput) error) error
}

type UnimplementedBackend struct{}

func (UnimplementedBackend) ValidateConfig(context.Context, Config) error { return nil }
func (UnimplementedBackend) Infer(context.Context, pb.ModelOperation, Config, json.RawMessage) (any, error) {
	return nil, status.Error(codes.Unimplemented, "operation not implemented")
}
func (UnimplementedBackend) InferStream(context.Context, Config, ChatInput, func(StreamOutput) error) error {
	return status.Error(codes.Unimplemented, "streaming not implemented")
}

type Server struct {
	pb.UnimplementedModelProviderPluginServer
	Info    *pb.ModelProviderInfoResponse
	Backend Backend
}

func (s *Server) GetInfo(context.Context, *pb.ModelProviderInfoRequest) (*pb.ModelProviderInfoResponse, error) {
	if s.Info == nil || s.Backend == nil {
		return nil, status.Error(codes.FailedPrecondition, "model backend unavailable")
	}
	info := proto.Clone(s.Info).(*pb.ModelProviderInfoResponse)
	info.Transport = Transport
	return info, nil
}
func (s *Server) ValidateConfig(ctx context.Context, req *pb.ModelProviderValidateRequest) (*pb.ModelProviderValidateResponse, error) {
	var config Config
	if Decode(req.GetConfigJson(), &config) != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid configuration")
	}
	if s.Backend == nil {
		return nil, status.Error(codes.FailedPrecondition, "model backend unavailable")
	}
	if err := s.Backend.ValidateConfig(ctx, config); err != nil {
		return nil, err
	}
	return &pb.ModelProviderValidateResponse{}, nil
}
func ModelType(op pb.ModelOperation) string {
	switch op {
	case pb.ModelOperation_MODEL_OPERATION_CHAT:
		return "KnowledgeQA"
	case pb.ModelOperation_MODEL_OPERATION_EMBED:
		return "Embedding"
	case pb.ModelOperation_MODEL_OPERATION_RERANK:
		return "Rerank"
	case pb.ModelOperation_MODEL_OPERATION_VISION:
		return "VLLM"
	case pb.ModelOperation_MODEL_OPERATION_TRANSCRIBE:
		return "ASR"
	default:
		return ""
	}
}
func (s *Server) decode(ctx context.Context, req *pb.ModelInferenceRequest) (Config, error) {
	var config Config
	if s.Info == nil || s.Backend == nil {
		return config, status.Error(codes.FailedPrecondition, "model backend unavailable")
	}
	if kind := ModelType(req.GetOperation()); kind == "" || !slices.Contains(s.Info.ModelTypes, kind) {
		return config, status.Error(codes.Unimplemented, "unsupported model operation")
	}
	var object map[string]json.RawMessage
	if len(req.GetInputJson())+len(req.GetConfigJson()) > MaxMessageBytes-1024 || Decode(req.GetConfigJson(), &config) != nil || Decode(req.GetInputJson(), &object) != nil {
		return config, status.Error(codes.InvalidArgument, "invalid model request")
	}
	return config, s.Backend.ValidateConfig(ctx, config)
}
func Encode(output any) (*pb.ModelInferenceResponse, error) {
	raw, err := json.Marshal(output)
	if err != nil {
		return nil, status.Error(codes.Internal, "invalid model output")
	}
	var object map[string]json.RawMessage
	if len(raw) > MaxMessageBytes-1024 {
		return nil, status.Error(codes.ResourceExhausted, "model output exceeds 32 MiB")
	}
	if Decode(raw, &object) != nil {
		return nil, status.Error(codes.Internal, "model output must be an object")
	}
	return &pb.ModelInferenceResponse{OutputJson: raw}, nil
}
func (s *Server) Infer(ctx context.Context, req *pb.ModelInferenceRequest) (*pb.ModelInferenceResponse, error) {
	config, err := s.decode(ctx, req)
	if err != nil {
		return nil, err
	}
	output, err := s.Backend.Infer(ctx, req.Operation, config, req.InputJson)
	if err != nil {
		return nil, err
	}
	return Encode(output)
}
func (s *Server) InferStream(req *pb.ModelInferenceRequest, stream grpc.ServerStreamingServer[pb.ModelInferenceResponse]) error {
	if req.GetOperation() != pb.ModelOperation_MODEL_OPERATION_CHAT {
		return status.Error(codes.Unimplemented, "only chat supports streaming")
	}
	config, err := s.decode(stream.Context(), req)
	if err != nil {
		return err
	}
	var input ChatInput
	if Decode(req.InputJson, &input) != nil {
		return status.Error(codes.InvalidArgument, "invalid chat input")
	}
	finished := false
	err = s.Backend.InferStream(stream.Context(), config, input, func(event StreamOutput) error {
		if finished {
			return status.Error(codes.FailedPrecondition, "event after terminal marker")
		}
		if event.Validate() != nil {
			return status.Error(codes.Internal, "invalid model stream event")
		}
		frame, err := Encode(event)
		if err != nil {
			return err
		}
		if err := stream.Send(frame); err != nil {
			return err
		}
		finished = event.Terminal()
		return nil
	})
	if err != nil {
		return err
	}
	if !finished {
		return status.Error(codes.DataLoss, "model stream ended without terminal event")
	}
	return nil
}

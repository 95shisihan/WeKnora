package modelprovidergrpc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	pb "github.com/Tencent/WeKnora/plugin/proto"
	"github.com/Tencent/WeKnora/plugin/sdk/model"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (c *Connector) UsesInference() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.transport == model.Transport
}

func (c *Connector) request(op pb.ModelOperation, config model.Config, input any) (*pb.ModelInferenceRequest, error) {
	kind := map[pb.ModelOperation]types.ModelType{
		pb.ModelOperation_MODEL_OPERATION_CHAT:       types.ModelTypeKnowledgeQA,
		pb.ModelOperation_MODEL_OPERATION_EMBED:      types.ModelTypeEmbedding,
		pb.ModelOperation_MODEL_OPERATION_RERANK:     types.ModelTypeRerank,
		pb.ModelOperation_MODEL_OPERATION_VISION:     types.ModelTypeVLLM,
		pb.ModelOperation_MODEL_OPERATION_TRANSCRIBE: types.ModelTypeASR,
	}[op]
	c.mu.RLock()
	allowed := c.transport == model.Transport && kind != "" && slices.Contains(c.info.ModelTypes, kind)
	c.mu.RUnlock()
	if !allowed {
		return nil, status.Error(codes.Unimplemented, "model plugin does not support this operation")
	}
	conf, err := json.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("invalid model configuration")
	}
	body, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("invalid model input")
	}
	if len(conf)+len(body) > model.MaxMessageBytes-1024 {
		return nil, status.Error(codes.ResourceExhausted, "model request exceeds 32 MiB")
	}
	return &pb.ModelInferenceRequest{Operation: op, ConfigJson: conf, InputJson: body}, nil
}

// Do not propagate vendor response bodies or credentials in arbitrary RPC error
// messages. Preserve the gRPC code, including cancellation and deadlines.
func inferenceError(err error) error {
	if err == nil {
		return nil
	}
	if err == context.Canceled || err == context.DeadlineExceeded {
		return status.FromContextError(err).Err()
	}
	return status.Error(status.Code(err), "model plugin inference failed")
}

func (c *Connector) Infer(ctx context.Context, op pb.ModelOperation, config model.Config, input, output any) error {
	req, err := c.request(op, config, input)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	response, err := c.client.Infer(ctx, req, grpc.MaxCallRecvMsgSize(model.MaxMessageBytes), grpc.MaxCallSendMsgSize(model.MaxMessageBytes))
	if err != nil {
		return inferenceError(err)
	}
	var fields map[string]json.RawMessage
	if err := model.Decode(response.GetOutputJson(), &fields); err != nil {
		return status.Error(codes.DataLoss, "invalid model output")
	}
	required := map[pb.ModelOperation]string{pb.ModelOperation_MODEL_OPERATION_CHAT: "content", pb.ModelOperation_MODEL_OPERATION_EMBED: "vectors", pb.ModelOperation_MODEL_OPERATION_RERANK: "results", pb.ModelOperation_MODEL_OPERATION_VISION: "text", pb.ModelOperation_MODEL_OPERATION_TRANSCRIBE: "text"}[op]
	if value, ok := fields[required]; !ok || string(value) == "null" {
		return status.Error(codes.DataLoss, "model output missing required field")
	}
	if err := model.Decode(response.GetOutputJson(), output); err != nil {
		return status.Error(codes.DataLoss, "invalid model output")
	}
	return nil
}

func (c *Connector) InferStream(ctx context.Context, config model.Config, input any, emit func(context.Context, model.StreamOutput) error) error {
	req, err := c.request(pb.ModelOperation_MODEL_OPERATION_CHAT, config, input)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	stream, err := c.client.InferStream(ctx, req, grpc.MaxCallRecvMsgSize(model.MaxMessageBytes), grpc.MaxCallSendMsgSize(model.MaxMessageBytes))
	if err != nil {
		return inferenceError(err)
	}
	for {
		response, err := stream.Recv()
		if err == io.EOF {
			return status.Error(codes.DataLoss, "model stream ended without terminal event")
		}
		if err != nil {
			return inferenceError(err)
		}
		var event model.StreamOutput
		if err := model.Decode(response.GetOutputJson(), &event); err != nil {
			return err
		}
		if err := event.Validate(); err != nil {
			return err
		}
		if event.ResponseType == "error" {
			event.Content = "model plugin inference failed"
		}
		if err := emit(ctx, event); err != nil {
			return err
		}
		if event.Terminal() {
			return nil
		}
	}
}

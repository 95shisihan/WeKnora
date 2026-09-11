package chat

import (
	"context"
	"encoding/json"

	"github.com/Tencent/WeKnora/internal/models/provider"
	"github.com/Tencent/WeKnora/internal/types"
	pb "github.com/Tencent/WeKnora/plugin/proto"
	"github.com/Tencent/WeKnora/plugin/sdk/model"
)

type pluginChat struct {
	executor provider.Inference
	config   model.Config
}

func (p *pluginChat) GetModelName() string { return p.config.ModelName }
func (p *pluginChat) GetModelID() string   { return p.config.ModelID }
func pluginChatInput(messages []Message, opts *ChatOptions) (model.ChatInput, error) {
	var input model.ChatInput
	if err := provider.ConvertJSON(messages, &input.Messages); err != nil {
		return input, err
	}
	raw, err := json.Marshal(opts)
	input.Options = raw
	return input, err
}
func (p *pluginChat) Chat(ctx context.Context, messages []Message, opts *ChatOptions) (*types.ChatResponse, error) {
	input, err := pluginChatInput(messages, opts)
	if err != nil {
		return nil, err
	}
	var output model.ChatOutput
	if err := p.executor.Infer(ctx, pb.ModelOperation_MODEL_OPERATION_CHAT, p.config, input, &output); err != nil {
		return nil, err
	}
	var result types.ChatResponse
	if err := provider.ConvertJSON(output, &result); err != nil {
		return nil, err
	}
	return &result, nil
}
func (p *pluginChat) ChatStream(ctx context.Context, messages []Message, opts *ChatOptions) (<-chan types.StreamResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	input, err := pluginChatInput(messages, opts)
	if err != nil {
		return nil, err
	}
	out := make(chan types.StreamResponse, 8)
	go func() {
		defer close(out)
		err := p.executor.InferStream(ctx, p.config, input, func(streamCtx context.Context, event model.StreamOutput) error {
			var result types.StreamResponse
			if err := provider.ConvertJSON(event, &result); err != nil {
				return err
			}
			select {
			case out <- result:
				return nil
			case <-streamCtx.Done():
				return streamCtx.Err()
			}
		})
		if err != nil && ctx.Err() == nil {
			failure := types.StreamResponse{ResponseType: types.ResponseTypeError, Content: err.Error(), Done: true}
			// Reserve room for the terminal error even when a slow consumer filled
			// the bounded queue. Never hang after the upstream deadline expires.
			select {
			case out <- failure:
			default:
				select {
				case <-out:
				default:
				}
				select {
				case out <- failure:
				case <-ctx.Done():
				}
			}
		}
	}()
	return out, nil
}

package asr

import (
	"context"
	"fmt"

	"github.com/Tencent/WeKnora/internal/models/provider"
	pb "github.com/Tencent/WeKnora/plugin/proto"
	"github.com/Tencent/WeKnora/plugin/sdk/model"
)

type pluginASR struct {
	executor provider.Inference
	config   model.Config
	language string
}

func (p *pluginASR) GetModelName() string { return p.config.ModelName }
func (p *pluginASR) GetModelID() string   { return p.config.ModelID }
func (p *pluginASR) Transcribe(ctx context.Context, audio []byte, name string) (*TranscriptionResult, error) {
	var output model.TranscribeOutput
	if err := p.executor.Infer(ctx, pb.ModelOperation_MODEL_OPERATION_TRANSCRIBE, p.config, model.TranscribeInput{Audio: audio, FileName: name, Language: p.language}, &output); err != nil {
		return nil, err
	}
	for _, segment := range output.Segments {
		if segment.Start < 0 || segment.End < segment.Start {
			return nil, fmt.Errorf("invalid model plugin transcription timestamps")
		}
	}
	var result TranscriptionResult
	if err := provider.ConvertJSON(output, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

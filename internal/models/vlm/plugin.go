package vlm

import (
	"context"

	"github.com/Tencent/WeKnora/internal/models/provider"
	pb "github.com/Tencent/WeKnora/plugin/proto"
	"github.com/Tencent/WeKnora/plugin/sdk/model"
)

type pluginVLM struct {
	executor provider.Inference
	config   model.Config
}

func (p *pluginVLM) GetModelName() string { return p.config.ModelName }
func (p *pluginVLM) GetModelID() string   { return p.config.ModelID }
func (p *pluginVLM) Predict(ctx context.Context, images [][]byte, prompt string) (string, error) {
	var output model.TextOutput
	err := p.executor.Infer(ctx, pb.ModelOperation_MODEL_OPERATION_VISION, p.config, model.VisionInput{Images: images, Prompt: prompt}, &output)
	return output.Text, err
}

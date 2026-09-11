package embedding

import (
	"context"
	"fmt"
	"math"

	"github.com/Tencent/WeKnora/internal/models/provider"
	pb "github.com/Tencent/WeKnora/plugin/proto"
	"github.com/Tencent/WeKnora/plugin/sdk/model"
)

type pluginEmbedder struct {
	executor provider.Inference
	config   Config
	pooler   EmbedderPooler
}

func (p *pluginEmbedder) GetModelName() string { return p.config.ModelName }
func (p *pluginEmbedder) GetModelID() string   { return p.config.ModelID }
func (p *pluginEmbedder) GetDimensions() int   { return p.config.Dimensions }
func (p *pluginEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	rows, err := p.BatchEmbed(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	return rows[0], nil
}
func (p *pluginEmbedder) BatchEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return [][]float32{}, nil
	}
	config := model.Config{ModelName: p.config.ModelName, ModelID: p.config.ModelID, BaseURL: p.config.BaseURL, APIKey: p.config.APIKey, Extra: model.Extra(p.config.ExtraConfig), CustomHeaders: p.config.CustomHeaders}
	input := model.EmbedInput{Texts: texts, Dimensions: p.config.Dimensions, SupportsDimensionOverride: p.config.SupportsDimensionOverride, TruncatePromptTokens: p.config.TruncatePromptTokens}
	var output model.EmbedOutput
	if err := p.executor.Infer(ctx, pb.ModelOperation_MODEL_OPERATION_EMBED, config, input, &output); err != nil {
		return nil, err
	}
	if len(output.Vectors) != len(texts) {
		return nil, fmt.Errorf("model plugin returned incorrect vector count")
	}
	dim := p.config.Dimensions
	for _, vector := range output.Vectors {
		if dim <= 0 {
			dim = len(vector)
		}
		if len(vector) == 0 || len(vector) != dim {
			return nil, fmt.Errorf("model plugin returned incorrect vector dimensions")
		}
		for _, value := range vector {
			if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
				return nil, fmt.Errorf("model plugin returned non-finite vector")
			}
		}
	}
	return output.Vectors, nil
}
func (p *pluginEmbedder) BatchEmbedWithPool(ctx context.Context, target Embedder, texts []string) ([][]float32, error) {
	if p.pooler != nil {
		return p.pooler.BatchEmbedWithPool(ctx, target, texts)
	}
	return target.BatchEmbed(ctx, texts)
}

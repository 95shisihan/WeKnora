package rerank

import (
	"context"
	"fmt"
	"math"

	"github.com/Tencent/WeKnora/internal/models/provider"
	pb "github.com/Tencent/WeKnora/plugin/proto"
	"github.com/Tencent/WeKnora/plugin/sdk/model"
)

type pluginReranker struct {
	executor provider.Inference
	config   model.Config
}

func (p *pluginReranker) GetModelName() string { return p.config.ModelName }
func (p *pluginReranker) GetModelID() string   { return p.config.ModelID }
func (p *pluginReranker) Rerank(ctx context.Context, query string, documents []string) ([]RankResult, error) {
	var output model.RerankOutput
	if err := p.executor.Infer(ctx, pb.ModelOperation_MODEL_OPERATION_RERANK, p.config, model.RerankInput{Query: query, Documents: documents}, &output); err != nil {
		return nil, err
	}
	seen := map[int]bool{}
	result := make([]RankResult, 0, len(output.Results))
	for _, row := range output.Results {
		if row.Index < 0 || row.Index >= len(documents) || seen[row.Index] || math.IsNaN(row.RelevanceScore) || math.IsInf(row.RelevanceScore, 0) {
			return nil, fmt.Errorf("invalid model plugin rerank result")
		}
		seen[row.Index] = true
		result = append(result, RankResult{Index: row.Index, Document: DocumentInfo{Text: documents[row.Index]}, RelevanceScore: row.RelevanceScore})
	}
	return result, nil
}

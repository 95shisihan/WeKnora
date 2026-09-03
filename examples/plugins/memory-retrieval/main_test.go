package main

import (
	"context"
	"encoding/json"
	"testing"

	retrievalsdk "github.com/Tencent/WeKnora/plugin/sdk/retrieval"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func TestIncrementalUpsertSearchAndDelete(t *testing.T) {
	s := newServer()
	ctx := context.Background()
	upsert, _ := json.Marshal(retrievalsdk.UpsertRequest{Records: []retrievalsdk.IndexRecord{
		{Index: &retrievalsdk.IndexInfo{ID: "1", ChunkID: "c1", SourceID: "s1", KnowledgeID: "k1", KnowledgeBaseID: "kb", Content: "alpha document", IsEnabled: true}, Embedding: []float32{1, 0}},
		{Index: &retrievalsdk.IndexInfo{ID: "2", ChunkID: "c2", SourceID: "s2", KnowledgeID: "k2", KnowledgeBaseID: "kb", Content: "beta document", IsEnabled: true}, Embedding: []float32{0, 1}},
	}})
	_, err := s.Upsert(ctx, wrapperspb.Bytes(upsert))
	require.NoError(t, err)

	search, _ := json.Marshal(retrievalsdk.SearchRequest{Params: retrievalsdk.RetrieveParams{Query: "alpha", KnowledgeBaseIDs: []string{"kb"}, TopK: 5, Threshold: 0.01, RetrieverType: "keywords"}})
	response, err := s.Search(ctx, wrapperspb.Bytes(search))
	require.NoError(t, err)
	var found retrievalsdk.SearchResponse
	require.NoError(t, json.Unmarshal(response.GetValue(), &found))
	require.Len(t, found.Results[0].Results, 1)
	require.Equal(t, "c1", found.Results[0].Results[0].ChunkID)

	// Upsert only the changed record; the other record remains intact.
	changed, _ := json.Marshal(retrievalsdk.UpsertRequest{Records: []retrievalsdk.IndexRecord{{Index: &retrievalsdk.IndexInfo{ID: "1", ChunkID: "c1", SourceID: "s1", KnowledgeID: "k1", KnowledgeBaseID: "kb", Content: "gamma document", IsEnabled: true}, Embedding: []float32{1, 0}}}})
	_, err = s.Upsert(ctx, wrapperspb.Bytes(changed))
	require.NoError(t, err)
	require.Len(t, s.records, 2)

	deletion, _ := json.Marshal(retrievalsdk.DeleteRequest{Kind: "source", IDs: []string{"s2"}})
	_, err = s.Delete(ctx, wrapperspb.Bytes(deletion))
	require.NoError(t, err)
	require.Len(t, s.records, 1)
}

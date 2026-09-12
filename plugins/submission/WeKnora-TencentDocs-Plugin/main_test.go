package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	pb "example.org/weknora-tencent-docs-public/proto"
	"example.org/weknora-tencent-docs-public/sdk/hosthttp"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protowire"
	"os"
	"testing"
)

type fakeHTTP struct {
	called   bool
	request  hosthttp.Request
	response *hosthttp.Response
	err      error
}

func (f *fakeHTTP) Do(_ context.Context, r hosthttp.Request) (*hosthttp.Response, error) {
	f.called = true
	f.request = r
	return f.response, f.err
}
func configRequest(link string) *pb.ConfigRequest {
	raw, _ := json.Marshal(map[string]any{"settings": map[string]string{"url": link}})
	return &pb.ConfigRequest{ConfigJson: raw}
}
func wireField(n protowire.Number, b []byte) []byte {
	return protowire.AppendBytes(protowire.AppendTag(nil, n, protowire.BytesType), b)
}
func snapshot(body string) []byte {
	return wireField(1, wireField(2, wireField(6, wireField(1, []byte(body)))))
}
func payload(title, body string) []byte {
	raw, _ := json.Marshal(map[string]any{"padType": "doc", "clientVars": map[string]any{"title": title, "collab_client_vars": map[string]any{"isChunked": false, "initialAttributedText": map[string]any{"text": []string{base64.StdEncoding.EncodeToString(snapshot(body))}}}}})
	return raw
}
func TestInvalidLinksNeverReachBroker(t *testing.T) {
	for _, link := range []string{"", "https://docs.qq.com/", "http://docs.qq.com/doc/a", "https://user:secret@docs.qq.com/doc/a", "https://docs.qq.com:8080/doc/a", "https://example.com/doc/a", "https://docs.qq.com/sheet/a"} {
		f := &fakeHTTP{}
		_, err := (&server{http: f}).Validate(context.Background(), configRequest(link))
		require.Error(t, err)
		require.False(t, f.called)
	}
}
func TestBodyAndIncremental(t *testing.T) {
	f := &fakeHTTP{response: &hosthttp.Response{StatusCode: 200, Body: payload("RAG", "正文\rRetriever\x0f\x1e")}}
	s := &server{http: f}
	ctx := context.Background()
	cfg := configRequest("https://docs.qq.com/doc/Example?no_promotion=1").ConfigJson
	resources, err := s.ListResources(ctx, &pb.ListResourcesRequest{ConfigJson: cfg})
	require.NoError(t, err)
	require.Equal(t, "RAG", resources.Resources[0].Name)
	require.Equal(t, "https://docs.qq.com/", f.request.Headers.Get("Referer"))
	items, cur, err := s.collect(ctx, &pb.FetchRequest{ConfigJson: cfg, ResourceIds: []string{"Example"}})
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, "正文\nRetriever", string(items[0].Content))
	items, _, err = s.collect(ctx, &pb.FetchRequest{ConfigJson: cfg, CursorJson: cur})
	require.NoError(t, err)
	require.Empty(t, items)
	f.response.Body = payload("RAG 修改标题", "新正文")
	items, _, err = s.collect(ctx, &pb.FetchRequest{ConfigJson: cfg, CursorJson: cur})
	require.NoError(t, err)
	require.Len(t, items, 1)
	items, _, err = s.collect(ctx, &pb.FetchRequest{ConfigJson: cfg, CursorJson: cur, Full: true})
	require.NoError(t, err)
	require.Len(t, items, 1)
	_, _, err = s.collect(ctx, &pb.FetchRequest{ConfigJson: cfg, ResourceIds: []string{"unknown"}})
	require.Error(t, err)
	f.response.StatusCode = 403
	items, next, err := s.collect(ctx, &pb.FetchRequest{ConfigJson: cfg, CursorJson: cur})
	require.ErrorContains(t, err, "HTTP 403")
	require.Nil(t, items)
	require.Nil(t, next)
}
func TestRejectIncompleteOrNonDocument(t *testing.T) {
	for _, body := range [][]byte{[]byte("<html>Login</html>"), []byte(`{"ret":401}`), payload("Empty", ""), []byte(`{"padType":"doc","clientVars":{"title":"Long","collab_client_vars":{"isChunked":true}}}`)} {
		_, err := decodeDocument(body, document{})
		require.Error(t, err)
	}
	_, err := snapshotText([]byte{0x0a, 0xff})
	require.Error(t, err)
}
func TestCapturedDocument(t *testing.T) {
	path := os.Getenv("WEKNORA_TENCENT_CAPTURE")
	if path == "" {
		t.Skip("optional local document fixture")
	}
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	doc, err := decodeDocument(raw, document{})
	require.NoError(t, err)
	require.Contains(t, doc.Text, "Retriever")
	require.Contains(t, doc.Text, "RAG")
	require.Contains(t, doc.Title, "RAG")
}

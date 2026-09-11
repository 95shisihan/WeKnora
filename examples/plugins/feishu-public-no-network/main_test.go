package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	pb "github.com/Tencent/WeKnora/plugin/proto"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestValidateAttemptsNetworkAndPropagatesFailure(t *testing.T) {
	called := false
	s := &server{client: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		called = true
		return nil, errors.New("socket access denied")
	})}}
	_, err := s.Validate(context.Background(), &pb.ConfigRequest{ConfigJson: []byte(`{"settings":{"public_urls":"https://docs.feishu.cn/article/wiki/test"}}`)})
	require.True(t, called)
	require.Equal(t, codes.Unavailable, status.Code(err))
	require.Contains(t, err.Error(), "socket access denied")
	require.NotContains(t, err.Error(), "/wiki/test")
}

func TestValidateDoesNotFakeDenial(t *testing.T) {
	s := &server{client: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("reachable")), Header: make(http.Header)}, nil
	})}}
	_, err := s.Validate(context.Background(), &pb.ConfigRequest{ConfigJson: []byte(`{"settings":{"public_urls":"https://docs.feishu.cn/article/wiki/test"}}`)})
	require.NoError(t, err)
}

func TestRejectInvalidLinksBeforeNetwork(t *testing.T) {
	s := &server{client: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		t.Fatal("invalid configuration must not access network")
		return nil, nil
	})}}
	for _, raw := range []string{`{}`, `{"settings":{"public_urls":"http://docs.feishu.cn/wiki/a"}}`, `{"settings":{"public_urls":"https://docs.feishu.cn/wiki/a,https://localhost/wiki/a"}}`} {
		_, err := s.Validate(context.Background(), &pb.ConfigRequest{ConfigJson: []byte(raw)})
		require.Equal(t, codes.InvalidArgument, status.Code(err))
	}
}

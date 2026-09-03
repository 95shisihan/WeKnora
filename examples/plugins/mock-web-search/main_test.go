package main

import (
	"context"
	"testing"

	pluginproto "github.com/Tencent/WeKnora/plugin/proto"
)

func TestSearchIsDeterministicAndHonorsLimit(t *testing.T) {
	service := server{}
	empty, err := service.Search(context.Background(), &pluginproto.WebSearchRequest{Query: "plugins", MaxResults: 0})
	if err != nil || len(empty.GetResults()) != 0 {
		t.Fatalf("zero-limit search = (%v, %v), want no results", empty, err)
	}

	response, err := service.Search(context.Background(), &pluginproto.WebSearchRequest{Query: "plugins", MaxResults: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.GetResults()) != 1 || response.GetResults()[0].GetTitle() != "Result for plugins" {
		t.Fatalf("unexpected response: %#v", response.GetResults())
	}
}

func TestGetInfoMatchesManifestDefaults(t *testing.T) {
	info, err := (server{}).GetInfo(context.Background(), &pluginproto.WebSearchInfoRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if info.GetId() != "io.weknora.mock-search" || info.GetProviderType() != "mock_search" || info.GetProtocolVersion() != "v1" {
		t.Fatalf("unexpected plugin identity: %#v", info)
	}
}

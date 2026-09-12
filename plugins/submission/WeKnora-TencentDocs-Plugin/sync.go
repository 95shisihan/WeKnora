package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"time"

	pb "example.org/weknora-tencent-docs-public/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func fail(err error) error { return status.Error(codes.FailedPrecondition, err.Error()) }
func (s *server) Validate(ctx context.Context, req *pb.ConfigRequest) (*pb.Empty, error) {
	_, docs, err := parseConfig(req.GetConfigJson())
	if err != nil {
		return nil, fail(err)
	}
	for _, d := range docs {
		if _, err = s.read(ctx, d); err != nil {
			return nil, fail(err)
		}
	}
	return &pb.Empty{}, nil
}
func (s *server) ListResources(ctx context.Context, req *pb.ListResourcesRequest) (*pb.ListResourcesResponse, error) {
	_, docs, err := parseConfig(req.GetConfigJson())
	if err != nil {
		return nil, fail(err)
	}
	result := &pb.ListResourcesResponse{}
	if req.ParentId != "" {
		for _, d := range docs {
			if d.ID == req.ParentId {
				return result, nil
			}
		}
		return nil, fail(fmt.Errorf("未知资源"))
	}
	for _, d := range docs {
		d, err = s.read(ctx, d)
		if err != nil {
			return nil, fail(err)
		}
		result.Resources = append(result.Resources, &pb.Resource{ExternalId: d.ID, Name: d.Title, Type: "document", Url: d.URL})
	}
	return result, nil
}
func (s *server) ResolveResourceAncestors(_ context.Context, req *pb.ResolveResourceAncestorsRequest) (*pb.ResolveResourceAncestorsResponse, error) {
	_, docs, err := parseConfig(req.ConfigJson)
	if err != nil {
		return nil, fail(err)
	}
	known := map[string]bool{}
	for _, d := range docs {
		known[d.ID] = true
	}
	result := &pb.ResolveResourceAncestorsResponse{}
	for _, id := range req.ResourceIds {
		if !known[id] {
			return nil, fail(fmt.Errorf("未知资源"))
		}
		result.Ancestors = append(result.Ancestors, &pb.Ancestors{ResourceId: id})
	}
	return result, nil
}

type cursorContents struct {
	Version int               `json:"version"`
	Scope   []string          `json:"scope"`
	Files   map[string]string `json:"files"`
}
type cursor struct {
	LastSyncTime time.Time      `json:"last_sync_time"`
	Contents     cursorContents `json:"connector_cursor"`
}

func (s *server) collect(ctx context.Context, req *pb.FetchRequest) ([]*pb.FetchedItem, []byte, error) {
	config, docs, err := parseConfig(req.ConfigJson)
	if err != nil {
		return nil, nil, err
	}
	known := map[string]document{}
	for _, d := range docs {
		known[d.ID] = d
	}
	selected := req.ResourceIds
	if len(selected) == 0 {
		selected = config.ResourceIDs
	}
	if len(selected) == 0 {
		for _, d := range docs {
			selected = append(selected, d.ID)
		}
	}
	scope := append([]string(nil), selected...)
	sort.Strings(scope)
	scope = slices.Compact(scope)
	for _, id := range scope {
		if _, ok := known[id]; !ok {
			return nil, nil, fmt.Errorf("选择的资源不在当前公开链接配置中")
		}
	}
	previous := cursor{}
	if !req.Full && len(req.CursorJson) > 0 && string(req.CursorJson) != "null" {
		if json.Unmarshal(req.CursorJson, &previous) != nil || previous.Contents.Version != 1 || previous.Contents.Files == nil {
			return nil, nil, fmt.Errorf("同步游标无效，请执行全量同步")
		}
		if !slices.Equal(scope, previous.Contents.Scope) {
			return nil, nil, fmt.Errorf("同步范围已变化，请执行全量同步")
		}
	}
	next := cursor{LastSyncTime: time.Now().UTC(), Contents: cursorContents{Version: 1, Scope: scope, Files: map[string]string{}}}
	var items []*pb.FetchedItem
	// Complete every read before emitting items or the final cursor. A revoked
	// link must fail the sync, not silently delete previously imported content.
	for _, id := range scope {
		d, err := s.read(ctx, known[id])
		if err != nil {
			return nil, nil, err
		}
		digest := sha256.Sum256([]byte(d.Title + "\x00" + d.Text))
		hash := hex.EncodeToString(digest[:])
		next.Contents.Files[id] = hash
		if previous.Contents.Files[id] == hash {
			continue
		}
		items = append(items, &pb.FetchedItem{ExternalId: id, SourceResourceId: id, Title: d.Title, Content: []byte(d.Text), ContentType: "text/plain", FileName: id + ".txt", Url: d.URL, Metadata: map[string]string{"sha256": hash, "source": "tencent_docs_public"}})
	}
	raw, err := json.Marshal(next)
	return items, raw, err
}
func (s *server) Fetch(req *pb.FetchRequest, stream grpc.ServerStreamingServer[pb.FetchEvent]) error {
	items, raw, err := s.collect(stream.Context(), req)
	if err != nil {
		return fail(err)
	}
	for _, item := range items {
		if err := stream.Send(&pb.FetchEvent{Payload: &pb.FetchEvent_Item{Item: item}}); err != nil {
			return err
		}
	}
	return stream.Send(&pb.FetchEvent{Payload: &pb.FetchEvent_FinalCursorJson{FinalCursorJson: raw}})
}

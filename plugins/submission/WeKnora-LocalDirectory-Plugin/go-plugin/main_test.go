package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"

	pluginproto "example.org/weknora-local-directory/proto"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"
)

type recordingFetchStream struct {
	ctx    context.Context
	events []*pluginproto.FetchEvent
}

func (s *recordingFetchStream) Send(event *pluginproto.FetchEvent) error {
	s.events = append(s.events, event)
	return nil
}
func (*recordingFetchStream) SetHeader(metadata.MD) error  { return nil }
func (*recordingFetchStream) SendHeader(metadata.MD) error { return nil }
func (*recordingFetchStream) SetTrailer(metadata.MD)       {}
func (*recordingFetchStream) SendMsg(any) error            { return nil }
func (*recordingFetchStream) RecvMsg(any) error            { return nil }
func (s *recordingFetchStream) Context() context.Context   { return s.ctx }

func TestIncrementalFetchEmitsOnlyChangedFile(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "one.md"), []byte("one"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "two.md"), []byte("two"), 0o600))
	config, err := json.Marshal(dataSourceConfig{Settings: map[string]any{"root": root}})
	require.NoError(t, err)

	service := &server{}
	first := &recordingFetchStream{ctx: context.Background()}
	require.NoError(t, service.Fetch(&pluginproto.FetchRequest{ConfigJson: config, Full: true}, first))
	require.Len(t, fetchedItems(first.events), 2)
	cursor := finalCursor(t, first.events)

	require.NoError(t, os.WriteFile(filepath.Join(root, "two.md"), []byte("two changed"), 0o600))
	second := &recordingFetchStream{ctx: context.Background()}
	require.NoError(t, service.Fetch(&pluginproto.FetchRequest{ConfigJson: config, CursorJson: cursor}, second))
	items := fetchedItems(second.events)
	require.Len(t, items, 1)
	require.Equal(t, "two.md", items[0].GetExternalId())
	require.Equal(t, []byte("two changed"), items[0].GetContent())
}

func TestIncrementalFetchEmitsDeletion(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "gone.txt")
	require.NoError(t, os.WriteFile(path, []byte("gone"), 0o600))
	config, err := json.Marshal(dataSourceConfig{Settings: map[string]any{"root": root}})
	require.NoError(t, err)

	service := &server{}
	first := &recordingFetchStream{ctx: context.Background()}
	require.NoError(t, service.Fetch(&pluginproto.FetchRequest{ConfigJson: config, Full: true}, first))
	require.NoError(t, os.Remove(path))

	second := &recordingFetchStream{ctx: context.Background()}
	require.NoError(t, service.Fetch(&pluginproto.FetchRequest{ConfigJson: config, CursorJson: finalCursor(t, first.events)}, second))
	items := fetchedItems(second.events)
	require.Len(t, items, 1)
	require.True(t, items[0].GetIsDeleted())
	require.Equal(t, "gone.txt", items[0].GetExternalId())
}

type interruptedFetchStream struct {
	recordingFetchStream
	checkpoint []byte
	fail       func(*pluginproto.FetchEvent) bool
}

func (s *interruptedFetchStream) Send(event *pluginproto.FetchEvent) error {
	if s.fail(event) {
		return io.ErrUnexpectedEOF
	}
	if raw := event.GetCheckpointCursorJson(); len(raw) > 0 {
		s.checkpoint = append([]byte(nil), raw...)
	}
	return s.recordingFetchStream.Send(event)
}

func TestIncrementalFetchResumesCheckpointWithoutLosingHistory(t *testing.T) {
	for _, interruption := range []string{"after_checkpoint", "during_deletion", "before_final_cursor"} {
		t.Run(interruption, func(t *testing.T) {
			root := t.TempDir()
			for i := 1; i <= 200; i++ {
				require.NoError(t, os.WriteFile(filepath.Join(root, fmt.Sprintf("%03d.txt", i)), []byte(fmt.Sprintf("file %d", i)), 0o600))
			}
			deletedPath := filepath.Join(root, "gone.txt")
			require.NoError(t, os.WriteFile(deletedPath, []byte("gone"), 0o600))
			config, err := json.Marshal(dataSourceConfig{Settings: map[string]any{"root": root}})
			require.NoError(t, err)
			service := &server{}
			initial := &recordingFetchStream{ctx: context.Background()}
			require.NoError(t, service.Fetch(&pluginproto.FetchRequest{ConfigJson: config, Full: true}, initial))
			initialCursor := finalCursor(t, initial.events)
			// The 100th entry emits a checkpoint; the later update must retain
			// its old hash until sent, and the deletion must remain discoverable.
			for _, name := range []string{"100.txt", "150.txt"} {
				require.NoError(t, os.WriteFile(filepath.Join(root, name), []byte("changed "+name), 0o600))
			}
			require.NoError(t, os.Remove(deletedPath))
			broken := &interruptedFetchStream{recordingFetchStream: recordingFetchStream{ctx: context.Background()}}
			broken.fail = func(event *pluginproto.FetchEvent) bool {
				switch interruption {
				case "after_checkpoint":
					return len(broken.checkpoint) > 0
				case "during_deletion":
					return event.GetItem() != nil && event.GetItem().GetIsDeleted()
				default:
					return len(event.GetFinalCursorJson()) > 0
				}
			}
			err = service.Fetch(&pluginproto.FetchRequest{ConfigJson: config, CursorJson: initialCursor}, broken)
			require.ErrorIs(t, err, io.ErrUnexpectedEOF)
			require.NotEmpty(t, broken.checkpoint)

			resumed := &recordingFetchStream{ctx: context.Background()}
			require.NoError(t, service.Fetch(&pluginproto.FetchRequest{ConfigJson: config, CursorJson: broken.checkpoint}, resumed))
			items := fetchedItems(resumed.events)
			// Only the update after the checkpoint and the pending deletion
			// may replay. None of the unchanged files should be sent again.
			require.Equal(t, 2, len(items))
			require.Equal(t, "150.txt", items[0].GetExternalId())
			require.Equal(t, []byte("changed 150.txt"), items[0].GetContent())
			require.Equal(t, "gone.txt", items[1].GetExternalId())
			require.True(t, items[1].GetIsDeleted())

			settled := &recordingFetchStream{ctx: context.Background()}
			require.NoError(t, service.Fetch(&pluginproto.FetchRequest{ConfigJson: config, CursorJson: finalCursor(t, resumed.events)}, settled))
			require.Empty(t, fetchedItems(settled.events))
		})
	}
}

func TestGRPCRoundTrip(t *testing.T) {
	listener := bufconn.Listen(1024 * 1024)
	grpcServer := grpc.NewServer()
	pluginproto.RegisterDatasourcePluginServer(grpcServer, &server{pluginID: "io.test", version: "1.2.3"})
	healthServer := health.NewServer()
	healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)
	grpc_health_v1.RegisterHealthServer(grpcServer, healthServer)
	go func() { _ = grpcServer.Serve(listener) }()
	t.Cleanup(grpcServer.Stop)

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	_, err = grpc_health_v1.NewHealthClient(conn).Check(context.Background(), &grpc_health_v1.HealthCheckRequest{})
	require.NoError(t, err)
	info, err := pluginproto.NewDatasourcePluginClient(conn).GetInfo(context.Background(), &pluginproto.GetInfoRequest{})
	require.NoError(t, err)
	require.Equal(t, "io.test", info.GetId())
	require.Equal(t, connectorType, info.GetConnectorType())

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "one.txt"), []byte("one"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "two.txt"), []byte("two"), 0o600))
	config, err := json.Marshal(dataSourceConfig{Settings: map[string]any{"root": root}})
	require.NoError(t, err)
	client := pluginproto.NewDatasourcePluginClient(conn)
	first := receiveFetchEvents(t, client, &pluginproto.FetchRequest{ConfigJson: config, Full: true})
	require.Len(t, fetchedItems(first), 2)

	require.NoError(t, os.WriteFile(filepath.Join(root, "two.txt"), []byte("changed"), 0o600))
	second := receiveFetchEvents(t, client, &pluginproto.FetchRequest{
		ConfigJson: config, CursorJson: finalCursor(t, first),
	})
	require.Len(t, fetchedItems(second), 1)
	require.Equal(t, "two.txt", fetchedItems(second)[0].GetExternalId())
}

func receiveFetchEvents(t *testing.T, client pluginproto.DatasourcePluginClient, request *pluginproto.FetchRequest) []*pluginproto.FetchEvent {
	t.Helper()
	stream, err := client.Fetch(context.Background(), request)
	require.NoError(t, err)
	var events []*pluginproto.FetchEvent
	for {
		event, err := stream.Recv()
		if err == io.EOF {
			return events
		}
		require.NoError(t, err)
		events = append(events, event)
	}
}

func fetchedItems(events []*pluginproto.FetchEvent) []*pluginproto.FetchedItem {
	var result []*pluginproto.FetchedItem
	for _, event := range events {
		if item := event.GetItem(); item != nil {
			result = append(result, item)
		}
	}
	return result
}

func finalCursor(t *testing.T, events []*pluginproto.FetchEvent) []byte {
	t.Helper()
	for index := len(events) - 1; index >= 0; index-- {
		if raw := events[index].GetFinalCursorJson(); len(raw) > 0 {
			return raw
		}
	}
	t.Fatal("final cursor not emitted")
	return nil
}

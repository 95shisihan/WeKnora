package transport_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/plugin/sdk/hosthttp"
	"github.com/Tencent/WeKnora/plugin/sdk/transport"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func TestPythonStdioGRPC(t *testing.T) {
	python := os.Getenv("WEKNORA_PYTHON")
	if python == "" {
		t.Skip("set WEKNORA_PYTHON to an interpreter with SDK dependencies")
	}
	helper, err := filepath.Abs("../python/stdio_test_server.py")
	require.NoError(t, err)
	cmd := exec.Command(python, helper)
	cmd.Stderr = os.Stderr
	reader, err := cmd.StdoutPipe()
	require.NoError(t, err)
	writer, err := cmd.StdinPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())
	defer func() { cmd.Process.Kill(); cmd.Wait() }()
	address := "passthrough:///python-stdio-test"
	unregister := transport.Register(address, &transport.PipeConn{Reader: reader, Writer: writer}, nil)
	defer unregister()
	conn, err := grpc.NewClient(address, append(transport.Options(address), grpc.WithTransportCredentials(insecure.NewCredentials()))...)
	require.NoError(t, err)
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_, err = grpc_health_v1.NewHealthClient(conn).Check(ctx, &grpc_health_v1.HealthCheckRequest{})
	require.NoError(t, err)
	payload := bytes.Repeat([]byte{0, 10, 13, 26, 255}, 200000)
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var result wrapperspb.BytesValue
			err := conn.Invoke(ctx, "/test.Pipe/Echo", wrapperspb.Bytes(payload), &result)
			if err != nil {
				t.Error(err)
				return
			}
			if !bytes.Equal(payload, result.Value) {
				t.Error("binary pipe data changed")
			}
		}()
	}
	wg.Wait()
	short, stop := context.WithTimeout(ctx, 50*time.Millisecond)
	defer stop()
	var result wrapperspb.BytesValue
	require.Error(t, conn.Invoke(short, "/test.Pipe/Slow", wrapperspb.Bytes(nil), &result))
	stream, err := conn.NewStream(ctx, &grpc.StreamDesc{ClientStreams: true, ServerStreams: true}, hosthttp.Method)
	require.NoError(t, err)
	ready, err := hosthttp.Receive(stream)
	require.NoError(t, err)
	require.True(t, ready.Ready)
	done := make(chan error, 1)
	go func() { done <- conn.Invoke(ctx, "/test.Pipe/HTTP", wrapperspb.String("https://example.com"), &result) }()
	request, err := hosthttp.Receive(stream)
	require.NoError(t, err)
	require.Equal(t, "https://example.com", request.Request.URL)
	require.NoError(t, hosthttp.Send(stream, hosthttp.Frame{ID: request.ID, Response: &hosthttp.Response{StatusCode: 200, Body: payload}}))
	require.NoError(t, <-done)
	require.Equal(t, payload, result.Value)
	_, err = grpc_health_v1.NewHealthClient(conn).Check(ctx, &grpc_health_v1.HealthCheckRequest{})
	require.NoError(t, err)
	t.Log("Python stdio: concurrent 1 MB binary RPCs, flow control, cancellation, health and reverse HTTP passed")
}

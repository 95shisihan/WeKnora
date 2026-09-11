//go:build windows && (amd64 || arm64)

package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/plugin/windowsandbox"
	pb "github.com/Tencent/WeKnora/plugin/proto"
	"github.com/Tencent/WeKnora/plugin/sdk/transport"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

func TestNativeFeishuConnectionDenied(t *testing.T) {
	source, link := os.Getenv("WEKNORA_FEISHU_DENIED_EXE"), os.Getenv("WEKNORA_FEISHU_TEST_URL")
	if source == "" || link == "" || os.Getenv("WEKNORA_WINDOWS_SANDBOX_TEST") != "1" {
		t.Skip("requires built executable, public URL and Windows sandbox opt-in")
	}
	raw, err := json.Marshal(map[string]any{"settings": map[string]any{"public_urls": link}})
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	// The same real request must succeed without the OS restriction first.
	_, err = (&server{client: newClient()}).Validate(ctx, &pb.ConfigRequest{ConfigJson: raw})
	require.NoError(t, err, "unrestricted positive control must be reachable")
	t.Log("Unrestricted control: same public Feishu URL is reachable")
	dir := t.TempDir()
	exe := filepath.Join(dir, "feishu.exe")
	binary, err := os.ReadFile(source)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(exe, binary, 0700))
	p, err := windowsandbox.Start(windowsandbox.Config{Command: []string{exe}, Directory: dir, Stderr: os.Stderr,
		Environment: append(os.Environ(), "WEKNORA_PLUGIN_ADDRESS=stdio://")})
	require.NoError(t, err)
	defer p.Close()
	address := "passthrough:///feishu-denied-test"
	unregister := transport.Register(address, p.Conn, p.PrepareConfig)
	defer unregister()
	conn, err := grpc.NewClient(address, append(transport.Options(address), grpc.WithTransportCredentials(insecure.NewCredentials()))...)
	require.NoError(t, err)
	defer conn.Close()
	h, err := grpc_health_v1.NewHealthClient(conn).Check(ctx, &grpc_health_v1.HealthCheckRequest{})
	require.NoError(t, err)
	require.Equal(t, grpc_health_v1.HealthCheckResponse_SERVING, h.GetStatus())
	client := pb.NewDatasourcePluginClient(conn)
	info, err := client.GetInfo(ctx, &pb.GetInfoRequest{})
	require.NoError(t, err)
	require.Equal(t, pluginID, info.GetId())
	_, err = client.Validate(ctx, &pb.ConfigRequest{ConfigJson: raw})
	require.Equal(t, codes.Unavailable, status.Code(err))
	message := strings.ToLower(err.Error())
	dnsDenied := strings.Contains(message, "lookup docs.feishu.cn: no such host")
	require.True(t, dnsDenied || strings.Contains(message, "forbidden by its access permissions") || strings.Contains(message, "permission denied") || strings.Contains(message, "access is denied") || strings.Contains(message, "10013"), "expected restricted DNS/socket failure, not generic HTTP failure: %v", err)
	if dnsDenied {
		t.Log("Restricted Windows resolver failed before TCP; this is a paired reachability result, not an explicit Winsock denial code")
	}
	t.Logf("Restricted production EXE: Health=SERVING; Validate=%v", err)
	_, err = grpc_health_v1.NewHealthClient(conn).Check(ctx, &grpc_health_v1.HealthCheckRequest{})
	require.NoError(t, err, "network denial must not break control channel")
	_, err = (&server{client: newClient()}).Validate(ctx, &pb.ConfigRequest{ConfigJson: raw})
	require.NoError(t, err, "unrestricted control must remain reachable after restricted failure")
	t.Log("Unrestricted control remains reachable after restricted failure")
	require.NoError(t, p.Close())
}

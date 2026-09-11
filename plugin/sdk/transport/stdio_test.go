package transport

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
)

func TestPipeGRPCHealthAndCancellation(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	listener := &pipeListener{conn: serverConn, done: make(chan struct{})}
	server := grpc.NewServer()
	service := health.NewServer()
	service.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)
	grpc_health_v1.RegisterHealthServer(server, service)
	defer server.Stop()
	go func() { _ = server.Serve(listener) }()
	address := "passthrough:///transport-health-test"
	remove := Register(address, clientConn, nil)
	defer remove()
	client, err := grpc.NewClient(address, append(Options(address), grpc.WithTransportCredentials(insecure.NewCredentials()))...)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	response, err := grpc_health_v1.NewHealthClient(client).Check(ctx, &grpc_health_v1.HealthCheckRequest{})
	if err != nil || response.GetStatus() != grpc_health_v1.HealthCheckResponse_SERVING {
		t.Fatal(response, err)
	}
	watchCtx, stopWatch := context.WithCancel(ctx)
	stream, err := grpc_health_v1.NewHealthClient(client).Watch(watchCtx, &grpc_health_v1.HealthCheckRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Recv(); err != nil {
		t.Fatal(err)
	}
	stopWatch()
	if _, err := stream.Recv(); err == nil {
		t.Fatal("cancelled stream remained usable")
	}
	remove()
	if len(Options(address)) != 0 {
		t.Fatal("transport registration leaked")
	}
}

func TestPipeListenerCloseUnblocksAccept(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()
	l := &pipeListener{conn: a, done: make(chan struct{})}
	if _, err := l.Accept(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := l.Accept(); done <- err }()
	_ = l.Close()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("closed listener accepted")
		}
	case <-time.After(time.Second):
		t.Fatal("Accept did not unblock")
	}
}

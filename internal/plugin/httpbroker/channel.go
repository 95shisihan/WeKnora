package httpbroker

import (
	"context"
	"fmt"
	"sync"

	"github.com/Tencent/WeKnora/plugin/sdk/hosthttp"
	"google.golang.org/grpc"
)

// Open binds the executor to a host-owned connection. The peer never supplies
// an identity or policy. Channel lifetime is bounded by runtime and connection.
func (e *Executor) Open(ctx context.Context, conn *grpc.ClientConn) error {
	stream, err := conn.NewStream(ctx, &grpc.StreamDesc{ServerStreams: true, ClientStreams: true}, hosthttp.Method, grpc.MaxCallRecvMsgSize(hosthttp.MaxWireBytes), grpc.MaxCallSendMsgSize(hosthttp.MaxWireBytes))
	if err != nil {
		return err
	}
	ready, err := hosthttp.Receive(stream)
	if err != nil {
		return err
	}
	if !ready.Ready {
		return fmt.Errorf("missing HTTP channel handshake")
	}
	go e.serve(stream)
	return nil
}
func (e *Executor) serve(stream grpc.ClientStream) {
	ctx, cancel := context.WithCancel(stream.Context())
	defer cancel()
	var mu, send sync.Mutex
	active := make(map[uint64]context.CancelFunc)
	defer func() {
		mu.Lock()
		for _, stop := range active {
			stop()
		}
		mu.Unlock()
		_ = stream.CloseSend()
	}()
	sendResponse := func(id uint64, r *hosthttp.Response) {
		send.Lock()
		err := hosthttp.Send(stream, hosthttp.Frame{ID: id, Response: r})
		send.Unlock()
		if err != nil {
			cancel()
		}
	}
	for {
		f, err := hosthttp.Receive(stream)
		if err != nil {
			return
		}
		if f.ID == 0 {
			return
		}
		mu.Lock()
		if f.Cancel {
			if stop := active[f.ID]; stop != nil {
				stop()
			}
			mu.Unlock()
			continue
		}
		if f.Request == nil || f.Response != nil || f.Ready {
			mu.Unlock()
			return
		}
		if _, exists := active[f.ID]; exists {
			mu.Unlock()
			return
		}
		if len(active) >= 4 {
			mu.Unlock()
			sendResponse(f.ID, failure("BUSY", "plugin HTTP concurrency limit reached"))
			continue
		}
		requestCtx, stop := context.WithCancel(ctx)
		active[f.ID] = stop
		mu.Unlock()
		go func(f hosthttp.Frame) {
			response := e.Do(requestCtx, *f.Request)
			stop()
			// Keep the slot until its response is sent to bound blocked writes.
			sendResponse(f.ID, response)
			mu.Lock()
			delete(active, f.ID)
			mu.Unlock()
		}(f)
	}
}

package hosthttp

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// Client is registered on the plugin's existing gRPC server before Serve.
// The host opens the reverse stream; calls wait for it subject to ctx.
type Client struct {
	mu      sync.Mutex
	session *session
	ready   chan struct{}
	next    atomic.Uint64
}
type session struct {
	stream  grpc.ServerStream
	send    sync.Mutex
	pending map[uint64]chan *Response // protected by Client.mu
}
type channelServer interface{ open(grpc.ServerStream) error }

func Register(s grpc.ServiceRegistrar) *Client {
	c := &Client{ready: make(chan struct{})}
	s.RegisterService(&grpc.ServiceDesc{ServiceName: "weknora.hosthttp.v1.Channel", HandlerType: (*channelServer)(nil), Streams: []grpc.StreamDesc{{StreamName: "Open", ServerStreams: true, ClientStreams: true, Handler: func(s any, stream grpc.ServerStream) error { return s.(channelServer).open(stream) }}}}, c)
	return c
}
func Send(stream interface{ SendMsg(any) error }, f Frame) error {
	raw, err := json.Marshal(f)
	if err != nil {
		return err
	}
	if len(raw) > MaxWireBytes {
		return errors.New("host HTTP frame too large")
	}
	return stream.SendMsg(wrapperspb.Bytes(raw))
}
func Receive(stream interface{ RecvMsg(any) error }) (Frame, error) {
	var raw wrapperspb.BytesValue
	if err := stream.RecvMsg(&raw); err != nil {
		return Frame{}, err
	}
	if len(raw.Value) > MaxWireBytes {
		return Frame{}, errors.New("host HTTP frame too large")
	}
	var f Frame
	err := json.Unmarshal(raw.Value, &f)
	return f, err
}
func (c *Client) open(stream grpc.ServerStream) error {
	s := &session{stream: stream, pending: make(map[uint64]chan *Response)}
	c.mu.Lock()
	if c.session != nil {
		c.mu.Unlock()
		return status.Error(codes.AlreadyExists, "HTTP channel already open")
	}
	c.session = s
	close(c.ready)
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.session = nil
		c.ready = make(chan struct{})
		for _, ch := range s.pending {
			ch <- &Response{Error: &Error{Code: "CHANNEL_CLOSED", Message: "host HTTP channel closed"}}
		}
		c.mu.Unlock()
	}()
	s.send.Lock()
	err := Send(stream, Frame{Ready: true})
	s.send.Unlock()
	if err != nil {
		return err
	}
	for {
		f, err := Receive(stream)
		if err != nil {
			return err
		}
		if f.Response == nil || f.ID == 0 {
			return status.Error(codes.InvalidArgument, "invalid HTTP response")
		}
		c.mu.Lock()
		if ch, ok := s.pending[f.ID]; ok {
			delete(s.pending, f.ID)
			ch <- f.Response
		}
		c.mu.Unlock()
	}
}
func (c *Client) Do(ctx context.Context, req Request) (*Response, error) {
	if len(req.Body) > MaxBodyBytes {
		return nil, &Error{Code: "REQUEST_TOO_LARGE", Message: "request body exceeds SDK limit"}
	}
	for {
		c.mu.Lock()
		s := c.session
		ready := c.ready
		if s == nil {
			c.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-ready:
				continue
			}
		}
		if len(s.pending) >= 4 {
			c.mu.Unlock()
			return nil, &Error{Code: "BUSY", Message: "at most four HTTP requests may be active"}
		}
		id := c.next.Add(1)
		ch := make(chan *Response, 1)
		s.pending[id] = ch
		c.mu.Unlock()
		defer func() { c.mu.Lock(); delete(s.pending, id); c.mu.Unlock() }()
		s.send.Lock()
		err := Send(s.stream, Frame{ID: id, Request: &req})
		s.send.Unlock()
		if err != nil {
			return nil, err
		}
		select {
		case response := <-ch:
			if response.Error != nil {
				return nil, response.Error
			}
			return response, nil
		case <-ctx.Done():
			s.send.Lock()
			_ = Send(s.stream, Frame{ID: id, Cancel: true})
			s.send.Unlock()
			return nil, ctx.Err()
		}
	}
}

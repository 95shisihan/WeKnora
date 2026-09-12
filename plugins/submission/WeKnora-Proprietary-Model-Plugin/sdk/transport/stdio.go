// Package transport supplies local IPC for plugins that have no IP networking.
// In stdio mode stdout is exclusively the gRPC byte stream; log to stderr.
package transport

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
)

type pipeAddr string

// Listen selects the transport requested by the host. Existing gRPC services
// only replace net.Listen with this function; their service APIs do not change.
func Listen(address string) (net.Listener, error) {
	if address == "stdio://" {
		return StdioListener(), nil
	}
	if path, ok := strings.CutPrefix(address, "unix://"); ok {
		if path == "" {
			return nil, fmt.Errorf("empty Unix socket path")
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		return net.Listen("unix", path)
	}
	return net.Listen("tcp", address)
}

func (a pipeAddr) Network() string { return "stdio" }
func (a pipeAddr) String() string  { return string(a) }

type PipeConn struct {
	Reader io.ReadCloser
	Writer io.WriteCloser
	once   sync.Once
}

func (c *PipeConn) Read(p []byte) (int, error)  { return c.Reader.Read(p) }
func (c *PipeConn) Write(p []byte) (int, error) { return c.Writer.Write(p) }
func (c *PipeConn) Close() error {
	c.once.Do(func() { _ = c.Reader.Close(); _ = c.Writer.Close() })
	return nil
}
func (*PipeConn) LocalAddr() net.Addr  { return pipeAddr("local") }
func (*PipeConn) RemoteAddr() net.Addr { return pipeAddr("plugin") }

// gRPC owns cancellation and closes the transport on connection failure.
func (*PipeConn) SetDeadline(time.Time) error      { return nil }
func (*PipeConn) SetReadDeadline(time.Time) error  { return nil }
func (*PipeConn) SetWriteDeadline(time.Time) error { return nil }

type pipeListener struct {
	conn     net.Conn
	mu       sync.Mutex
	accepted bool
	done     chan struct{}
	once     sync.Once
}

func StdioListener() net.Listener {
	return &pipeListener{conn: &PipeConn{Reader: os.Stdin, Writer: os.Stdout}, done: make(chan struct{})}
}
func (l *pipeListener) Accept() (net.Conn, error) {
	l.mu.Lock()
	select {
	case <-l.done:
		l.mu.Unlock()
		return nil, net.ErrClosed
	default:
	}
	if !l.accepted {
		l.accepted = true
		l.mu.Unlock()
		return l.conn, nil
	}
	l.mu.Unlock()
	<-l.done
	return nil, net.ErrClosed
}
func (l *pipeListener) Close() error {
	l.once.Do(func() { close(l.done); _ = l.conn.Close() })
	return nil
}
func (*pipeListener) Addr() net.Addr { return pipeAddr("stdio://") }

type endpoint struct {
	conn    net.Conn
	prepare func([]byte) error
	used    bool
	mu      sync.Mutex
}

var endpoints sync.Map

// Register is called only by the host after an OS-enforced process was started.
func Register(address string, conn net.Conn, prepare func([]byte) error) func() {
	entry := &endpoint{conn: conn, prepare: prepare}
	endpoints.Store(address, entry)
	return func() { endpoints.CompareAndDelete(address, entry); _ = conn.Close() }
}

// Options retains the ordinary TCP/Unix gRPC path for existing plugins.
func Options(address string) []grpc.DialOption {
	value, ok := endpoints.Load(address)
	if !ok {
		return nil
	}
	e := value.(*endpoint)
	prepare := func(message any) error {
		if request, ok := message.(interface{ GetConfigJson() []byte }); ok && e.prepare != nil {
			return e.prepare(request.GetConfigJson())
		}
		return nil
	}
	return []grpc.DialOption{
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			e.mu.Lock()
			defer e.mu.Unlock()
			if e.used {
				return nil, fmt.Errorf("plugin stdio process must be restarted")
			}
			e.used = true
			return e.conn, nil
		}),
		grpc.WithChainUnaryInterceptor(func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoke grpc.UnaryInvoker, opts ...grpc.CallOption) error {
			if err := prepare(req); err != nil {
				return err
			}
			return invoke(ctx, method, req, reply, cc, opts...)
		}),
		grpc.WithChainStreamInterceptor(func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
			ctx, cancel := context.WithCancel(ctx)
			stream, err := streamer(ctx, desc, cc, method, opts...)
			if err != nil {
				cancel()
				return nil, err
			}
			return &preparedStream{ClientStream: stream, prepare: prepare, cancel: cancel}, nil
		}),
	}
}

type preparedStream struct {
	grpc.ClientStream
	prepare func(any) error
	cancel  context.CancelFunc
}

func (s *preparedStream) SendMsg(message any) error {
	if err := s.prepare(message); err != nil {
		s.cancel()
		return err
	}
	return s.ClientStream.SendMsg(message)
}

func (s *preparedStream) RecvMsg(message any) error {
	err := s.ClientStream.RecvMsg(message)
	if err != nil {
		s.cancel()
	}
	return err
}

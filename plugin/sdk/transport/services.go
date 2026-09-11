package transport

import (
	"context"
	"sync"

	"google.golang.org/grpc"
)

var hostServices sync.Map

// RegisterHostService is host-only. It binds a reverse service to the runtime
// address established by the host, not to plugin-supplied identity fields.
func RegisterHostService(address string, open func(context.Context, *grpc.ClientConn) error) func() {
	hostServices.Store(address, open)
	return func() { hostServices.Delete(address) }
}

// AttachHostServices must run after NewClient and before business RPCs.
func AttachHostServices(ctx context.Context, address string, conn *grpc.ClientConn) error {
	if open, ok := hostServices.Load(address); ok {
		return open.(func(context.Context, *grpc.ClientConn) error)(ctx, conn)
	}
	return nil
}

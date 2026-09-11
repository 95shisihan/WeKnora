package httpbroker

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/plugin/sdk/hosthttp"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

func TestReverseChannelRoundTripAndCancellation(t *testing.T) {
	e, _ := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/wait" {
			<-r.Context().Done()
			return
		}
		w.Write([]byte("via host"))
	})
	l := bufconn.Listen(4 << 20)
	g := grpc.NewServer()
	sdk := hosthttp.Register(g)
	go g.Serve(l)
	t.Cleanup(g.Stop)
	conn, err := grpc.NewClient("passthrough:///test", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return l.Dial() }))
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	require.NoError(t, e.Open(ctx, conn))
	call, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	r, err := sdk.Do(call, hosthttp.Request{URL: "https://example.com/"})
	require.NoError(t, err)
	require.Equal(t, "via host", string(r.Body))
	_, err = sdk.Do(call, hosthttp.Request{URL: "https://unauthorized.example.org/"})
	var denied *hosthttp.Error
	require.ErrorAs(t, err, &denied)
	require.Equal(t, "DOMAIN_NOT_ALLOWED", denied.Code)
	short, end := context.WithTimeout(ctx, 100*time.Millisecond)
	defer end()
	_, err = sdk.Do(short, hosthttp.Request{URL: "https://example.com/wait"})
	require.Error(t, err)
	r, err = sdk.Do(call, hosthttp.Request{URL: "https://example.com/"})
	require.NoError(t, err)
	require.Equal(t, 200, r.StatusCode)
	stop()
}

func TestSDKWithoutHostHonorsDeadline(t *testing.T) {
	g := grpc.NewServer()
	sdk := hosthttp.Register(g)
	defer g.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := sdk.Do(ctx, hosthttp.Request{URL: "https://example.com"})
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

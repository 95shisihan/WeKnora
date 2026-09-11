package httpbroker

import (
	"context"
	"crypto/x509"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/plugin/sdk/hosthttp"
	"github.com/stretchr/testify/require"
)

type lookup func(context.Context, string, string) ([]netip.Addr, error)

func TestSameOriginRootReferer(t *testing.T) {
	var got string
	e, count := fixture(t, func(w http.ResponseWriter, r *http.Request) { got = r.Header.Get("Referer"); w.Write([]byte("ok")) })
	for _, ref := range []string{"https://other.example.com/", "https://example.com/private", "https://example.com/?token=x", "https://user:pass@example.com/", "http://example.com/", "https://example.com/#secret"} {
		result := e.Do(context.Background(), hosthttp.Request{URL: "https://example.com/", Headers: http.Header{"Referer": {ref}}})
		require.Equal(t, "HEADERS_DENIED", result.Error.Code)
	}
	require.Zero(t, count.Load())
	result := e.Do(context.Background(), hosthttp.Request{URL: "https://example.com/", Headers: http.Header{"Referer": {"https://example.com/"}}})
	require.Nil(t, result.Error)
	require.Equal(t, "https://example.com/", got)
}

func TestRefererRemovedAcrossOrigins(t *testing.T) {
	var leaked string
	e, _ := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			w.Header().Set("Location", "https://other.example.com/end")
			w.WriteHeader(302)
			return
		}
		leaked = r.Header.Get("Referer")
		w.Write([]byte("ok"))
	})
	result := e.Do(context.Background(), hosthttp.Request{URL: "https://example.com/start", Headers: http.Header{"Referer": {"https://example.com/"}}})
	require.Nil(t, result.Error)
	require.Empty(t, leaked)
}

func (f lookup) LookupNetIP(c context.Context, n, h string) ([]netip.Addr, error) { return f(c, n, h) }
func fixture(t *testing.T, handler http.HandlerFunc) (*Executor, *atomic.Int32) {
	t.Helper()
	s := httptest.NewTLSServer(handler)
	t.Cleanup(s.Close)
	e, err := New(hosthttp.Policy{Rules: []hosthttp.Rule{{Hosts: []string{"example.com", "other.example.com"}, Methods: []string{"GET", "POST"}}}, MaxRedirects: 3})
	require.NoError(t, err)
	e.rootCAs = x509.NewCertPool()
	e.rootCAs.AddCert(s.Certificate())
	e.resolver = lookup(func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
	})
	count := &atomic.Int32{}
	e.dial = func(ctx context.Context, network, address string) (net.Conn, error) {
		count.Add(1)
		require.Equal(t, "93.184.216.34:443", address, "connect must use validated numeric IP")
		return (&net.Dialer{}).DialContext(ctx, network, s.Listener.Addr().String())
	}
	return e, count
}
func TestIPPolicy(t *testing.T) {
	for _, raw := range []string{"127.0.0.1", "10.1.2.3", "172.16.0.1", "192.168.1.1", "169.254.169.254", "100.100.100.200", "0.0.0.0", "224.0.0.1", "198.18.0.1", "::1", "::ffff:127.0.0.1", "fe80::1", "fc00::1", "64:ff9b::a00:1", "2002:7f00:1::", "2001:db8::1"} {
		require.False(t, publicIP(netip.MustParseAddr(raw)), raw)
	}
	for _, raw := range []string{"8.8.8.8", "2606:4700:4700::1111"} {
		require.True(t, publicIP(netip.MustParseAddr(raw)), raw)
	}
}
func TestBlockedRequestsNeverDial(t *testing.T) {
	e, count := fixture(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	for _, raw := range []string{"http://example.com", "https://example.com:444/", "https://example.com.evil.test/", "https://user@example.com/", "https://127.0.0.1/", "https://example.com./"} {
		r := e.Do(context.Background(), hosthttp.Request{URL: raw})
		require.NotNil(t, r.Error, raw)
	}
	require.Zero(t, count.Load())
	e.resolver = lookup(func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("::ffff:127.0.0.1")}, nil
	})
	r := e.Do(context.Background(), hosthttp.Request{URL: "https://example.com"})
	require.Equal(t, "TARGET_IP_DENIED", r.Error.Code)
	require.Zero(t, count.Load())
}
func TestHTTPSAndRedirectPolicy(t *testing.T) {
	e, count := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/external":
			w.Header().Set("Location", "https://evil.example.org/")
			w.WriteHeader(302)
		case "/loop":
			w.Header().Set("Location", "/loop")
			w.WriteHeader(302)
		case "/start":
			w.Header().Set("Location", "/ok")
			w.WriteHeader(302)
		default:
			w.Write([]byte("ok"))
		}
	})
	r := e.Do(context.Background(), hosthttp.Request{URL: "https://example.com/start"})
	require.Nil(t, r.Error)
	require.Equal(t, "ok", string(r.Body))
	require.EqualValues(t, 2, count.Load())
	r = e.Do(context.Background(), hosthttp.Request{URL: "https://example.com/external"})
	require.Equal(t, "REDIRECT_DENIED", r.Error.Code)
	require.EqualValues(t, 3, count.Load())
	r = e.Do(context.Background(), hosthttp.Request{URL: "https://example.com/loop"})
	require.Equal(t, "REDIRECT_DENIED", r.Error.Code)
}
func TestRebindingIsCheckedOnEveryHop(t *testing.T) {
	e, count := fixture(t, func(w http.ResponseWriter, r *http.Request) { w.Header().Set("Location", "/next"); w.WriteHeader(302) })
	calls := 0
	e.resolver = lookup(func(context.Context, string, string) ([]netip.Addr, error) {
		calls++
		ip := "93.184.216.34"
		if calls > 1 {
			ip = "127.0.0.1"
		}
		return []netip.Addr{netip.MustParseAddr(ip)}, nil
	})
	r := e.Do(context.Background(), hosthttp.Request{URL: "https://example.com/"})
	require.Equal(t, "TARGET_IP_DENIED", r.Error.Code)
	require.EqualValues(t, 1, count.Load())
}
func TestLimitsHeadersAndCancellation(t *testing.T) {
	e, count := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/wait" {
			<-r.Context().Done()
			return
		}
		w.Write([]byte(strings.Repeat("x", 32)))
	})
	e.policy.MaxResponseBytes = 8
	r := e.Do(context.Background(), hosthttp.Request{URL: "https://example.com/"})
	require.Equal(t, "RESPONSE_TOO_LARGE", r.Error.Code)
	for _, key := range []string{"Host", "Cookie", "Proxy-Authorization", "Connection", "Transfer-Encoding"} {
		r = e.Do(context.Background(), hosthttp.Request{URL: "https://example.com/", Headers: http.Header{key: []string{"x"}}})
		require.Equal(t, "HEADERS_DENIED", r.Error.Code)
	}
	require.EqualValues(t, 1, count.Load())
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	r = e.Do(ctx, hosthttp.Request{URL: "https://example.com/wait"})
	require.Equal(t, "TIMEOUT", r.Error.Code)
}
func TestTLSVerificationNotDisabled(t *testing.T) {
	e, _ := fixture(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	e.rootCAs = x509.NewCertPool()
	r := e.Do(context.Background(), hosthttp.Request{URL: "https://example.com"})
	require.Equal(t, "NETWORK_ERROR", r.Error.Code)
}
func TestCrossOriginBodyAndCredentials(t *testing.T) {
	e, count := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "https://other.example.com/end")
		w.WriteHeader(307)
	})
	r := e.Do(context.Background(), hosthttp.Request{Method: "POST", URL: "https://example.com/start", Body: []byte("secret")})
	require.Equal(t, "REDIRECT_DENIED", r.Error.Code)
	require.EqualValues(t, 1, count.Load())
}
func TestPolicyValidation(t *testing.T) {
	for _, p := range []hosthttp.Policy{{}, {Rules: []hosthttp.Rule{{Hosts: []string{"*.example.com"}, Methods: []string{"GET"}}}}, {Rules: []hosthttp.Rule{{Hosts: []string{"example.com"}, Methods: []string{"CONNECT"}}}}, {Rules: []hosthttp.Rule{{Hosts: []string{"example.com"}, Methods: []string{"GET"}}}, MaxRedirects: 99}} {
		_, err := New(p)
		require.Error(t, err)
	}
}

func TestCrossOriginHeadersAndCookieScope(t *testing.T) {
	var authorization, cookies string
	e, _ := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			http.SetCookie(w, &http.Cookie{Name: "guest", Value: "anonymous", Domain: "example.com", Path: "/", Secure: true})
			http.SetCookie(w, &http.Cookie{Name: "bad", Value: "public-suffix", Domain: "com", Path: "/", Secure: true})
			w.Header().Set("Location", "https://other.example.com/end")
			w.WriteHeader(302)
			return
		}
		authorization = r.Header.Get("Authorization")
		cookies = r.Header.Get("Cookie")
		w.Write([]byte("ok"))
	})
	r := e.Do(context.Background(), hosthttp.Request{URL: "https://example.com/start", Headers: http.Header{"authorization": []string{"Bearer private"}}})
	require.Nil(t, r.Error)
	require.Empty(t, authorization)
	require.Contains(t, cookies, "guest=anonymous")
	require.NotContains(t, cookies, "bad=")
	require.Empty(t, r.Headers.Get("Set-Cookie"))
	r = e.Do(context.Background(), hosthttp.Request{URL: "https://other.example.com/end"})
	require.Nil(t, r.Error)
	require.Empty(t, cookies, "separate requests must not share guest sessions")
}

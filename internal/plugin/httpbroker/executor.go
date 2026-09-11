// Package httpbroker enforces HTTP capabilities independently of plugin code.
package httpbroker

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/plugin/sdk/hosthttp"
	"golang.org/x/net/publicsuffix"
)

var hostname = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)+$`)
var methods = map[string]bool{"GET": true, "HEAD": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true, "OPTIONS": true}
var globalSlots = make(chan struct{}, 32)

func Normalize(p hosthttp.Policy) (hosthttp.Policy, error) {
	if len(p.Rules) == 0 || len(p.Rules) > 32 {
		return p, fmt.Errorf("http.rules requires 1 to 32 rules")
	}
	for _, r := range p.Rules {
		if len(r.Hosts) == 0 || len(r.Hosts) > 32 || len(r.Methods) == 0 {
			return p, fmt.Errorf("each HTTP rule requires hosts and methods")
		}
		for _, h := range r.Hosts {
			if len(h) > 253 || !hostname.MatchString(h) || net.ParseIP(h) != nil {
				return p, fmt.Errorf("HTTP hosts must be exact lowercase DNS names, without wildcards or IP literals")
			}
		}
		for _, m := range r.Methods {
			if !methods[m] {
				return p, fmt.Errorf("unsupported HTTP method %q", m)
			}
		}
	}
	if p.TimeoutSeconds == 0 {
		p.TimeoutSeconds = 20
	}
	if p.MaxResponseBytes == 0 {
		p.MaxResponseBytes = 1 << 20
	}
	if p.MaxRequestBytes == 0 {
		p.MaxRequestBytes = 1 << 20
	}
	if p.MaxRedirects < 0 || p.MaxRedirects > 5 || p.TimeoutSeconds < 1 || p.TimeoutSeconds > 30 || p.MaxResponseBytes < 1 || p.MaxResponseBytes > hosthttp.MaxBodyBytes || p.MaxRequestBytes < 1 || p.MaxRequestBytes > hosthttp.MaxBodyBytes {
		return p, fmt.Errorf("HTTP limits: redirects 0..5, timeout 1..30 seconds, request/response 1..2097152 bytes")
	}
	return p, nil
}

type resolver interface {
	LookupNetIP(context.Context, string, string) ([]netip.Addr, error)
}
type Executor struct {
	rootCAs  *x509.CertPool // nil in production: use the system trust store
	policy   hosthttp.Policy
	resolver resolver
	dial     func(context.Context, string, string) (net.Conn, error)
	Audit    func(host, code string)
}

func New(p hosthttp.Policy) (*Executor, error) {
	p, err := Normalize(p)
	if err != nil {
		return nil, err
	}
	// Own the policy snapshot; later manifest mutations must not expand authority.
	rules := make([]hosthttp.Rule, len(p.Rules))
	for i, r := range p.Rules {
		rules[i] = hosthttp.Rule{Hosts: append([]string(nil), r.Hosts...), Methods: append([]string(nil), r.Methods...)}
	}
	p.Rules = rules
	return &Executor{policy: p, resolver: net.DefaultResolver, dial: (&net.Dialer{Timeout: 10 * time.Second}).DialContext}, nil
}
func failure(code, message string) *hosthttp.Response {
	return &hosthttp.Response{Error: &hosthttp.Error{Code: code, Message: message}}
}

// Exclude special-purpose ranges in addition to private, loopback and link local.
var deniedPrefixes = func() []netip.Prefix {
	var out []netip.Prefix
	for _, s := range []string{"0.0.0.0/8", "100.64.0.0/10", "168.63.129.16/32", "192.0.0.0/24", "192.0.2.0/24", "192.88.99.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "224.0.0.0/3", "2001::/23", "2001:db8::/32", "2002::/16", "3fff::/20"} {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}()

func publicIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || ip.Zone() != "" || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	// Only native global IPv6; deny NAT64/translation ranges and local aliases.
	if ip.Is6() && !netip.MustParsePrefix("2000::/3").Contains(ip) {
		return false
	}
	for _, p := range deniedPrefixes {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}
func (e *Executor) target(raw, method string) (*url.URL, *hosthttp.Response) {
	u, err := url.Parse(raw)
	if err != nil || len(raw) > 8192 || u.Scheme != "https" || u.Opaque != "" || u.User != nil || (u.Port() != "" && u.Port() != "443") || u.Fragment != "" {
		return nil, failure("INVALID_URL", "only HTTPS port 443 without userinfo or fragment is supported")
	}
	h := strings.ToLower(u.Hostname())
	if !hostname.MatchString(h) || net.ParseIP(h) != nil {
		return nil, failure("DOMAIN_NOT_ALLOWED", "an approved DNS hostname is required")
	}
	u.Host = h // canonical host and default port, no user-controlled Host header
	for _, r := range e.policy.Rules {
		for _, allowed := range r.Hosts {
			if h == allowed {
				for _, m := range r.Methods {
					if m == method {
						return u, nil
					}
				}
			}
		}
	}
	return nil, failure("DOMAIN_NOT_ALLOWED", fmt.Sprintf("%s %s is not approved", method, h))
}
func safeHeaders(h http.Header) bool {
	size := 0
	for k, vs := range h {
		switch strings.ToLower(k) {
		case "accept", "accept-language", "content-type", "authorization", "user-agent", "if-none-match", "if-modified-since", "referer":
		default:
			return false
		}
		for _, v := range vs {
			size += len(k) + len(v)
			if strings.ContainsAny(v, "\r\n\x00") {
				return false
			}
		}
	}
	return size <= 16384
}
func (e *Executor) Do(parent context.Context, in hosthttp.Request) (out *hosthttp.Response) {
	host := ""
	defer func() {
		if e.Audit != nil {
			code := "OK"
			if out != nil && out.Error != nil {
				code = out.Error.Code
			}
			e.Audit(host, code)
		}
	}()
	select {
	case globalSlots <- struct{}{}:
		defer func() { <-globalSlots }()
	default:
		return failure("BUSY", "host HTTP concurrency limit reached")
	}
	if len(in.Body) > int(e.policy.MaxRequestBytes) {
		return failure("REQUEST_TOO_LARGE", "request exceeds approved size")
	}
	if !safeHeaders(in.Headers) {
		return failure("HEADERS_DENIED", "unsupported headers or header size")
	}
	seconds := e.policy.TimeoutSeconds
	if in.TimeoutSeconds < 0 {
		return failure("INVALID_REQUEST", "invalid timeout")
	}
	if in.TimeoutSeconds > 0 && in.TimeoutSeconds < seconds {
		seconds = in.TimeoutSeconds
	}
	ctx, cancel := context.WithTimeout(parent, time.Duration(seconds)*time.Second)
	defer cancel()
	method := in.Method
	if method == "" {
		method = "GET"
	}
	raw := in.URL
	body := in.Body
	headers := in.Headers.Clone()
	if headers == nil {
		headers = make(http.Header)
	}
	jar, _ := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List}) // ephemeral, only this request's redirect chain
	previousHost := ""
	for hop := 0; ; hop++ {
		// Audit the attempted destination even when a redirect is rejected;
		// never include its path, query, userinfo or request headers.
		if auditURL, err := url.Parse(raw); err == nil && hostname.MatchString(strings.ToLower(auditURL.Hostname())) {
			host = strings.ToLower(auditURL.Hostname())
		}
		u, rejection := e.target(raw, method)
		if rejection != nil {
			if hop > 0 {
				rejection.Error.Code = "REDIRECT_DENIED"
			}
			return rejection
		}
		host = u.Hostname()
		if previousHost != "" && previousHost != host {
			for k := range headers {
				if strings.EqualFold(k, "Authorization") || strings.EqualFold(k, "Referer") {
					delete(headers, k)
				}
			}
			// Do not leak a POST body via cross-origin 307/308.
			if len(body) > 0 {
				return failure("REDIRECT_DENIED", "cross-origin redirect with request body is not supported")
			}
		}
		// Only a same-origin root Referer is supported. No document paths,
		// query tokens, userinfo or cross-origin values can be forwarded.
		refs := 0
		for key, values := range headers {
			if !strings.EqualFold(key, "Referer") {
				continue
			}
			for _, value := range values {
				refs++
				ref, err := url.Parse(value)
				if err != nil || ref.Scheme != "https" || !strings.EqualFold(ref.Hostname(), host) || ref.User != nil || (ref.Port() != "" && ref.Port() != "443") || ref.Path != "/" || ref.RawQuery != "" || ref.ForceQuery || ref.Fragment != "" || ref.RawPath != "" || refs > 1 {
					return failure("HEADERS_DENIED", "Referer must be the same HTTPS origin root")
				}
			}
		}
		ips, err := e.resolver.LookupNetIP(ctx, "ip", host)
		if err != nil || len(ips) == 0 {
			return requestError(ctx, "DNS resolution failed")
		}
		for _, ip := range ips {
			if !publicIP(ip) {
				return failure("TARGET_IP_DENIED", "DNS resolved to a non-public address")
			}
		}
		// Dial numeric addresses only. TLS still authenticates the original hostname.
		tr := &http.Transport{Proxy: nil, DisableCompression: true, DisableKeepAlives: true, MaxResponseHeaderBytes: 32768,
			TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, ServerName: host, RootCAs: e.rootCAs},
			DialContext: func(c context.Context, network, address string) (net.Conn, error) {
				var last error
				for _, ip := range ips {
					conn, err := e.dial(c, "tcp", net.JoinHostPort(ip.String(), "443"))
					if err == nil {
						return conn, nil
					}
					last = err
				}
				return nil, last
			}}
		client := &http.Client{Transport: tr, Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		req, err := http.NewRequestWithContext(ctx, method, u.String(), bytes.NewReader(body))
		if err != nil {
			return failure("INVALID_REQUEST", "cannot create HTTP request")
		}
		req.Header = headers.Clone()
		resp, err := client.Do(req)
		if err != nil {
			tr.CloseIdleConnections()
			return requestError(ctx, "HTTPS connection failed")
		}
		location := resp.Header.Get("Location")
		redirect := resp.StatusCode == 301 || resp.StatusCode == 302 || resp.StatusCode == 303 || resp.StatusCode == 307 || resp.StatusCode == 308
		if redirect && location != "" {
			resp.Body.Close()
			tr.CloseIdleConnections()
			if hop >= e.policy.MaxRedirects {
				return failure("REDIRECT_DENIED", "redirect limit reached")
			}
			next, err := u.Parse(location)
			if err != nil {
				return failure("REDIRECT_DENIED", "invalid redirect URL")
			}
			// Fragments do not participate in HTTP requests.
			next.Fragment = ""
			raw = next.String()
			previousHost = host
			if resp.StatusCode == 303 && method != "HEAD" || (resp.StatusCode == 301 || resp.StatusCode == 302) && method == "POST" {
				method = "GET"
				body = nil
				headers.Del("Content-Type")
			}
			continue
		}
		if resp.Header.Get("Content-Encoding") != "" && resp.Header.Get("Content-Encoding") != "identity" {
			resp.Body.Close()
			tr.CloseIdleConnections()
			return failure("ENCODING_DENIED", "compressed responses are not supported")
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, e.policy.MaxResponseBytes+1))
		resp.Body.Close()
		tr.CloseIdleConnections()
		if err != nil {
			return requestError(ctx, "response read failed")
		}
		if int64(len(data)) > e.policy.MaxResponseBytes {
			return failure("RESPONSE_TOO_LARGE", "response exceeds approved size")
		}
		resp.Header.Del("Set-Cookie")
		return &hosthttp.Response{StatusCode: resp.StatusCode, Headers: resp.Header, Body: data}
	}
}
func requestError(ctx context.Context, message string) *hosthttp.Response {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return failure("TIMEOUT", "HTTP request deadline exceeded")
	}
	if ctx.Err() != nil {
		return failure("CANCELLED", "HTTP request cancelled")
	}
	return failure("NETWORK_ERROR", message)
}

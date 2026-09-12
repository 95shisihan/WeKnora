// Package hosthttp exposes host-mediated HTTPS to OS-isolated plugins.
package hosthttp

import "net/http"

// Policy is an administrator-approved upper bound, never supplied by a request.
type Policy struct {
	Rules            []Rule `json:"rules" yaml:"rules"`
	MaxRedirects     int    `json:"maxRedirects" yaml:"maxRedirects"`
	TimeoutSeconds   int    `json:"timeoutSeconds" yaml:"timeoutSeconds"`
	MaxResponseBytes int64  `json:"maxResponseBytes" yaml:"maxResponseBytes"`
	MaxRequestBytes  int64  `json:"maxRequestBytes" yaml:"maxRequestBytes"`
}
type Rule struct {
	Hosts   []string `json:"hosts" yaml:"hosts"`
	Methods []string `json:"methods" yaml:"methods"`
}

const MaxWireBytes = 4 << 20
const MaxBodyBytes = 2 << 20
const Method = "/weknora.hosthttp.v1.Channel/Open"

type Request struct {
	URL            string      `json:"url"`
	Method         string      `json:"method"`
	Headers        http.Header `json:"headers,omitempty"`
	Body           []byte      `json:"body,omitempty"`
	TimeoutSeconds int         `json:"timeoutSeconds,omitempty"`
}
type Response struct {
	StatusCode int         `json:"statusCode,omitempty"`
	Headers    http.Header `json:"headers,omitempty"`
	Body       []byte      `json:"body,omitempty"`
	Error      *Error      `json:"error,omitempty"`
}
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// Frame is JSON inside protobuf google.protobuf.BytesValue. No plugin identity
// is accepted: the host binds policy to its own connection to this process.
type Frame struct {
	ID       uint64    `json:"id,omitempty"`
	Ready    bool      `json:"ready,omitempty"`
	Cancel   bool      `json:"cancel,omitempty"`
	Request  *Request  `json:"request,omitempty"`
	Response *Response `json:"response,omitempty"`
}

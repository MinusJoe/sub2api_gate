package tlsfingerprint

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/http/httptrace"
	"sync/atomic"

	utls "github.com/refraction-networking/utls"
)

const (
	StatusApplied  = "applied"
	StatusStandard = "standard"
	StatusPlain    = "plain"
	StatusPlugin   = "plugin"
)

type responseStatusKey struct{}

// Only the gateway dialer returns this type, after the preset and handshake
// both succeed. The marker follows the socket throughout its pooled lifetime.
type fingerprintedConn struct{ *utls.UConn }

// SetResponseStatus stores local evidence on the response, outside wire headers.
func SetResponseStatus(resp *http.Response, req *http.Request, status string) {
	if resp == nil {
		return
	}
	if resp.Request != nil {
		req = resp.Request
	}
	if req == nil {
		return
	}
	resp.Request = req.WithContext(context.WithValue(req.Context(), responseStatusKey{}, status))
}

func ResponseStatus(resp *http.Response) string {
	if resp == nil || resp.Request == nil {
		return ""
	}
	status, _ := resp.Request.Context().Value(responseStatusKey{}).(string)
	return status
}

// ObservedClient shares the existing connection pool. Each redirect gets its own
// observation, and GotConn also identifies a previously established connection.
func ObservedClient(client *http.Client) *http.Client {
	if client == nil {
		client = http.DefaultClient
	}
	base := client.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	observed := *client
	observed.Transport = observedTransport{base}
	return &observed
}

type observedTransport struct{ http.RoundTripper }

func (t observedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var status atomic.Uint32
	const (
		unobserved = iota
		customTLS
		standardTLS
		plainHTTP
	)
	trace := &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) {
		var value uint32 = unobserved
		if req.URL.Scheme == "http" {
			value = plainHTTP
		} else if req.URL.Scheme == "https" {
			switch conn := info.Conn.(type) {
			case *fingerprintedConn:
				value = customTLS
			case *tls.Conn:
				if conn.ConnectionState().HandshakeComplete {
					value = standardTLS
				}
			}
		}
		status.Store(value)
	}}
	resp, err := t.RoundTripper.RoundTrip(req.WithContext(httptrace.WithClientTrace(req.Context(), trace)))
	if err == nil && resp != nil {
		if observed := status.Load(); observed != unobserved {
			values := [...]string{"", StatusApplied, StatusStandard, StatusPlain}
			SetResponseStatus(resp, req, values[observed])
		}
	}
	return resp, err
}

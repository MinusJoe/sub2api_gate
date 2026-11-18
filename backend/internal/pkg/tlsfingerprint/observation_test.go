package tlsfingerprint

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"testing"

	utls "github.com/refraction-networking/utls"
	"github.com/stretchr/testify/require"
)

func TestResponseStatusTracksTheConnectionIncludingReuse(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// A remote header cannot supply local transport evidence.
		w.Header().Set("Tls-Fingerprint-Status", "applied")
		w.WriteHeader(http.StatusNoContent)
	})
	plain := httptest.NewServer(handler)
	defer plain.Close()
	secure := httptest.NewTLSServer(handler)
	defer secure.Close()
	roots := x509.NewCertPool()
	roots.AddCert(secure.Certificate())
	d := &net.Dialer{}
	custom := &http.Transport{
		DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			raw, err := d.DialContext(ctx, network, addr)
			if err != nil {
				return nil, err
			}
			host, _, err := net.SplitHostPort(addr)
			if err != nil {
				_ = raw.Close()
				return nil, err
			}
			conn := utls.UClient(raw, &utls.Config{ServerName: host, RootCAs: roots}, utls.HelloGolang)
			if err := conn.HandshakeContext(ctx); err != nil {
				_ = raw.Close()
				return nil, err
			}
			return &fingerprintedConn{conn}, nil
		},
	}
	defer custom.CloseIdleConnections()
	for _, tc := range []struct {
		status, url string
		client      *http.Client
	}{
		{StatusApplied, secure.URL, &http.Client{Transport: custom}},
		{StatusStandard, secure.URL, secure.Client()},
		{StatusPlain, plain.URL, plain.Client()},
	} {
		t.Run(tc.status, func(t *testing.T) {
			client := ObservedClient(tc.client)
			for i := 0; i < 2; i++ {
				var reused bool
				ctx := httptrace.WithClientTrace(context.Background(), &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) { reused = info.Reused }})
				req, err := http.NewRequestWithContext(ctx, http.MethodGet, tc.url, nil)
				require.NoError(t, err)
				resp, err := client.Do(req)
				require.NoError(t, err)
				_, err = io.Copy(io.Discard, resp.Body)
				require.NoError(t, err)
				require.NoError(t, resp.Body.Close())
				require.Equal(t, i == 1, reused)
				require.Equal(t, tc.status, ResponseStatus(resp))
			}
		})
	}
}

func TestResponseStatusUsesFinalHopAndIgnoresUnobservedResponses(t *testing.T) {
	dst := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer dst.Close()
	src := httptest.NewTLSServer(http.RedirectHandler(dst.URL, http.StatusFound))
	defer src.Close()
	resp, err := ObservedClient(src.Client()).Get(src.URL)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, dst.Listener.Addr().String(), resp.Request.URL.Host)
	require.Equal(t, StatusPlain, ResponseStatus(resp))
	unobserved := &http.Response{Header: http.Header{"Tls-Fingerprint-Status": {"applied"}}, TLS: &tls.ConnectionState{HandshakeComplete: true}}
	require.Empty(t, ResponseStatus(unobserved))
	SetResponseStatus(unobserved, httptest.NewRequest(http.MethodGet, "/", nil), StatusPlugin)
	require.Equal(t, StatusPlugin, ResponseStatus(unobserved))
}

package httpclient

import (
	"testing"

	"github.com/MACOS-DO/sub4api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

// 指纹模式必须强制 h1 并使用 utls 握手，否则会重新发出 Go 的 ALPN。
func TestBuildTransportWithTLSProfileDirect(t *testing.T) {
	profile := tlsfingerprint.CodexRustlsProfile()
	transport, err := buildTransport(Options{TLSProfile: profile})
	require.NoError(t, err)
	require.False(t, transport.ForceAttemptHTTP2)
	require.NotNil(t, transport.DialTLSContext)
	require.NotNil(t, transport.TLSNextProto)
	require.Nil(t, transport.Proxy, "指纹模式由 dialer 处理代理，不能再设 Proxy")
}

// http 代理走 CONNECT + utls。
func TestBuildTransportWithTLSProfileHTTPProxy(t *testing.T) {
	transport, err := buildTransport(Options{
		ProxyURL:   "http://127.0.0.1:18080",
		TLSProfile: tlsfingerprint.CodexRustlsProfile(),
	})
	require.NoError(t, err)
	require.NotNil(t, transport.DialTLSContext)
	require.Nil(t, transport.Proxy)
}

// https 代理无法建立 CONNECT+TLS，必须回退为普通代理（仍然是可用客户端）。
func TestBuildTransportWithTLSProfileHTTPSProxyFallsBack(t *testing.T) {
	transport, err := buildTransport(Options{
		ProxyURL:   "https://127.0.0.1:18443",
		TLSProfile: tlsfingerprint.CodexRustlsProfile(),
	})
	require.NoError(t, err)
	require.Nil(t, transport.DialTLSContext)
	require.NotNil(t, transport.Proxy, "https 代理回退后必须走普通 Proxy")
}

// 同参数复用缓存；profile 不同必须拿到不同客户端（连接池不能串用指纹）。
func TestGetClientIsolatesByTLSProfile(t *testing.T) {
	a, err := GetClient(Options{Timeout: 1, TLSProfile: tlsfingerprint.CodexRustlsProfile()})
	require.NoError(t, err)
	b, err := GetClient(Options{Timeout: 1, TLSProfile: tlsfingerprint.CodexRustlsProfile()})
	require.NoError(t, err)
	require.Same(t, a, b, "相同 profile 必须复用同一个客户端")

	edited := tlsfingerprint.CodexRustlsProfile()
	edited.CipherSuites = append([]uint16{0x1301}, edited.CipherSuites...)
	c, err := GetClient(Options{Timeout: 1, TLSProfile: edited})
	require.NoError(t, err)
	require.NotSame(t, a, c, "profile 变化必须换客户端")

	plain, err := GetClient(Options{Timeout: 1})
	require.NoError(t, err)
	require.NotSame(t, a, plain, "?????????????????????")
}

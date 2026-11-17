package httpclient

import (
	"crypto/tls"
	"net/http"
	"net/url"
	"strings"

	"github.com/MACOS-DO/sub4api/internal/pkg/proxyutil"
	"github.com/MACOS-DO/sub4api/internal/pkg/tlsfingerprint"
)

// applyTLSFingerprintTransport 把 transport 换成"按 profile 握手"的形态：
//   - 强制 HTTP/1.1（ForceAttemptHTTP2=false + 空 TLSNextProto），与 Codex 官方客户端一致；
//   - 直连/http 代理/socks5 分别用对应的 tlsfingerprint dialer 完成握手；
//   - https 代理与未知 scheme 无法建立 CONNECT+TLS，回退为普通代理（不伪装，但可用）。
//
// 必须在 ConfigureTransportProxy 之前调用：指纹模式下 CONNECT 由 dialer 自己发，
// 不能再让 Transport 再包一层 Proxy，否则握手会绕开 profile。
func applyTLSFingerprintTransport(transport *http.Transport, proxy *url.URL, profile *tlsfingerprint.Profile) {
	if transport == nil || profile == nil {
		return
	}
	transport.ForceAttemptHTTP2 = false
	transport.TLSNextProto = make(map[string]func(string, *tls.Conn) http.RoundTripper)

	if proxy == nil {
		transport.DialTLSContext = tlsfingerprint.NewDialer(profile, nil).DialTLSContext
		return
	}
	switch strings.ToLower(proxy.Scheme) {
	case "http":
		transport.DialTLSContext = tlsfingerprint.NewHTTPProxyDialer(profile, proxy).DialTLSContext
	case "socks5", "socks5h":
		transport.DialTLSContext = tlsfingerprint.NewSOCKS5ProxyDialer(profile, proxy).DialTLSContext
	default:
		_ = proxyutil.ConfigureTransportProxy(transport, proxy)
	}
}

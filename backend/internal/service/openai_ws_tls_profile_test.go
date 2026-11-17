package service

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/MACOS-DO/sub4api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

var (
	errStubDialPath    = errors.New("stub dial")
	errStubProfilePath = errors.New("stub dial with profile")
)

// stubWSProfileDialer 同时实现 Dial 与 DialWithProfile，用于区分走的是哪条路径。
type stubWSProfileDialer struct {
	dialCalls    int
	profileCalls int
	lastProfile  *tlsfingerprint.Profile
}

func (s *stubWSProfileDialer) Dial(context.Context, string, http.Header, string) (openAIWSClientConn, int, http.Header, error) {
	s.dialCalls++
	return nil, 0, nil, errStubDialPath
}

func (s *stubWSProfileDialer) DialWithProfile(_ context.Context, _ string, _ http.Header, _ string, profile *tlsfingerprint.Profile) (openAIWSClientConn, int, http.Header, error) {
	s.profileCalls++
	s.lastProfile = profile
	return nil, 0, nil, errStubProfilePath
}

// stubWSPlainDialer 只实现基础接口：profile 非空时应回落到 Dial。
type stubWSPlainDialer struct{ dialCalls int }

func (s *stubWSPlainDialer) Dial(context.Context, string, http.Header, string) (openAIWSClientConn, int, http.Header, error) {
	s.dialCalls++
	return nil, 0, nil, errStubDialPath
}

func TestDialOpenAIWSClientRouting(t *testing.T) {
	ctx := context.Background()
	profile := tlsfingerprint.CodexRustlsProfile()

	t.Run("nil dialer", func(t *testing.T) {
		_, _, _, err := dialOpenAIWSClient(ctx, nil, "wss://example.invalid", nil, "", profile)
		require.ErrorIs(t, err, errOpenAIWSClientDialerNil)
	})

	t.Run("no profile keeps Dial", func(t *testing.T) {
		d := &stubWSProfileDialer{}
		_, _, _, err := dialOpenAIWSClient(ctx, d, "wss://example.invalid", nil, "", nil)
		require.ErrorIs(t, err, errStubDialPath)
		require.Equal(t, 1, d.dialCalls)
		require.Equal(t, 0, d.profileCalls)
	})

	t.Run("profile uses DialWithProfile", func(t *testing.T) {
		d := &stubWSProfileDialer{}
		_, _, _, err := dialOpenAIWSClient(ctx, d, "wss://example.invalid", nil, "", profile)
		require.ErrorIs(t, err, errStubProfilePath)
		require.Equal(t, 1, d.profileCalls)
		require.Equal(t, 0, d.dialCalls)
		require.Equal(t, profile.Name, d.lastProfile.Name)
	})

	t.Run("profile falls back when dialer lacks support", func(t *testing.T) {
		d := &stubWSPlainDialer{}
		_, _, _, err := dialOpenAIWSClient(ctx, d, "wss://example.invalid", nil, "", profile)
		require.ErrorIs(t, err, errStubDialPath)
		require.Equal(t, 1, d.dialCalls)
	})
}

// 指纹客户端必须是 h1 + utls 握手：ForceAttemptHTTP2=false 且 DialTLSContext 非空，
// 直接复用默认 Transport 会重新发出 Go 的 ALPN。
func TestWSUpstreamHTTPClientWithProfile(t *testing.T) {
	d := &coderOpenAIWSClientDialer{proxyClients: make(map[string]*openAIWSProxyClientEntry)}
	profile := tlsfingerprint.CodexRustlsProfile()

	client, err := d.upstreamHTTPClient("", profile)
	require.NoError(t, err)
	require.NotNil(t, client)
	transport, ok := client.Transport.(*http.Transport)
	require.True(t, ok, "必须是自定义 transport")
	require.False(t, transport.ForceAttemptHTTP2)
	require.NotNil(t, transport.DialTLSContext)

	// 同 profile 同代理必须复用同一个 client（连接池才有效）。
	again, err := d.upstreamHTTPClient("", profile)
	require.NoError(t, err)
	require.Same(t, client, again)

	// 不同 profile 不能共用 client。
	other := tlsfingerprint.CodexRustlsProfile()
	other.Name = "other"
	other.CipherSuites = append([]uint16{0x1301}, other.CipherSuites...)
	otherClient, err := d.upstreamHTTPClient("", other)
	require.NoError(t, err)
	require.NotSame(t, client, otherClient)

	// 无 profile 且无代理：保持原行为（返回 nil，让 coderws 用默认客户端）。
	plain, err := d.upstreamHTTPClient("", nil)
	require.NoError(t, err)
	require.Nil(t, plain)
}

// http 代理走 CONNECT + utls；https 代理无法用指纹 dialer，必须回退到普通代理客户端。
func TestWSUpstreamHTTPClientProxySchemes(t *testing.T) {
	profile := tlsfingerprint.CodexRustlsProfile()

	d := &coderOpenAIWSClientDialer{proxyClients: make(map[string]*openAIWSProxyClientEntry)}
	client, err := d.upstreamHTTPClient("http://127.0.0.1:18080", profile)
	require.NoError(t, err)
	transport := client.Transport.(*http.Transport)
	require.NotNil(t, transport.DialTLSContext)
	require.Nil(t, transport.Proxy, "指纹模式由 DialTLSContext 处理 CONNECT，不能再设 Proxy")

	d2 := &coderOpenAIWSClientDialer{proxyClients: make(map[string]*openAIWSProxyClientEntry)}
	fallback, err := d2.upstreamHTTPClient("https://127.0.0.1:18443", profile)
	require.NoError(t, err)
	fallbackTransport := fallback.Transport.(*http.Transport)
	require.NotNil(t, fallbackTransport.Proxy, "https 代理必须回退到普通代理客户端")
	require.Nil(t, fallbackTransport.DialTLSContext)
}

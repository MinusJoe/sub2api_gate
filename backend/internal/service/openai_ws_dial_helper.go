package service

import (
	"context"
	"errors"
	"net/http"

	"github.com/MACOS-DO/sub4api/internal/pkg/tlsfingerprint"
)

// errOpenAIWSClientDialerNil 用于统一 WS 建连入口的空 dialer 判定。
var errOpenAIWSClientDialerNil = errors.New("openai ws client dialer is nil")

// dialOpenAIWSClient 是 WS 建连的统一入口：profile 非空且 dialer 支持时走
// DialWithProfile（utls 指纹），否则维持原来的 Dial。
//
// 用类型断言而不是扩展 openAIWSClientDialer 接口：测试桩与其它 dialer 实现
// 不需要改动，也不影响 profile == nil 时的既有行为。
func dialOpenAIWSClient(
	ctx context.Context,
	dialer openAIWSClientDialer,
	wsURL string,
	headers http.Header,
	proxyURL string,
	profile *tlsfingerprint.Profile,
) (openAIWSClientConn, int, http.Header, error) {
	if dialer == nil {
		return nil, 0, nil, errOpenAIWSClientDialerNil
	}
	if profile != nil {
		if pd, ok := dialer.(openAIWSProfileDialer); ok {
			return pd.DialWithProfile(ctx, wsURL, headers, proxyURL, profile)
		}
	}
	return dialer.Dial(ctx, wsURL, headers, proxyURL)
}

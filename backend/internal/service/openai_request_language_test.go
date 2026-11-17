package service

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// accept-language 归一默认关闭，且只在客户端已声明该头时改写：
// 头缺失时不新增，避免给上游凭空多造一个信号。
func TestNormalizeOpenAIAcceptLanguage(t *testing.T) {
	for _, test := range []struct {
		name     string
		existing string
		override string
		want     string
	}{
		{name: "rewrites existing header", existing: "zh-CN,zh;q=0.9", override: "en-US,en;q=0.9", want: "en-US,en;q=0.9"},
		{name: "absent header stays absent", override: "en-US,en;q=0.9", want: ""},
		{name: "empty override keeps client value", existing: "zh-CN,zh;q=0.9", want: "zh-CN,zh;q=0.9"},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := make(http.Header)
			if test.existing != "" {
				h.Set("Accept-Language", test.existing)
			}
			normalizeOpenAIAcceptLanguage(h, test.override)
			if test.want == "" {
				require.Empty(t, h.Values("Accept-Language"), "缺失的头不得被新增")
				return
			}
			require.Equal(t, test.want, h.Get("Accept-Language"))
			require.Len(t, h.Values("Accept-Language"), 1, "改写后应只有一个值，不得追加")
		})
	}
}

// nil header 必须安全 no-op（合成请求路径可能没有 header map）。
func TestNormalizeOpenAIAcceptLanguageNilHeader(t *testing.T) {
	require.NotPanics(t, func() { normalizeOpenAIAcceptLanguage(nil, "en-US") })
}

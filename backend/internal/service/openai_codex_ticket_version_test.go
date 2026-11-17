package service

import (
	"net/http"
	"strings"
	"testing"

	"github.com/MACOS-DO/sub4api/internal/pkg/openai"
	"github.com/stretchr/testify/require"
)

// 打票请求必须与业务转发声明同一个客户端版本：按模型抬版本会让同一账号同一 installation
// 在几分钟内交替声明两个版本，本身就是可识别的异常信号。
//
// 不得给本用例加 t.Parallel()：它改写进程级规范 UA 解析器。
func TestCodexTicketHarvestIdentityKeepsAccountLevelVersion(t *testing.T) {
	const canonicalUA = "codex_cli_rs/0.146.0 (Ubuntu 22.4.0; x86_64) xterm-256color"
	SetCodexCanonicalUserAgentResolver(func() string { return canonicalUA })
	t.Cleanup(func() { SetCodexCanonicalUserAgentResolver(nil) })

	for _, model := range []string{"gpt-6-astra", "gpt-5.6-sol"} {
		t.Run(model, func(t *testing.T) {
			h := make(http.Header)
			applyOpenAICodexTicketHarvestIdentity(h, model, "")

			require.Equal(t, "0.146.0", h.Get("version"), "version 头不得按模型抬升")
			require.Equal(t, canonicalUA, h.Get("user-agent"), "UA 必须与规范身份一致")
			require.Equal(t, openai.CodexCLIOriginator, h.Get("originator"))
			require.Equal(t, "codex_cli_rs", h.Get("originator"))
			// UA 的版本段与 version 头必须同源，否则拼出的身份自相矛盾。
			require.Equal(t, h.Get("version"), openai.CodexUserAgentVersion(h.Get("user-agent")))
			require.False(t, strings.Contains(h.Get("user-agent"), openAICodexAstraMinVersion))
		})
	}
}

// astra 模型在生效版本低于下限时只允许发一条去重 Warn，且绝不修改请求头。
func TestCodexTicketHarvestIdentityWarnsBelowAstraFloorWithoutMutatingHeaders(t *testing.T) {
	const canonicalUA = "codex_cli_rs/0.146.0 (Ubuntu 22.4.0; x86_64) xterm-256color"
	SetCodexCanonicalUserAgentResolver(func() string { return canonicalUA })
	t.Cleanup(func() { SetCodexCanonicalUserAgentResolver(nil) })

	h := make(http.Header)
	require.NotPanics(t, func() { applyOpenAICodexTicketHarvestIdentity(h, "gpt-6-astra", "") })
	require.Equal(t, "0.146.0", h.Get("version"))
	require.Equal(t, canonicalUA, h.Get("user-agent"))
	require.Equal(t, openai.CodexCLIOriginator, h.Get("originator"))

	// 去重后重复调用仍然不 panic、不修改头。
	require.NotPanics(t, func() { applyOpenAICodexTicketHarvestIdentity(h, "gpt-6-astra", "") })
	require.Equal(t, "0.146.0", h.Get("version"))
}

// 账号级自定义 UA 必须同时作用于打票与业务：否则同一 installation 会在两条路径上声明
// 不同的平台/终端指纹（版本号一致也不够，OS/terminal 段本身就是设备指纹的一部分）。
func TestCodexTicketHarvestIdentityUsesAccountOverrideUA(t *testing.T) {
	const canonicalUA = "codex_cli_rs/0.146.0 (Ubuntu 22.4.0; x86_64) xterm-256color"
	const overrideUA = "codex_cli_rs/0.146.0 (Debian 12; x86_64) myterm-256color"
	SetCodexCanonicalUserAgentResolver(func() string { return canonicalUA })
	t.Cleanup(func() { SetCodexCanonicalUserAgentResolver(nil) })

	h := make(http.Header)
	applyOpenAICodexTicketHarvestIdentity(h, "gpt-6-astra", overrideUA)

	require.Equal(t, overrideUA, h.Get("user-agent"), "打票 UA 必须与业务侧账号级覆写同源")
	require.Equal(t, "0.146.0", h.Get("version"))
	require.Equal(t, openai.CodexCLIOriginator, h.Get("originator"))
}

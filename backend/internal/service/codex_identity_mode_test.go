package service

import (
	"strings"
	"testing"

	"github.com/MACOS-DO/sub4api/internal/config"
	"github.com/MACOS-DO/sub4api/internal/pkg/openai"
	"github.com/stretchr/testify/require"
)

// exec 模式：UA 首段、originator、尾部括号组三段必须互相配套（官方实测形态）。
func TestCodexIdentityModeExec(t *testing.T) {
	t.Cleanup(func() { SetCodexIdentityMode("cli") })

	SetCodexIdentityMode("exec")
	ua := buildCodexCLIUserAgent("0.169.0")
	require.True(t, strings.HasPrefix(ua, "codex_exec/0.169.0"), "UA 首段必须是 codex_exec: %s", ua)
	require.True(t, strings.HasSuffix(ua, "(codex_exec; 0.169.0)"), "exec 模式尾部必须带括号组: %s", ua)

	originator, paired, ok := openai.PairCodexClientIdentity(ua)
	require.True(t, ok, "官方 exec UA 必须能被配对解析")
	require.Equal(t, "codex_exec", originator)
	require.Equal(t, ua, paired)
}

// 默认模式保持历史行为：codex_cli_rs，且不带 exec 尾部括号组。
func TestCodexIdentityModeDefaultIsCLI(t *testing.T) {
	t.Cleanup(func() { SetCodexIdentityMode("") })
	SetCodexIdentityMode("")

	ua := buildCodexCLIUserAgent("0.169.0")
	require.True(t, strings.HasPrefix(ua, "codex_cli_rs/0.169.0"))
	require.False(t, strings.HasSuffix(ua, "(codex_cli_rs; 0.169.0)"))
	require.Equal(t, "codex_cli_rs", CodexIdentityModeName())
}

// 非法值必须被拒绝，且不改变当前模式。
func TestCodexIdentityModeRejectsUnknown(t *testing.T) {
	t.Cleanup(func() { SetCodexIdentityMode("cli") })
	SetCodexIdentityMode("cli")
	SetCodexIdentityMode("codex_tui")
	require.Equal(t, "codex_cli_rs", CodexIdentityModeName())
}

// 装配层发布：配置生效；nil 配置不回退。
func TestApplyCodexOutboundConfigPublishesIdentityMode(t *testing.T) {
	t.Cleanup(func() { SetCodexIdentityMode("cli") })
	cfg := &config.Config{}
	cfg.Gateway.CodexIdentityMode = "exec"
	applyCodexOutboundConfig(cfg)
	require.Equal(t, "codex_exec", CodexIdentityModeName())

	applyCodexOutboundConfig(nil)
	require.Equal(t, "codex_exec", CodexIdentityModeName(), "nil config 不得清空已发布值")
}

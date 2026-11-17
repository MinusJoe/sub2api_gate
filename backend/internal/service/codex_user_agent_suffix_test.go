package service

import (
	"strings"
	"testing"

	"github.com/MACOS-DO/sub4api/internal/config"
	"github.com/stretchr/testify/require"
)

// UA 平台段可配置：只改 OS/架构/终端，客户端名与版本段仍由同一处派生。
func TestSetCodexUserAgentSuffix(t *testing.T) {
	t.Cleanup(func() { SetCodexUserAgentSuffix("") })

	baseline := buildCodexCLIUserAgent("0.169.0")
	require.Equal(t, "codex_cli_rs/0.169.0 (Ubuntu 22.4.0; x86_64) xterm-256color", baseline)

	SetCodexUserAgentSuffix("(Ubuntu 26.4.0; x86_64) xterm-256color")
	require.Equal(t,
		"codex_cli_rs/0.169.0 (Ubuntu 26.4.0; x86_64) xterm-256color",
		buildCodexCLIUserAgent("0.169.0"),
		"版本段仍来自同一处，只替换平台段")

	// 畸形值必须被拒绝，且不破坏当前身份。
	before := buildCodexCLIUserAgent("0.169.0")
	SetCodexUserAgentSuffix("no-parens")
	require.Equal(t, before, buildCodexCLIUserAgent("0.169.0"))

	SetCodexUserAgentSuffix("")
	require.Equal(t, baseline, buildCodexCLIUserAgent("0.169.0"), "空串恢复默认")
}

// 装配层发布：配置值生效；nil 配置不回退已发布的 suffix。
func TestApplyCodexOutboundConfigPublishesUserAgentSuffix(t *testing.T) {
	t.Cleanup(func() { SetCodexUserAgentSuffix("") })
	cfg := &config.Config{}
	cfg.Gateway.CodexUserAgentSuffix = "(Ubuntu 26.4.0; x86_64) xterm-256color"
	applyCodexOutboundConfig(cfg)
	require.True(t, strings.Contains(buildCodexCLIUserAgent("0.169.0"), "Ubuntu 26.4.0"))

	applyCodexOutboundConfig(nil)
	require.True(t, strings.Contains(buildCodexCLIUserAgent("0.169.0"), "Ubuntu 26.4.0"),
		"nil config 不得清空已发布值")
}

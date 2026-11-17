package service

import (
	"strings"
	"sync/atomic"

	"github.com/MACOS-DO/sub4api/internal/pkg/logger"
	"github.com/MACOS-DO/sub4api/internal/pkg/openai"
)

// codexIdentityMode 控制出站身份声明成哪一种官方客户端模式。
//
//   - "codex_cli_rs"（默认）：交互式 CLI，与历史行为一致；
//   - "codex_exec"：非交互执行模式。实测官方 exec 的 UA 为
//     codex_exec/0.169.0 (Ubuntu 26.4.0; x86_64) xterm-256color (codex_exec; 0.169.0)，
//     originator 也是 codex_exec。
//
// 两种都是官方客户端；选择取决于下游真实用法，且必须与版本、UA 平台段保持同源。
var codexIdentityModeName = func() *atomic.Value {
	v := &atomic.Value{}
	v.Store(openai.CodexCLIOriginator)
	return v
}()

// SetCodexIdentityMode 发布出站客户端模式。空串或 "cli" 表示默认交互模式；
// 其它未识别值会被拒绝并保留当前模式（避免把身份改成上游不认识的名字）。
func SetCodexIdentityMode(mode string) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "cli", "cli_rs", "codex_cli_rs":
		codexIdentityModeName.Store(openai.CodexCLIOriginator)
	case "exec", "codex_exec":
		codexIdentityModeName.Store("codex_exec")
	default:
		logger.LegacyPrintf("openai_codex_identity", "[warn] ignore invalid gateway.codex_identity_mode %q (want cli|exec)", mode)
	}
}

// CodexIdentityModeName 返回当前出站客户端名（同时用作 originator 与 UA 首段）。
func CodexIdentityModeName() string {
	if v, ok := codexIdentityModeName.Load().(string); ok && v != "" {
		return v
	}
	return openai.CodexCLIOriginator
}

package service

import (
	"sync/atomic"

	"github.com/MACOS-DO/sub4api/internal/pkg/logger"
)

// openAITLSFingerprintDefault 是 OpenAI OAuth 账号的进程级默认开关。
//
// 三态语义（与 codex_cli_only 保持一致）：
//   - 账号 extra 显式 true  → 开启（最高优先）
//   - 账号 extra 显式 false → 关闭（该账号退出全局默认）
//   - 未设置                → 跟随本进程级默认（默认 false = 保持既有行为）
var openAITLSFingerprintDefault = func() *atomic.Bool {
	v := &atomic.Bool{}
	v.Store(false)
	return v
}()

// SetOpenAITLSFingerprintDefault 发布 OpenAI 指纹的进程级默认开关。
func SetOpenAITLSFingerprintDefault(enabled bool) {
	openAITLSFingerprintDefault.Store(enabled)
}

// OpenAITLSFingerprintDefault 返回当前默认值，便于观测与测试。
func OpenAITLSFingerprintDefault() bool {
	return openAITLSFingerprintDefault.Load()
}

// resolveOpenAITLSFingerprintEnabled 计算某个 OpenAI 账号最终是否启用指纹。
// 非 OAuth/SetupToken 账号永远不启用（没有可复刻的官方客户端身份）。
func resolveOpenAITLSFingerprintEnabled(a *Account, def bool) bool {
	if a == nil || !a.IsOpenAIOAuthLike() {
		return false
	}
	if a.Extra != nil {
		if v, ok := a.Extra["enable_tls_fingerprint"]; ok {
			if enabled, ok := v.(bool); ok {
				return enabled
			}
			logger.LegacyPrintf("service.openai_tlsfp",
				"[warn] account %d enable_tls_fingerprint is not a bool (%T), fallback to default=%t", a.ID, v, def)
		}
	}
	return def
}

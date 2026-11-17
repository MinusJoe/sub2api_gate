package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func fpTestAccount(extra map[string]any) *Account {
	return &Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: extra}
}

// 三态：显式 true / 显式 false 优先；未设置跟随默认。
func TestResolveOpenAITLSFingerprintEnabledTriState(t *testing.T) {
	require.True(t, resolveOpenAITLSFingerprintEnabled(fpTestAccount(map[string]any{"enable_tls_fingerprint": true}), false))
	require.False(t, resolveOpenAITLSFingerprintEnabled(fpTestAccount(map[string]any{"enable_tls_fingerprint": false}), true))
	require.True(t, resolveOpenAITLSFingerprintEnabled(fpTestAccount(nil), true))
	require.False(t, resolveOpenAITLSFingerprintEnabled(fpTestAccount(nil), false))
}

// 非 OAuth/SetupToken 账号永远不启用（没有可复刻的官方客户端身份）。
func TestResolveOpenAITLSFingerprintEnabledRejectsAPIKey(t *testing.T) {
	acc := &Account{ID: 7, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Extra: map[string]any{"enable_tls_fingerprint": true}}
	require.False(t, resolveOpenAITLSFingerprintEnabled(acc, true))
}

// Anthropic 不受 OpenAI 默认值影响：仍只有显式 true 才生效。
func TestAccountIsTLSFingerprintEnabledAnthropicUnchangedByDefault(t *testing.T) {
	t.Cleanup(func() { SetOpenAITLSFingerprintDefault(false) })
	SetOpenAITLSFingerprintDefault(true)

	anthropic := &Account{ID: 9, Platform: PlatformAnthropic, Type: AccountTypeOAuth}
	require.False(t, anthropic.IsTLSFingerprintEnabled(), "Anthropic 不能被 OpenAI 默认值带开")

	openai := fpTestAccount(nil)
	require.True(t, openai.IsTLSFingerprintEnabled())
}

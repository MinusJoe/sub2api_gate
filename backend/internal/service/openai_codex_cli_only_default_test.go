package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// codex_cli_only 默认化的三态语义：extra 里的显式 bool 最优先，
// 缺失/类型不符才落到进程级默认（gateway.openai_codex_require_official_client）。
// 默认 false 时整条链路必须与改动前完全一致。
func TestAccountIsCodexCLIOnlyEnabledDefault(t *testing.T) {
	t.Cleanup(func() { SetCodexCLIOnlyDefault(false) })

	newOAuth := func(extra map[string]any) *Account {
		return &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: extra}
	}

	t.Run("默认 false 时缺失字段 = false", func(t *testing.T) {
		SetCodexCLIOnlyDefault(false)
		require.False(t, CodexCLIOnlyDefault())
		require.False(t, newOAuth(nil).IsCodexCLIOnlyEnabled())
		require.False(t, newOAuth(map[string]any{}).IsCodexCLIOnlyEnabled())
		require.False(t, newOAuth(map[string]any{"other": true}).IsCodexCLIOnlyEnabled())
	})

	t.Run("默认 true 时缺失字段 = true", func(t *testing.T) {
		SetCodexCLIOnlyDefault(true)
		require.True(t, CodexCLIOnlyDefault())
		require.True(t, newOAuth(nil).IsCodexCLIOnlyEnabled())
		require.True(t, newOAuth(map[string]any{}).IsCodexCLIOnlyEnabled())
	})

	t.Run("显式 false 在默认 true 下仍为 false", func(t *testing.T) {
		SetCodexCLIOnlyDefault(true)
		require.False(t, newOAuth(map[string]any{"codex_cli_only": false}).IsCodexCLIOnlyEnabled(),
			"账号级显式关闭不得被全局默认覆盖")
	})

	t.Run("显式 true 在默认 false 下仍为 true", func(t *testing.T) {
		SetCodexCLIOnlyDefault(false)
		require.True(t, newOAuth(map[string]any{"codex_cli_only": true}).IsCodexCLIOnlyEnabled())
	})

	t.Run("类型不符按缺失处理（跟随默认）", func(t *testing.T) {
		SetCodexCLIOnlyDefault(false)
		require.False(t, newOAuth(map[string]any{"codex_cli_only": "true"}).IsCodexCLIOnlyEnabled())
		SetCodexCLIOnlyDefault(true)
		require.True(t, newOAuth(map[string]any{"codex_cli_only": "true"}).IsCodexCLIOnlyEnabled(),
			"字符串类型的字段等同缺失，应跟随默认")
	})

	t.Run("非 OpenAI OAuth 账号不受全局默认影响", func(t *testing.T) {
		SetCodexCLIOnlyDefault(true)
		require.False(t, (&Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}).IsCodexCLIOnlyEnabled())
		require.False(t, (&Account{Platform: PlatformAnthropic, Type: AccountTypeOAuth}).IsCodexCLIOnlyEnabled())
		var nilAccount *Account
		require.False(t, nilAccount.IsCodexCLIOnlyEnabled())
	})

	t.Run("app_server 放行仍以 codex_cli_only 开启为前提", func(t *testing.T) {
		SetCodexCLIOnlyDefault(true)
		require.False(t, newOAuth(map[string]any{"codex_cli_only": false, "codex_cli_only_allow_app_server": true}).
			IsCodexCLIOnlyAppServerAllowed(), "显式关闭后 app_server 放行必须一并失效")
		require.True(t, newOAuth(map[string]any{"codex_cli_only_allow_app_server": true}).
			IsCodexCLIOnlyAppServerAllowed(), "默认开启后 app_server 放行跟随 codex_cli_only")
	})
}

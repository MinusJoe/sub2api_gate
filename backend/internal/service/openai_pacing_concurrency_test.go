package service

import (
	"testing"

	"github.com/MACOS-DO/sub4api/internal/config"
	"github.com/stretchr/testify/require"
)

// OpenAI 单账号调度并发上限（gateway.openai_pacing_max_account_concurrency）：
// 默认 0 = 关闭，此时 OpenAISchedulingConcurrency 必须逐值等于 Concurrency，
// 即调度并发与改动前完全一致；配置后才把「不限」或越界的并发压到上限。
func TestAccountOpenAIPacingConcurrency(t *testing.T) {
	t.Cleanup(func() { SetOpenAIPacingMaxConcurrency(0) })

	t.Run("上限 0 不生效", func(t *testing.T) {
		SetOpenAIPacingMaxConcurrency(0)
		for _, concurrency := range []int{-1, 0, 1, 10} {
			account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: concurrency}
			require.Equal(t, concurrency, account.OpenAISchedulingConcurrency(), "未配置上限时必须原样返回 Concurrency")
		}
	})

	t.Run("上限 2 把 10 压到 2", func(t *testing.T) {
		SetOpenAIPacingMaxConcurrency(2)
		account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 10}
		require.Equal(t, 2, account.OpenAISchedulingConcurrency())
	})

	t.Run("Concurrency=0 且上限 2 返回 2", func(t *testing.T) {
		SetOpenAIPacingMaxConcurrency(2)
		account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 0}
		require.Equal(t, 2, account.OpenAISchedulingConcurrency(), "账号「不限」时上限必须接管")
	})

	t.Run("上限 2 且 Concurrency=1 保持 1", func(t *testing.T) {
		SetOpenAIPacingMaxConcurrency(2)
		account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 1}
		require.Equal(t, 1, account.OpenAISchedulingConcurrency(), "低于上限的账号不得被抬高")
	})

	t.Run("负上限按关闭处理", func(t *testing.T) {
		SetOpenAIPacingMaxConcurrency(-5)
		account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 10}
		require.Equal(t, 10, account.OpenAISchedulingConcurrency())
	})

	t.Run("nil 账号安全返回 0", func(t *testing.T) {
		SetOpenAIPacingMaxConcurrency(2)
		var account *Account
		require.Zero(t, account.OpenAISchedulingConcurrency())
	})

	// EffectiveLoadFactor 被 Anthropic 共用，不能被 OpenAI 的 pacing 上限改写。
	t.Run("不改动 EffectiveLoadFactor", func(t *testing.T) {
		SetOpenAIPacingMaxConcurrency(2)
		account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 10}
		require.Equal(t, 10, account.EffectiveLoadFactor())
	})
}

// 装配层接线：applyCodexOutboundConfig 必须把两个行为门的配置发布成进程级快照，
// 并在 cfg==nil 时保持已发布的值（不清空）。
func TestApplyCodexOutboundConfigPublishesBehaviorGates(t *testing.T) {
	t.Cleanup(func() {
		SetCodexCLIOnlyDefault(false)
		SetOpenAIPacingMaxConcurrency(0)
		SetCodexClientVersionFloor("")
		SetOpenAIEgressRegion("")
		SetDefaultOpenAIRequestTimezone("")
	})

	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 10}

	// 未配置（零值配置）时两个门都关闭：默认路径行为不变。
	applyCodexOutboundConfig(&config.Config{})
	require.False(t, CodexCLIOnlyDefault())
	require.False(t, account.IsCodexCLIOnlyEnabled())
	require.Equal(t, 10, account.OpenAISchedulingConcurrency())

	cfg := &config.Config{}
	cfg.Gateway.OpenAICodexRequireOfficialClient = true
	cfg.Gateway.OpenAIPacingMaxAccountConcurrency = 2
	applyCodexOutboundConfig(cfg)

	require.True(t, CodexCLIOnlyDefault(), "装配层必须发布 codex_cli_only 默认值")
	require.True(t, account.IsCodexCLIOnlyEnabled(), "缺失字段的账号跟随已发布的默认值")
	require.Equal(t, 2, account.OpenAISchedulingConcurrency(), "装配层必须发布调度并发上限")

	applyCodexOutboundConfig(nil)
	require.True(t, CodexCLIOnlyDefault(), "nil config 不得清空已发布的默认值")
	require.Equal(t, 2, account.OpenAISchedulingConcurrency(), "nil config 不得清空已发布的上限")
}

// 负载批次使用的并发也必须应用上限，否则会出现「按 10 判定未满、按 2 抢槽失败」。
func TestAccountOpenAISchedulingLoadFactor(t *testing.T) {
	t.Cleanup(func() { SetOpenAIPacingMaxConcurrency(0) })

	loadFactor := 8
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 10, LoadFactor: &loadFactor}

	SetOpenAIPacingMaxConcurrency(0)
	require.Equal(t, 8, account.OpenAISchedulingLoadFactor(), "未配置上限时保持 LoadFactor 语义")

	SetOpenAIPacingMaxConcurrency(3)
	require.Equal(t, 3, account.OpenAISchedulingLoadFactor(), "上限必须压住 LoadFactor")

	account.LoadFactor = nil
	require.Equal(t, 3, account.OpenAISchedulingLoadFactor())
}

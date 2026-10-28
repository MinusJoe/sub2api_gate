package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func openAICreditOverageExtra(fetchedAt time.Time) map[string]any {
	return map[string]any{
		openaiQuotaCreditsKey: map[string]any{
			"credits": map[string]any{
				"has_credits": true,
				"unlimited":   false,
				"balance":     "12.5",
			},
			"fetched_at": float64(fetchedAt.Unix()),
		},
	}
}

func TestMergeOpenAICreditsSnapshotExtraPreservesNil(t *testing.T) {
	require.Nil(t, MergeOpenAICreditsSnapshotExtra(nil, nil))
	snapshot := openAICreditsSnapshot{FetchedAt: 123}
	require.Equal(t, map[string]any{openaiQuotaCreditsKey: snapshot}, MergeOpenAICreditsSnapshotExtra(nil, map[string]any{openaiQuotaCreditsKey: snapshot}))
	incoming := map[string]any{openaiQuotaCreditsKey: "forged", "custom": true}
	merged := MergeOpenAICreditsSnapshotExtra(incoming, nil)
	require.Equal(t, map[string]any{"custom": true}, merged)
	require.Equal(t, "forged", incoming[openaiQuotaCreditsKey], "do not mutate the caller's map")
}

func TestResolveOpenAICreditOverage(t *testing.T) {
	now := time.Now()

	t.Run("开关未开启", func(t *testing.T) {
		extra := openAICreditOverageExtra(now)
		account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: extra}
		state := resolveOpenAICreditOverage(account, now)
		require.False(t, state.Allowed)
		require.Equal(t, openAICreditOverageReasonDisabled, state.Reason)
	})

	t.Run("非 OpenAI 平台不生效", func(t *testing.T) {
		extra := openAICreditOverageExtra(now)
		extra[OpenAICreditOverageExtraKey] = true
		account := &Account{Platform: PlatformAnthropic, Type: AccountTypeOAuth, Extra: extra}
		require.False(t, resolveOpenAICreditOverage(account, now).Allowed)
	})

	t.Run("快照缺失", func(t *testing.T) {
		account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{
			OpenAICreditOverageExtraKey: true,
		}}
		state := resolveOpenAICreditOverage(account, now)
		require.False(t, state.Allowed)
		require.Equal(t, openAICreditOverageReasonSnapshotMissing, state.Reason)
	})

	t.Run("快照过期", func(t *testing.T) {
		extra := openAICreditOverageExtra(now.Add(-openAICreditOverageSnapshotTTL - time.Minute))
		extra[OpenAICreditOverageExtraKey] = true
		account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: extra}
		state := resolveOpenAICreditOverage(account, now)
		require.False(t, state.Allowed)
		require.Equal(t, openAICreditOverageReasonSnapshotStale, state.Reason)
	})

	t.Run("无余额", func(t *testing.T) {
		extra := openAICreditOverageExtra(now)
		extra[OpenAICreditOverageExtraKey] = true
		extra[openaiQuotaCreditsKey] = map[string]any{
			"credits":    map[string]any{"has_credits": false, "unlimited": false, "balance": nil},
			"fetched_at": float64(now.Unix()),
		}
		account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: extra}
		state := resolveOpenAICreditOverage(account, now)
		require.False(t, state.Allowed)
		require.Equal(t, openAICreditOverageReasonNoCredits, state.Reason)
	})

	t.Run("无限余额放行", func(t *testing.T) {
		extra := openAICreditOverageExtra(now)
		extra[OpenAICreditOverageExtraKey] = true
		extra[openaiQuotaCreditsKey] = map[string]any{
			"credits":    map[string]any{"has_credits": true, "unlimited": true, "balance": nil},
			"fetched_at": float64(now.Unix()),
		}
		account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: extra}
		state := resolveOpenAICreditOverage(account, now)
		require.True(t, state.Allowed)
		require.Equal(t, openAICreditOverageReasonUnlimited, state.Reason)
	})

	t.Run("余额为零视为耗尽", func(t *testing.T) {
		extra := openAICreditOverageExtra(now)
		extra[OpenAICreditOverageExtraKey] = true
		extra[openaiQuotaCreditsKey] = map[string]any{
			"credits":    map[string]any{"has_credits": true, "unlimited": false, "balance": "0.00"},
			"fetched_at": float64(now.Unix()),
		}
		account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: extra}
		state := resolveOpenAICreditOverage(account, now)
		require.False(t, state.Allowed)
		require.Equal(t, openAICreditOverageReasonBalanceExhausted, state.Reason)
	})

	t.Run("余额无法解析时交给上游裁决", func(t *testing.T) {
		extra := openAICreditOverageExtra(now)
		extra[OpenAICreditOverageExtraKey] = true
		extra[openaiQuotaCreditsKey] = map[string]any{
			"credits":    map[string]any{"has_credits": true, "unlimited": false, "balance": "unknown"},
			"fetched_at": float64(now.Unix()),
		}
		account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: extra}
		state := resolveOpenAICreditOverage(account, now)
		require.True(t, state.Allowed)
		require.Equal(t, openAICreditOverageReasonBalance, state.Reason)
	})

	t.Run("结构体形态的快照同样可读", func(t *testing.T) {
		balance := "3.00"
		account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{
			OpenAICreditOverageExtraKey: true,
			openaiQuotaCreditsKey: openAICreditsSnapshot{
				Credits:   &OpenAICredits{HasCredits: true, Balance: &balance},
				FetchedAt: now.Unix(),
			},
		}}
		state := resolveOpenAICreditOverage(account, now)
		require.True(t, state.Allowed)
		require.Equal(t, openAICreditOverageReasonBalance, state.Reason)
	})
}

func TestShouldAutoPauseOpenAIAccountByQuota_CreditOverage(t *testing.T) {
	now := time.Now().UTC()
	quotaExtra := func() map[string]any {
		return map[string]any{
			"auto_pause_5h_threshold": 0.8,
			"codex_5h_used_percent":   100.0,
			"codex_usage_updated_at":  now.Format(time.RFC3339),
			"codex_5h_reset_at":       now.Add(time.Hour).Format(time.RFC3339),
		}
	}

	t.Run("窗口用满但余额可用时继续调度", func(t *testing.T) {
		extra := quotaExtra()
		extra[OpenAICreditOverageExtraKey] = true
		for key, value := range openAICreditOverageExtra(now) {
			extra[key] = value
		}
		account := &Account{ID: 8_100_001, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: extra}
		paused, _ := shouldAutoPauseOpenAIAccountByQuota(context.Background(), account)
		require.False(t, paused)
	})

	t.Run("未开余额开关时保持原有暂停", func(t *testing.T) {
		extra := quotaExtra()
		for key, value := range openAICreditOverageExtra(now) {
			extra[key] = value
		}
		account := &Account{ID: 8_100_002, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: extra}
		paused, decision := shouldAutoPauseOpenAIAccountByQuota(context.Background(), account)
		require.True(t, paused)
		require.Equal(t, "5h", decision.window)
	})

	t.Run("余额快照过期时不放行", func(t *testing.T) {
		extra := quotaExtra()
		extra[OpenAICreditOverageExtraKey] = true
		for key, value := range openAICreditOverageExtra(now.Add(-openAICreditOverageSnapshotTTL - time.Minute)) {
			extra[key] = value
		}
		account := &Account{ID: 8_100_003, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: extra}
		paused, _ := shouldAutoPauseOpenAIAccountByQuota(context.Background(), account)
		require.True(t, paused)
	})

	t.Run("开启自动用卡且余额可用时放行但仍通知用卡", func(t *testing.T) {
		svc := &OpenAIQuotaAutoResetService{ctx: context.Background(), queue: make(chan int64, 8)}
		setOpenAIAutoResetNotifier(svc)
		t.Cleanup(func() { clearOpenAIAutoResetNotifier(svc) })

		const accountID int64 = 8_100_004
		t.Cleanup(func() { openAIAutoResetSchedulerNotifiedAt.Delete(accountID) })

		extra := quotaExtra()
		extra[OpenAICreditOverageExtraKey] = true
		extra[OpenAIAutoResetCreditEnabledExtraKey] = true
		extra[OpenAIAutoResetCredit5hThresholdExtraKey] = 1.0
		extra[OpenAIAutoResetCredit7dThresholdExtraKey] = 1.0
		for key, value := range openAICreditOverageExtra(now) {
			extra[key] = value
		}
		account := &Account{ID: accountID, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: extra}
		paused, _ := shouldAutoPauseOpenAIAccountByQuota(context.Background(), account)
		require.False(t, paused)

		select {
		case got := <-svc.queue:
			require.Equal(t, accountID, got)
		case <-time.After(time.Second):
			t.Fatal("期望异步用卡通知仍然发出")
		}
	})
}

//go:build unit

package repository

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/MACOS-DO/sub4api/internal/config"
	"github.com/MACOS-DO/sub4api/internal/service"
	"github.com/stretchr/testify/require"
)

type creditOverageSelectionRepo struct {
	service.AccountRepository
	account service.Account
}

func (r creditOverageSelectionRepo) GetByID(context.Context, int64) (*service.Account, error) {
	copy := r.account
	return &copy, nil
}

func TestSchedulerCacheCreditOverageSelection(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(map[bool]string{true: "enabled", false: "disabled"}[enabled], func(t *testing.T) {
			ctx := context.Background()
			cache := newSchedulerCacheUnit(t)
			now := time.Now()
			account := service.Account{ID: 42, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Status: service.StatusActive, Schedulable: true, Concurrency: 1, Extra: map[string]any{
				service.OpenAICreditOverageExtraKey: enabled,
				"codex_credits_snapshot":            map[string]any{"credits": map[string]any{"has_credits": true, "unlimited": false, "balance": "12.5"}, "fetched_at": now.Unix()},
				"codex_5h_used_percent":             100.0, "auto_pause_5h_threshold": .8,
				"codex_usage_updated_at": now.Format(time.RFC3339), "codex_5h_reset_at": now.Add(time.Hour).Format(time.RFC3339),
			}}
			bucket := service.SchedulerBucket{Platform: service.PlatformOpenAI, Mode: service.SchedulerModeSingle}
			token, err := cache.CaptureBucketWriteToken(ctx, bucket)
			require.NoError(t, err)
			require.NoError(t, cache.SetSnapshot(ctx, bucket, token, []service.Account{account}))
			repo := creditOverageSelectionRepo{account: account}
			cfg := &config.Config{}
			snapshot := service.NewSchedulerSnapshotService(cache, nil, repo, nil, cfg)
			gateway := service.NewOpenAIGatewayService(repo, nil, nil, nil, nil, nil, nil, cfg, snapshot, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
			selected, err := gateway.SelectAccountForModel(ctx, nil, "", "gpt-5.1")
			if enabled {
				require.NoError(t, err)
				require.NotNil(t, selected)
				require.Equal(t, account.ID, selected.ID)
			} else {
				require.Error(t, err)
				require.Nil(t, selected)
			}
		})
	}
}

func TestSchedulerCacheCreditRevisionCannotRegress(t *testing.T) {
	ctx := context.Background()
	cache := newSchedulerCacheUnit(t)
	account := service.Account{ID: 42, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Extra: map[string]any{
		"codex_credits_revision":  "9007199254740993",
		"codex_credits_snapshot":  map[string]any{"credits": nil, "fetched_at": 200},
		"auto_pause_5h_threshold": 0.8,
	}}
	require.NoError(t, cache.SetAccount(ctx, &account))
	account.Extra = map[string]any{
		"codex_credits_revision":  "9007199254740992",
		"codex_credits_snapshot":  map[string]any{"credits": map[string]any{"has_credits": true, "balance": "12.5"}, "fetched_at": 200},
		"auto_pause_5h_threshold": 0.9,
	}
	require.NoError(t, cache.SetAccount(ctx, &account))
	cached, err := cache.GetAccount(ctx, account.ID)
	require.NoError(t, err)
	require.Equal(t, "9007199254740993", cached.Extra["codex_credits_revision"])
	snapshot, ok := cached.Extra["codex_credits_snapshot"].(map[string]any)
	require.True(t, ok)
	require.Nil(t, snapshot["credits"], "an older same-second positive observation must not revive unknown balance")
	require.Equal(t, 0.8, cached.Extra["auto_pause_5h_threshold"], "an older protected record is rejected as a whole, never mixing a new balance into an old identity")
}

func TestSchedulerCacheCreditRevisionNilExtra(t *testing.T) {
	ctx := context.Background()
	cache := newSchedulerCacheUnit(t)
	account := service.Account{ID: 43, Platform: service.PlatformAnthropic, Type: service.AccountTypeAPIKey}
	require.NoError(t, cache.SetAccount(ctx, &account), "nil Extra on an ordinary non-OpenAI account must remain cacheable")
	require.NoError(t, cache.SetAccount(ctx, &account), "an existing JSON null Extra must remain writable")
}

func TestSchedulerCacheCreditRevisionProtectsIdentityAndRebuild(t *testing.T) {
	ctx := context.Background()
	cache := newSchedulerCacheUnit(t)
	current := service.Account{ID: 42, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Credentials: map[string]any{"chatgpt_account_id": "workspace-b"}, Extra: map[string]any{service.OpenAICreditsRevisionExtraKey: "2"}}
	require.NoError(t, cache.SetAccount(ctx, &current))
	old := current
	old.Credentials = map[string]any{"chatgpt_account_id": "workspace-a"}
	old.Extra = map[string]any{service.OpenAICreditsRevisionExtraKey: "1", "codex_credits_snapshot": map[string]any{"credits": map[string]any{"has_credits": true, "balance": "12.5"}, "fetched_at": time.Now().Unix()}}
	bucket := service.SchedulerBucket{Platform: service.PlatformOpenAI, Mode: service.SchedulerModeSingle}
	token, err := cache.CaptureBucketWriteToken(ctx, bucket)
	require.NoError(t, err)
	require.NoError(t, cache.SetSnapshot(ctx, bucket, token, []service.Account{old}))
	full, err := cache.GetAccount(ctx, 42)
	require.NoError(t, err)
	require.Equal(t, "workspace-b", full.GetCredential("chatgpt_account_id"))
	require.NotContains(t, full.Extra, "codex_credits_snapshot")
	meta, hit, err := cache.GetSnapshot(ctx, bucket)
	require.NoError(t, err)
	require.True(t, hit)
	require.Len(t, meta, 1)
	require.Equal(t, "workspace-b", meta[0].GetCredential("chatgpt_account_id"))
	require.Equal(t, "2", meta[0].Extra[service.OpenAICreditsRevisionExtraKey])
	require.NotContains(t, meta[0].Extra, "codex_credits_snapshot")
}

func TestSchedulerCacheKeepsCreditOverage(t *testing.T) {
	for _, tc := range []struct {
		name    string
		enabled bool
		credits any
	}{
		{"usable", true, map[string]any{"has_credits": true, "unlimited": false, "balance": "12.5"}},
		{"disabled", false, map[string]any{"has_credits": false, "unlimited": false, "balance": "0"}},
		{"unknown", true, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			fetchedAt := time.Now().Unix()
			snapshot := map[string]any{"credits": tc.credits, "fetched_at": float64(fetchedAt)}
			account := service.Account{ID: 42, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Status: service.StatusActive, Schedulable: true, Extra: map[string]any{
				service.OpenAICreditOverageExtraKey: tc.enabled, "codex_credits_snapshot": snapshot, "codex_5h_used_percent": 100.0, "auto_pause_5h_threshold": 0.8, "unrelated_payload": "drop",
			}}
			cache := newSchedulerCacheUnit(t)
			bucket := service.SchedulerBucket{GroupID: 8, Platform: service.PlatformOpenAI, Mode: service.SchedulerModeSingle}
			token, err := cache.CaptureBucketWriteToken(ctx, bucket)
			require.NoError(t, err)
			require.NoError(t, cache.SetSnapshot(ctx, bucket, token, []service.Account{account}))
			candidates, hit, err := cache.GetSnapshot(ctx, bucket)
			require.NoError(t, err)
			require.True(t, hit)
			require.Len(t, candidates, 1)
			require.Equal(t, tc.enabled, candidates[0].Extra[service.OpenAICreditOverageExtraKey])
			want, _ := json.Marshal(snapshot)
			got, err := json.Marshal(candidates[0].Extra["codex_credits_snapshot"])
			require.NoError(t, err)
			require.JSONEq(t, string(want), string(got))
			require.NotContains(t, candidates[0].Extra, "unrelated_payload")
			// Balance refreshes update metadata without rebuilding bucket membership.
			account.Extra["codex_credits_snapshot"] = map[string]any{"credits": nil, "fetched_at": float64(fetchedAt + 1)}
			require.NoError(t, cache.SetAccount(ctx, &account))
			refreshed, hit, err := cache.GetSnapshot(ctx, bucket)
			require.NoError(t, err)
			require.True(t, hit)
			require.Len(t, refreshed, 1)
			latest, ok := refreshed[0].Extra["codex_credits_snapshot"].(map[string]any)
			require.True(t, ok)
			require.Nil(t, latest["credits"])
			require.EqualValues(t, fetchedAt+1, latest["fetched_at"])
		})
	}
}

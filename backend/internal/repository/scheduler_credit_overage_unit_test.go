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

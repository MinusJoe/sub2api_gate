package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCreditSnapshotShadowParentIdentityChanges(t *testing.T) {
	now := time.Now()
	parent := &Account{ID: 10, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Credentials: map[string]any{"chatgpt_account_id": "workspace-a", "chatgpt_user_id": "user-a", "access_token": "fixture", "expires_at": now.Add(time.Hour).Format(time.RFC3339)}, Extra: map[string]any{OpenAICreditsRevisionExtraKey: "1"}}
	balance := "12.5"
	snapshot := map[string]any{"credits": map[string]any{"has_credits": true, "balance": balance}, "fetched_at": now.Unix(), "source_account_id": float64(10), "source_identity": OpenAICreditsIdentity(parent), "source_revision": "1"}
	shadow := &Account{ID: 20, ParentAccountID: &parent.ID, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, QuotaDimension: QuotaDimensionSpark, Extra: map[string]any{OpenAICreditOverageExtraKey: true, openaiQuotaCreditsKey: snapshot, "auto_pause_5h_threshold": 0.8, "codex_5h_used_percent": 100.0, "codex_usage_updated_at": now.Format(time.RFC3339), "codex_5h_reset_at": now.Add(time.Hour).Format(time.RFC3339)}}
	lookup := func(int64) *Account { return parent }
	paused, _ := shouldAutoPauseOpenAIAccountByQuota(context.Background(), shadow)
	require.False(t, paused, "a matching shadow balance still bypasses quota pause")
	require.True(t, parentHealthyForShadow(shadow, lookup))
	parent.Credentials["chatgpt_account_id"] = "workspace-b"
	parent.Extra[OpenAICreditsRevisionExtraKey] = "2"
	require.False(t, parentHealthyForShadow(shadow, lookup), "a saved balance from A must not authorize the shadow after its parent becomes B")
	// A to B to A must still reject the first observation.
	parent.Credentials["chatgpt_account_id"] = "workspace-a"
	require.False(t, parentHealthyForShadow(shadow, lookup))
	shadow.Extra["codex_5h_used_percent"] = 10.0
	require.True(t, parentHealthyForShadow(shadow, lookup), "ordinary below-threshold traffic does not depend on old balance provenance")
	snapshot["source_revision"] = "2"
	snapshot["source_identity"] = OpenAICreditsIdentity(parent)
	shadow.Extra["codex_5h_used_percent"] = 100.0
	require.True(t, parentHealthyForShadow(shadow, lookup), "a newly queried matching shadow can use its balance again")
}

func TestDefaultSparkShadowModelMapping(t *testing.T) {
	mapping := defaultSparkShadowModelMapping()

	require.Len(t, mapping, 1, "spark 无 effort 变体，默认只含 base 模型")
	require.Equal(t, "gpt-5.3-codex-spark", mapping["gpt-5.3-codex-spark"], "恒等映射：base 映射到自身")
}

func TestSparkModelVariantsDerivedFromAliases(t *testing.T) {
	got := sparkModelVariants()
	require.ElementsMatch(t, []string{
		"gpt-5.3-codex-spark",
	}, got, "spark 只有 base：effort 变体不存在，已从 codexModelMap 移除")
}

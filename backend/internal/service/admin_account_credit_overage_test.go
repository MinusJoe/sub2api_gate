package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAdminAccountCreditsSnapshotProtection(t *testing.T) {
	for _, present := range []bool{false, true} {
		t.Run(map[bool]string{false: "reject injection", true: "preserve stored snapshot"}[present], func(t *testing.T) {
			account := ticketTestAccount(41)
			latest := map[string]any{"credits": map[string]any{"has_credits": false}, "fetched_at": 200}
			account.Extra = map[string]any{}
			if present {
				account.Extra[openaiQuotaCreditsKey] = latest
			}
			repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{41: account}}
			svc := &adminServiceImpl{accountRepo: repo}
			for _, incoming := range []map[string]any{
				{openaiQuotaCreditsKey: map[string]any{"fetched_at": 100}, OpenAICreditOverageExtraKey: true},
				{OpenAICreditOverageExtraKey: true},
			} {
				updated, err := svc.UpdateAccount(context.Background(), 41, &UpdateAccountInput{Extra: incoming})
				require.NoError(t, err)
				require.Equal(t, true, updated.Extra[OpenAICreditOverageExtraKey])
				if present {
					require.Equal(t, latest, updated.Extra[openaiQuotaCreditsKey])
				} else {
					require.NotContains(t, updated.Extra, openaiQuotaCreditsKey)
				}
			}
			require.NoError(t, svc.UpdateAccountExtra(context.Background(), 41, map[string]any{openaiQuotaCreditsKey: "forged", "custom": true}))
			updates := repo.updates[41]
			require.NotContains(t, updates[len(updates)-1], openaiQuotaCreditsKey)
			_, err := svc.BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{AccountIDs: []int64{41}, Extra: map[string]any{openaiQuotaCreditsKey: "forged", "custom": true}})
			require.NoError(t, err)
			require.NotContains(t, repo.bulkUpdates[0].Extra, openaiQuotaCreditsKey)
		})
	}
}

func TestBuildAccountForCreateDropsCreditsSnapshot(t *testing.T) {
	account, err := buildAccountForCreate(&CreateAccountInput{Platform: PlatformOpenAI, Type: AccountTypeOAuth}, map[string]any{openaiQuotaCreditsKey: "forged", OpenAICreditOverageExtraKey: true})
	require.NoError(t, err)
	require.NotContains(t, account.Extra, openaiQuotaCreditsKey)
	require.Equal(t, true, account.Extra[OpenAICreditOverageExtraKey])
}

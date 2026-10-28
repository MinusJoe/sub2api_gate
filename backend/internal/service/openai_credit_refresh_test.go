package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/MACOS-DO/sub4api/internal/pkg/pagination"
	"github.com/stretchr/testify/require"
)

func newCreditRefreshFixture(credits *OpenAICredits) (*OpenAIQuotaAutoResetService, *autoResetTestAccountRepo, *autoResetTestQuota) {
	account := &Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Extra: map[string]any{OpenAICreditOverageExtraKey: true, "codex_5h_used_percent": 100.0}}
	repo := &autoResetTestAccountRepo{account: account}
	quota := &autoResetTestQuota{usage: &OpenAIQuotaUsage{FetchedAt: time.Now().Unix(), Credits: credits, RateLimitResetCredits: &OpenAIRateLimitResetCredits{AvailableCount: 1}, autoResetCandidates: []openAIAutoResetCreditCandidate{{ID: "must-not-consume"}}}}
	return NewOpenAIQuotaAutoResetService(repo, quota, nil, nil, nil, nil, nil), repo, quota
}

func TestCreditOverageRefreshWithoutResetAuthorization(t *testing.T) {
	balance := "12.5"
	for _, tc := range []struct {
		name    string
		credits *OpenAICredits
	}{
		{"positive", &OpenAICredits{HasCredits: true, Balance: &balance}},
		{"zero", &OpenAICredits{}},
		{"unknown", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, repo, quota := newCreditRefreshFixture(tc.credits)
			require.NoError(t, svc.evaluateAccount(context.Background(), 42))
			snapshot := repo.account.Extra[openaiQuotaCreditsKey].(openAICreditsSnapshot)
			require.Equal(t, tc.credits, snapshot.Credits)
			require.Positive(t, snapshot.FetchedAt)
			require.NotContains(t, repo.account.Extra, OpenAIAutoResetCreditStateExtraKey)
			for i := 0; i < 3; i++ {
				require.NoError(t, svc.evaluateAccount(context.Background(), 42))
			}
			require.EqualValues(t, 1, quota.queryCalls.Load(), "zero and null balances must also be throttled")
			repo.setExtraForTest(openaiQuotaCreditsKey, openAICreditsSnapshot{Credits: tc.credits, FetchedAt: time.Now().Add(-openAIAutoResetSnapshotTTL - time.Minute).Unix()})
			quota.usage.FetchedAt = time.Now().Unix()
			require.NoError(t, svc.evaluateAccount(context.Background(), 42))
			require.EqualValues(t, 2, quota.queryCalls.Load())
			require.Zero(t, quota.resetCalls.Load(), "overage alone must never authorize reset consumption")
		})
	}
}

func TestCreditOverageRefreshFailureBackoff(t *testing.T) {
	svc, repo, quota := newCreditRefreshFixture(nil)
	quota.queryErr = errors.New("upstream unavailable")
	require.Error(t, svc.evaluateAccount(context.Background(), 42))
	require.NoError(t, svc.evaluateAccount(context.Background(), 42))
	require.EqualValues(t, 1, quota.queryCalls.Load())
	require.NotContains(t, repo.account.Extra, openaiQuotaCreditsKey)
	require.NotContains(t, repo.account.Extra, OpenAIAutoResetCreditStateExtraKey)
	svc.creditRefreshRetryAt.Store(int64(42), time.Now().Add(-time.Second))
	quota.queryErr = nil
	require.NoError(t, svc.evaluateAccount(context.Background(), 42))
	require.EqualValues(t, 2, quota.queryCalls.Load())
	require.Zero(t, quota.resetCalls.Load())
}

func TestAutoResetPersistsCreditsWithoutResetDetails(t *testing.T) {
	svc, repo, quota := newCreditRefreshFixture(&OpenAICredits{HasCredits: true})
	repo.setExtraForTest(OpenAIAutoResetCreditEnabledExtraKey, true)
	quota.usage.RateLimitResetCredits = nil
	require.Error(t, svc.evaluateAccount(context.Background(), 42))
	require.Equal(t, quota.usage.Credits, repo.account.Extra[openaiQuotaCreditsKey].(openAICreditsSnapshot).Credits)
	require.Zero(t, quota.resetCalls.Load())
}

func TestAutoResetRefreshesStaleBalanceDespiteFreshNoCreditState(t *testing.T) {
	now := time.Now()
	svc, repo, quota := newResetThresholdTestFixture(t, &OpenAIAutoResetCreditState{
		Status: OpenAIAutoResetStatusNoCredit, CheckedAt: now.UTC().Format(time.RFC3339), ErrorCode: "NO_RESET_CREDIT",
	}, 0)
	repo.setExtraForTest(OpenAICreditOverageExtraKey, true)
	repo.setExtraForTest(openaiQuotaCreditsKey, openAICreditsSnapshot{FetchedAt: now.Add(-3 * time.Hour).Unix()})
	quota.usage.Credits = &OpenAICredits{HasCredits: true}
	quota.usage.FetchedAt = now.Unix()
	require.NoError(t, svc.evaluateAccount(context.Background(), repo.account.ID))
	require.EqualValues(t, 1, quota.queryCalls.Load())
	require.Equal(t, quota.usage.Credits, repo.account.Extra[openaiQuotaCreditsKey].(openAICreditsSnapshot).Credits)
	require.NoError(t, svc.evaluateAccount(context.Background(), repo.account.ID))
	require.EqualValues(t, 1, quota.queryCalls.Load())
	require.Zero(t, quota.resetCalls.Load())
}

func TestCreditSnapshotRefreshCadence(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		name string
		raw  any
		want bool
	}{
		{"missing", nil, true},
		{"null fresh map", map[string]any{"credits": nil, "fetched_at": float64(now.Unix())}, false},
		{"zero fresh struct", openAICreditsSnapshot{Credits: &OpenAICredits{}, FetchedAt: now.Unix()}, false},
		{"future", openAICreditsSnapshot{FetchedAt: now.Add(time.Hour).Unix()}, true},
		{"expired", openAICreditsSnapshot{FetchedAt: now.Add(-openAIAutoResetSnapshotTTL - time.Second).Unix()}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, openAICreditSnapshotNeedsRefresh(map[string]any{openaiQuotaCreditsKey: tc.raw}, now))
		})
	}
}

type creditRefreshScanRepo struct {
	AccountRepository
	accounts []Account
}

func (r *creditRefreshScanRepo) ListWithFilters(context.Context, pagination.PaginationParams, string, string, string, string, int64, string) ([]Account, *pagination.PaginationResult, error) {
	return r.accounts, nil, nil
}

func TestCreditOverageScannerEligibility(t *testing.T) {
	_, base, _ := newCreditRefreshFixture(nil)
	parent := *base.account
	accounts := []Account{parent}
	disabled := parent
	disabled.ID = 43
	disabled.Schedulable = false
	accounts = append(accounts, disabled)
	shadow := parent
	shadow.ID = 44
	shadow.ParentAccountID = &parent.ID
	shadow.QuotaDimension = QuotaDimensionSpark
	accounts = append(accounts, shadow)
	svc := NewOpenAIQuotaAutoResetService(&creditRefreshScanRepo{accounts: accounts}, nil, nil, nil, nil, nil, nil)
	svc.scanEnabledAccounts(context.Background())
	require.Len(t, svc.queue, 1)
	require.Equal(t, parent.ID, <-svc.queue)
	for _, change := range []func(*Account){
		func(a *Account) { a.Status = StatusDisabled },
		func(a *Account) { a.Schedulable = false },
		func(a *Account) { a.Type = AccountTypeAPIKey },
		func(a *Account) { a.Platform = PlatformAnthropic },
	} {
		svc, repo, quota := newCreditRefreshFixture(nil)
		change(repo.account)
		require.NoError(t, svc.evaluateAccount(context.Background(), 42))
		require.Zero(t, quota.queryCalls.Load())
	}
}

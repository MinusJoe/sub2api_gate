package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestQueryUsageCodexCredits(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want string
	}{
		{"decimal balance", `{"credits":{"has_credits":true,"unlimited":false,"balance":"12345678901234567890.0123"}}`, `{"has_credits":true,"unlimited":false,"balance":"12345678901234567890.0123"}`},
		{"zero", `{"credits":{"has_credits":false,"unlimited":false,"balance":"0"}}`, `{"has_credits":false,"unlimited":false,"balance":"0"}`},
		{"unlimited", `{"credits":{"has_credits":false,"unlimited":true,"balance":null}}`, `{"has_credits":false,"unlimited":true,"balance":null}`},
		{"hidden balance", `{"credits":{"has_credits":true,"unlimited":false}}`, `{"has_credits":true,"unlimited":false,"balance":null}`},
		{"absent", `{}`, `null`},
		{"null", `{"credits":null}`, `null`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account := &Account{ID: 100, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive,
				Credentials: map[string]any{"chatgpt_account_id": "test-workspace"}}
			repo := &stubQuotaAccountRepo{accounts: map[int64]*Account{100: account}}
			tokens := &stubQuotaTokenCache{tokens: map[string]string{OpenAITokenCacheKey(account): "test-token"}}
			var paths []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				paths = append(paths, r.URL.Path)
				require.Equal(t, http.MethodGet, r.Method)
				require.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))
				require.Equal(t, "test-workspace", r.Header.Get("ChatGPT-Account-ID"))
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/backend-api/wham/usage" {
					_, _ = w.Write([]byte(tc.body))
					return
				}
				// Reset-card details may be unavailable without losing points.
				http.Error(w, "unavailable", http.StatusServiceUnavailable)
			}))
			defer srv.Close()
			svc := NewOpenAIQuotaService(repo, nil, NewOpenAITokenProvider(repo, tokens, nil), newQuotaRedirectingFactory(srv), nil)
			usage, err := svc.QueryUsage(context.Background(), 100)
			require.NoError(t, err)
			require.Positive(t, usage.FetchedAt)
			encoded, err := json.Marshal(usage.Credits)
			require.NoError(t, err)
			require.JSONEq(t, tc.want, string(encoded))
			require.Equal(t, []string{"/backend-api/wham/usage", "/backend-api/wham/rate-limit-reset-credits"}, paths)
			require.Zero(t, repo.extraUpdateCalls, "read-only query must not write account state")
		})
	}
}

type creditOrderingRepo struct {
	AccountRepository
	mu      sync.Mutex
	account *Account
}

func (r *creditOrderingRepo) GetByID(_ context.Context, id int64) (*Account, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	copy := *r.account
	copy.Credentials = cloneOpenAIAutoResetExtra(copy.Credentials)
	copy.Extra = cloneOpenAIAutoResetExtra(copy.Extra)
	return &copy, nil
}

func (r *creditOrderingRepo) UpdateExtra(_ context.Context, id int64, updates map[string]any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.account.Extra == nil {
		r.account.Extra = map[string]any{}
	}
	for key, value := range updates {
		r.account.Extra[key] = value
	}
	return nil
}

func (r *creditOrderingRepo) UpdateOpenAICreditsSnapshot(_ context.Context, observation OpenAICreditsObservation, updates map[string]any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	current, _ := OpenAICreditsRevision(r.account.Extra)
	expected, _ := OpenAICreditsRevision(observation.Target.Extra)
	if current != expected || OpenAICreditsIdentity(r.account) != OpenAICreditsIdentity(observation.Source) {
		return ErrOpenAICreditsObservationConflict
	}
	next, err := NextOpenAICreditsRevision(r.account.Extra)
	if err != nil {
		return err
	}
	if r.account.Extra == nil {
		r.account.Extra = map[string]any{}
	}
	for key, value := range updates {
		r.account.Extra[key] = value
	}
	r.account.Extra[OpenAICreditsRevisionExtraKey] = next
	return nil
}

func (r *stubQuotaAccountRepo) UpdateOpenAICreditsSnapshot(ctx context.Context, observation OpenAICreditsObservation, updates map[string]any) error {
	return r.UpdateExtra(ctx, observation.Target.ID, updates)
}

type creditRotatingTokenCache struct {
	stubQuotaTokenCache
	repo       *creditOrderingRepo
	switchUser bool
}

func (c *creditRotatingTokenCache) GetAccessToken(context.Context, string) (string, error) {
	c.repo.mu.Lock()
	defer c.repo.mu.Unlock()
	credentials := cloneOpenAIAutoResetExtra(c.repo.account.Credentials)
	credentials["access_token"] = "rotated-token"
	if c.switchUser {
		credentials["chatgpt_user_id"] = "user-b"
		credentials["chatgpt_account_id"] = "workspace-b"
	}
	c.repo.account.Credentials = credentials
	return "rotated-token", nil
}

func TestCreditSnapshotTokenPreparation(t *testing.T) {
	for _, switched := range []bool{false, true} {
		t.Run(map[bool]string{false: "same-user token rotation", true: "workspace changed in token acquisition"}[switched], func(t *testing.T) {
			account := &Account{ID: 100, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"chatgpt_account_id": "workspace-a", "chatgpt_user_id": "user-a", "access_token": "old-token"}}
			repo := &creditOrderingRepo{account: account}
			cache := &creditRotatingTokenCache{repo: repo, switchUser: switched}
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				require.Equal(t, "workspace-a", r.Header.Get("ChatGPT-Account-ID"))
				require.Equal(t, "Bearer rotated-token", r.Header.Get("Authorization"))
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"credits":{"has_credits":false},"available_count":0}`))
			}))
			defer srv.Close()
			svc := NewOpenAIQuotaService(repo, nil, NewOpenAITokenProvider(repo, cache, nil), newQuotaRedirectingFactory(srv), nil)
			usage, err := svc.QueryUsage(context.Background(), 100)
			if switched {
				require.ErrorIs(t, err, ErrOpenAICreditsObservationConflict)
				require.Zero(t, calls.Load())
			} else {
				require.NoError(t, err)
				require.NoError(t, svc.CacheCreditsSnapshot(context.Background(), 100, usage))
				require.EqualValues(t, 2, calls.Load())
			}
		})
	}
}

func TestCreditSnapshotRejectsDelayedObservation(t *testing.T) {
	for _, scenario := range []string{"same-second exhausted observation", "identity changed during request"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			account := &Account{ID: 100, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Credentials: map[string]any{"chatgpt_account_id": "workspace-a"}}
			repo := &creditOrderingRepo{account: account}
			tokens := &stubQuotaTokenCache{tokens: map[string]string{OpenAITokenCacheKey(account): "fixture-token"}}
			blocked, release := make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			var usageCalls, detailCalls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/backend-api/wham/usage" {
					if usageCalls.Add(1) == 1 {
						_, _ = w.Write([]byte(`{"credits":{"has_credits":true,"balance":"12.5"}}`))
					} else {
						_, _ = w.Write([]byte(`{"credits":{"has_credits":false,"balance":"0"}}`))
					}
					return
				}
				if detailCalls.Add(1) == 1 {
					close(blocked)
					<-release
				}
				_, _ = w.Write([]byte(`{"available_count":0,"credits":[]}`))
			}))
			defer func() { unblock(); srv.Close() }()
			svc := NewOpenAIQuotaService(repo, nil, NewOpenAITokenProvider(repo, tokens, nil), newQuotaRedirectingFactory(srv), nil)
			type outcome struct {
				usage *OpenAIQuotaUsage
				err   error
			}
			done := make(chan outcome, 1)
			go func() { usage, err := svc.QueryUsage(ctx, 100); done <- outcome{usage, err} }()
			select {
			case <-blocked:
			case <-time.After(5 * time.Second):
				t.Fatal("fixture query did not reach blocked detail read")
			}
			if scenario == "same-second exhausted observation" {
				newer, err := svc.QueryUsage(ctx, 100)
				require.NoError(t, err)
				newer.FetchedAt = 123
				require.NoError(t, svc.CacheCreditsSnapshot(ctx, 100, newer))
			} else {
				repo.mu.Lock()
				repo.account.Credentials = map[string]any{"chatgpt_account_id": "workspace-b"}
				repo.mu.Unlock()
			}
			unblock()
			var old outcome
			select {
			case old = <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("fixture query did not finish")
			}
			require.NoError(t, old.err)
			// Equal seconds deliberately remove timestamp ordering as an escape hatch.
			old.usage.FetchedAt = 123
			require.Error(t, svc.CacheCreditsSnapshot(ctx, 100, old.usage), "a delayed observation must report that it was not persisted")
			current, err := repo.GetByID(ctx, 100)
			require.NoError(t, err)
			if scenario == "same-second exhausted observation" {
				snapshot, ok := current.Extra[openaiQuotaCreditsKey].(openAICreditsSnapshot)
				require.True(t, ok)
				require.Equal(t, int64(123), snapshot.FetchedAt)
				require.False(t, snapshot.Credits.HasCredits)
			} else {
				require.NotContains(t, current.Extra, openaiQuotaCreditsKey)
			}
		})
	}
}

func TestCacheCodexCreditsSnapshot(t *testing.T) {
	ctx := context.Background()
	repo := &stubQuotaAccountRepo{}
	svc := &OpenAIQuotaService{accountRepo: repo}
	balance := "1200.50"
	usage := &OpenAIQuotaUsage{FetchedAt: 123, Credits: &OpenAICredits{HasCredits: true, Balance: &balance}, creditsObservation: &OpenAICreditsObservation{Target: &Account{ID: 200}, Source: &Account{ID: 100}}}

	// No reset-card details are required to persist the balance on the queried row.
	require.NoError(t, svc.CacheCreditsSnapshot(ctx, 200, usage))
	encoded, err := json.Marshal(repo.extraUpdates[200][openaiQuotaCreditsKey])
	require.NoError(t, err)
	require.JSONEq(t, `{"credits":{"has_credits":true,"unlimited":false,"balance":"1200.50"},"fetched_at":123,"source_account_id":100,"source_identity":"`+OpenAICreditsIdentity(usage.creditsObservation.Source)+`","source_revision":"0"}`, string(encoded))
	require.NotContains(t, repo.extraUpdates, int64(100), "a shadow row must not overwrite its parent")
	require.NotContains(t, repo.extraUpdates[200], openaiQuotaResetCreditsKey)

	// Missing credits from a successful read invalidate the previous balance.
	require.NoError(t, svc.CacheCreditsSnapshot(ctx, 200, &OpenAIQuotaUsage{FetchedAt: 456, creditsObservation: usage.creditsObservation}))
	encoded, err = json.Marshal(repo.extraUpdates[200][openaiQuotaCreditsKey])
	require.NoError(t, err)
	require.JSONEq(t, `{"credits":null,"fetched_at":456,"source_account_id":100,"source_identity":"`+OpenAICreditsIdentity(usage.creditsObservation.Source)+`","source_revision":"0"}`, string(encoded))

	require.Error(t, svc.CacheCreditsSnapshot(ctx, 200, nil))
	require.Equal(t, 2, repo.extraUpdateCalls)
	repo.extraUpdateErr = errors.New("database unavailable")
	require.ErrorContains(t, svc.CacheCreditsSnapshot(ctx, 200, usage), "database unavailable")
}

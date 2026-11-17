package service

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/MACOS-DO/sub4api/internal/config"
	"github.com/stretchr/testify/require"
)

type codexParticipationRepo struct {
	AccountRepository
	account *Account
	scans   chan struct{}
}

func (r *codexParticipationRepo) GetByID(context.Context, int64) (*Account, error) {
	return r.account, nil
}
func (r *codexParticipationRepo) UpdateExtra(_ context.Context, _ int64, values map[string]any) error {
	for key, value := range values {
		r.account.Extra[key] = value
	}
	return nil
}
func (r *codexParticipationRepo) ListByPlatform(context.Context, string) ([]Account, error) {
	if r.scans != nil {
		select {
		case r.scans <- struct{}{}:
		default:
		}
	}
	return []Account{*r.account}, nil
}

func TestCodexTicketParticipationCanBeRestoredWhenRuntimeDisabled(t *testing.T) {
	account := ticketTestAccount(71)
	account.Status = "disabled"
	account.Extra = map[string]any{codexTicketAccountEnabledKey: false, codexTicketModelsEnabledKey: map[string]any{"gpt-6-astra": false, "gpt-5.6-sol": false}, "codex_allow_without_ticket": false}
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: false}, nil)
	svc.accountRepo = &codexParticipationRepo{account: account}
	require.NoError(t, svc.SetCodexTicketParticipation(context.Background(), account.ID, false, map[string]bool{"gpt-6-astra": true, "gpt-5.6-sol": false}))
	require.False(t, CodexTicketHarvestEnabled(account, "gpt-6-astra"))
	require.NoError(t, svc.SetCodexTicketParticipation(context.Background(), account.ID, true, map[string]bool{"gpt-6-astra": true, "gpt-5.6-sol": false}))
	require.True(t, CodexTicketHarvestEnabled(account, "gpt-6-astra"))
	require.False(t, CodexTicketHarvestEnabled(account, "gpt-5.6-sol"))
	require.Equal(t, false, account.Extra["codex_allow_without_ticket"])
}

func TestCodexTicketHarvesterNotificationRescansWithoutBypassingParticipation(t *testing.T) {
	account := ticketTestAccount(71)
	account.Extra = map[string]any{codexTicketAccountEnabledKey: false}
	repo := &codexParticipationRepo{account: account, scans: make(chan struct{}, 10)}
	upstream := &httpUpstreamRecorder{}
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: true, HarvestProbeIntervalSeconds: 3600}, upstream)
	svc.accountRepo = repo
	svc.StartOpenAICodexTicketHarvester()
	defer svc.StopOpenAICodexTicketHarvester()
	select {
	case <-repo.scans:
	case <-time.After(time.Second):
		t.Fatal("initial scan missing")
	}
	svc.notifyOpenAICodexTicketHarvester()
	select {
	case <-repo.scans:
	case <-time.After(time.Second):
		t.Fatal("notification did not wake the scheduler")
	}
	svc.StopOpenAICodexTicketHarvester()
	require.Empty(t, upstream.requests)
}

func TestCodexTicketHarvestStillHonorsRuntimeGates(t *testing.T) {
	for _, gate := range []string{"global", "account", "models", "inactive", "rate limit"} {
		t.Run(gate, func(t *testing.T) {
			account := ticketTestAccount(71)
			account.Extra = map[string]any{}
			cfg := config.OpenAICodexTicketConfig{Enabled: true}
			switch gate {
			case "global":
				cfg.Enabled = false
			case "account":
				account.Extra[codexTicketAccountEnabledKey] = false
			case "models":
				choices := map[string]bool{}
				models, err := ModelTraceTicketModels()
				require.NoError(t, err)
				for _, model := range models {
					choices[model] = false
				}
				account.Extra[codexTicketModelsEnabledKey] = choices
			case "inactive":
				account.Status = "disabled"
			case "rate limit":
				until := time.Now().Add(time.Hour)
				account.RateLimitResetAt = &until
			}
			upstream := &httpUpstreamRecorder{}
			svc := ticketTestService(t, cfg, upstream)
			svc.accountRepo = &codexParticipationRepo{account: account}
			svc.refreshOpenAICodexTickets(context.Background())
			_, err := svc.ManualCodexTicketHarvest(context.Background(), account.ID, "gpt-6-astra")
			require.Error(t, err)
			require.Empty(t, upstream.requests)
		})
	}
}

func TestCodexTicketParticipationNeverBlocksMissingTicketRequests(t *testing.T) {
	account := ticketTestAccount(71)
	account.Extra = map[string]any{"codex_allow_without_ticket": false}
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: true, FailClosed: true}, nil) //nolint:staticcheck // 显式覆盖/读取废弃字段以验证其不再生效
	svc.accountRepo = &codexParticipationRepo{account: account}
	ctx := context.Background()
	models := map[string]bool{"gpt-6-astra": false, "gpt-5.6-sol": true}

	for _, enabled := range []bool{false, true, false} {
		require.NoError(t, svc.SetCodexTicketParticipation(ctx, account.ID, enabled, models))
		require.False(t, svc.isOpenAIAccountRequestRuntimeBlocked(account, "gpt-5.6-sol", false))
		projected := ticketTestAccount(account.ID)
		projected.SchedulerTicketProjection = true
		projected.Extra = map[string]any{
			codexTicketAccountEnabledKey: enabled, "codex_allow_without_ticket": false,
			CodexTicketReadyModelsExtraKey: map[string]bool{}, codexTicketModelsEnabledKey: models,
		}
		require.False(t, svc.isOpenAIAccountRequestRuntimeBlocked(projected, "gpt-5.6-sol", false))
		_, err := svc.applyOpenAICodexTicketWithGeneration(ctx, account, "gpt-5.6-sol", http.Header{})
		require.NoError(t, err)
		for _, status := range OpenAICodexTicketStatuses(account, svc.openAICodexTicketConfig(), time.Now()) {
			require.False(t, status.Blocked)
		}
		require.False(t, svc.isOpenAIAccountRequestRuntimeBlocked(account, "gpt-6-astra", false))
		require.Equal(t, false, account.Extra["codex_allow_without_ticket"], "the saved policy must not change")
		require.Equal(t, map[string]any{"gpt-6-astra": false, "gpt-5.6-sol": true}, account.Extra[codexTicketModelsEnabledKey])
	}
}

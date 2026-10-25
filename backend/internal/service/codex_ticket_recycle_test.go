package service

import (
	"net/http"
	"testing"
	"time"

	"github.com/MACOS-DO/sub4api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func recycleTestService(t *testing.T, cfg config.OpenAICodexTicketConfig, account *Account) (*OpenAIGatewayService, *codexParticipationRepo) {
	t.Helper()
	repo := &codexParticipationRepo{account: account}
	svc := ticketTestService(t, cfg, nil)
	svc.accountRepo = repo
	return svc, repo
}

func recycleTestContext() *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(nil)
	c.Set(OpsUpstreamModelKey, "gpt-6-astra")
	return c
}

func recycleHeader(state string) http.Header {
	h := http.Header{}
	h.Set(openAICodexTurnStateHeader, state)
	h.Add("Set-Cookie", "__oailb=old; Path=/; Secure")
	return h
}

func storedTicket(account *Account, model string) *openAICodexTicket {
	raw := account.Extra[openAICodexTicketExtraKey(model)]
	return parseOpenAICodexTicketFromAny(account.ID, model, raw)
}

func TestCodexTicketRecycleStoresBusinessResponseTicket(t *testing.T) {
	account := ticketTestAccount(91)
	account.Extra = map[string]any{}
	svc, _ := recycleTestService(t, config.OpenAICodexTicketConfig{Enabled: true, TurnStateMode: openAICodexTurnStateModeHarvest, TargetLength: 780}, account)
	c := recycleTestContext()
	state := fakeCodexTicketState(780)
	svc.captureOpenAICodexTicketFromBusinessResponse(c, account, recycleHeader(state))
	ticket := storedTicket(account, "gpt-6-astra")
	require.NotNil(t, ticket)
	require.Equal(t, state, ticket.State)
	require.Equal(t, "business_response_v1", ticket.VerificationMethod)
	require.Equal(t, len(state), ticket.Length)
	require.True(t, ticket.usable(time.Now(), 780, false, time.Hour))
}

func TestCodexTicketRecycleIsDebouncedPerAccountModel(t *testing.T) {
	account := ticketTestAccount(92)
	account.Extra = map[string]any{}
	svc, _ := recycleTestService(t, config.OpenAICodexTicketConfig{Enabled: true, TurnStateMode: openAICodexTurnStateModeHarvest, TargetLength: 780}, account)
	c := recycleTestContext()
	first := fakeCodexTicketState(780)
	svc.captureOpenAICodexTicketFromBusinessResponse(c, account, recycleHeader(first))
	require.NotNil(t, storedTicket(account, "gpt-6-astra"))
	second := openAICodexTicketStatePrefix + "second" + first[len(openAICodexTicketStatePrefix)+len("second"):]
	svc.captureOpenAICodexTicketFromBusinessResponse(c, account, recycleHeader(second))
	require.Equal(t, first, storedTicket(account, "gpt-6-astra").State, "60s 内不得覆盖 generation")
}

func TestCodexTicketRecycleSkippedOutsideHarvestMode(t *testing.T) {
	for _, mode := range []string{openAICodexTurnStateModeRelay, openAICodexTurnStateModeOff} {
		account := ticketTestAccount(93)
		account.Extra = map[string]any{}
		svc, _ := recycleTestService(t, config.OpenAICodexTicketConfig{Enabled: true, TurnStateMode: mode, TargetLength: 780}, account)
		svc.captureOpenAICodexTicketFromBusinessResponse(recycleTestContext(), account, recycleHeader(fakeCodexTicketState(780)))
		require.Nil(t, storedTicket(account, "gpt-6-astra"), "mode=%s 不应写库", mode)
	}
}

func TestCodexTicketRecycleRejectsInvalidShape(t *testing.T) {
	account := ticketTestAccount(94)
	account.Extra = map[string]any{}
	svc, _ := recycleTestService(t, config.OpenAICodexTicketConfig{Enabled: true, TurnStateMode: openAICodexTurnStateModeHarvest, TargetLength: 780}, account)
	svc.captureOpenAICodexTicketFromBusinessResponse(recycleTestContext(), account, recycleHeader("short-state"))
	require.Nil(t, storedTicket(account, "gpt-6-astra"))
}

func TestCodexTicketRecycleRequiresOutboundModel(t *testing.T) {
	account := ticketTestAccount(95)
	account.Extra = map[string]any{}
	svc, _ := recycleTestService(t, config.OpenAICodexTicketConfig{Enabled: true, TurnStateMode: openAICodexTurnStateModeHarvest, TargetLength: 780}, account)
	c := recycleTestContext()
	c.Set(OpsUpstreamModelKey, "")
	svc.captureOpenAICodexTicketFromBusinessResponse(c, account, recycleHeader(fakeCodexTicketState(780)))
	require.Nil(t, storedTicket(account, "gpt-6-astra"))
}

func TestCodexTicketProbeDisabledSkipsHarvest(t *testing.T) {
	account := ticketTestAccount(96)
	account.Extra = map[string]any{codexTicketAccountEnabledKey: true}
	upstream := &httpUpstreamRecorder{}
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: true, ProbeEnabled: false}, upstream)
	svc.accountRepo = &codexParticipationRepo{account: account}
	svc.StartOpenAICodexTicketHarvester()
	time.Sleep(100 * time.Millisecond)
	svc.StopOpenAICodexTicketHarvester()
	require.Empty(t, upstream.requests)
}

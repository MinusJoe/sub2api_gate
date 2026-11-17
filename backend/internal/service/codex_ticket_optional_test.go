package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/MACOS-DO/sub4api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestCodexMissingTicketDoesNotBlockSchedulingOrRequest(t *testing.T) {
	account := ticketTestAccount(901)
	account.Schedulable, account.Concurrency = true, 1
	account.Extra = map[string]any{"codex_allow_without_ticket": false}
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: true, FailClosed: true}, nil) //nolint:staticcheck // 显式覆盖/读取废弃字段以验证其不再生效
	require.False(t, svc.isOpenAIAccountRequestRuntimeBlocked(account, "gpt-6-astra", false))
	headers := http.Header{"X-Codex-Turn-State": {"client-state"}}
	ticket, err := svc.applyOpenAICodexTicketWithGeneration(context.Background(), account, "gpt-6-astra", headers)
	require.NoError(t, err)
	require.Nil(t, ticket)
	require.Equal(t, "client-state", headers.Get(openAICodexTurnStateHeader))
	for _, status := range OpenAICodexTicketStatuses(account, svc.openAICodexTicketConfig(), time.Now()) {
		require.False(t, status.Blocked)
	}
	// Ticket admission removal must not bypass real account cooldowns.
	until := time.Now().Add(time.Minute)
	svc.openaiAccountRuntimeBlockUntil.Store(account.ID, until)
	account.OverloadUntil = &until
	require.True(t, svc.isOpenAIAccountRequestRuntimeBlocked(account, "gpt-6-astra", false))
}

func TestCodexOptionalTicketLookupFailureLeavesClientHeaders(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  json.RawMessage
		err  error
	}{
		{name: "missing", raw: json.RawMessage(`null`)},
		{name: "lookup failure", err: errors.New("storage unavailable")},
		{name: "malformed", raw: json.RawMessage(`{"state":`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account := ticketTestAccount(902)
			svc := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: true, FailClosed: true}, nil) //nolint:staticcheck // 显式覆盖/读取废弃字段以验证其不再生效
			svc.openaiCodexTicketLifecycle = &codexTicketLookupStub{raw: tc.raw, err: tc.err}
			headers := http.Header{"X-Codex-Turn-State": {"client-state"}, "Cookie": {"client=cookie"}}
			ticket, err := svc.applyOpenAICodexTicketWithGeneration(context.Background(), account, "gpt-6-astra", headers)
			require.NoError(t, err)
			require.Nil(t, ticket)
			require.Equal(t, "client-state", headers.Get(openAICodexTurnStateHeader))
			require.Equal(t, "client=cookie", headers.Get("Cookie"))
		})
	}
}

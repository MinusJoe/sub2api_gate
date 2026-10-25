package service

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/MACOS-DO/sub4api/internal/config"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func fakeCodexTicketState(length int) string {
	if length < len(openAICodexTicketStatePrefix) {
		return strings.Repeat("A", length)
	}
	return openAICodexTicketStatePrefix + strings.Repeat("B", length-len(openAICodexTicketStatePrefix))
}

func ticketTestAccount(id int64) *Account {
	return &Account{ID: id, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive,
		Credentials: map[string]any{"access_token": "tok", "chatgpt_account_id": "acc-1"}}
}

func ticketTestService(t *testing.T, cfg config.OpenAICodexTicketConfig, upstream HTTPUpstream) *OpenAIGatewayService {
	t.Helper()
	return &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{OpenAICodexTicket: cfg}}, httpUpstream: upstream}
}

func verifiedTicket(account *Account, model, state, cookie string) *openAICodexTicket {
	return &openAICodexTicket{AccountID: account.ID, Model: model, State: state, Cookie: cookie,
		Length: len(state), GenerationID: uuid.NewString(), VerificationMethod: "modeltrace_v1",
		FingerprintCommit: modelTraceFallbackCommit, CapturedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}
}

func TestCodexTicketLegacyLengthOnlyIsNotReusable(t *testing.T) {
	account := ticketTestAccount(41)
	account.Extra = map[string]any{openAICodexTicketExtraKey("gpt-6-astra"): &openAICodexTicket{
		AccountID: account.ID, Model: "gpt-6-astra", State: fakeCodexTicketState(292), Length: 292, CapturedAt: time.Now(),
	}}
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: true, FailClosed: true}, nil)
	headers := http.Header{}
	require.ErrorIs(t, svc.applyOpenAICodexTicket(context.Background(), account, "gpt-6-astra", headers), ErrOpenAICodexTicketUnavailable)
	require.Empty(t, headers.Get(openAICodexTurnStateHeader))
}

func TestCodexTicketCookieOnlyIsNotInjectable(t *testing.T) {
	account := ticketTestAccount(41)
	account.Extra = map[string]any{openAICodexTicketExtraKey("gpt-6-astra"): verifiedTicket(account, "gpt-6-astra", "", "session=old")}
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: true, FailClosed: true}, nil)
	headers := http.Header{}
	headers.Set(openAICodexTurnStateHeader, "client-old")
	// cookie-only 票不得伪造「有 __oailb、无 turn-state」组合：fail_closed 下直接拒绝。
	require.ErrorIs(t, svc.applyOpenAICodexTicket(context.Background(), account, "gpt-6-astra", headers), ErrOpenAICodexTicketUnavailable)
	require.Equal(t, "client-old", headers.Get(openAICodexTurnStateHeader))
	require.Empty(t, headers.Get("Cookie"))
}

func TestCodexTicketExpiredCaptureIsNotInjectable(t *testing.T) {
	account := ticketTestAccount(41)
	ticket := verifiedTicket(account, "gpt-6-astra", fakeCodexTicketState(292), "")
	ticket.CapturedAt = time.Now().Add(-7 * 24 * time.Hour)
	ticket.ExpiresAt = time.Now().Add(-6 * 24 * time.Hour)
	account.Extra = map[string]any{openAICodexTicketExtraKey(ticket.Model): ticket}
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: true, FailClosed: true, TargetLength: 780}, nil)
	headers := http.Header{}
	require.ErrorIs(t, svc.applyOpenAICodexTicket(context.Background(), account, ticket.Model, headers), ErrOpenAICodexTicketUnavailable)
	require.Empty(t, headers.Get(openAICodexTurnStateHeader))
}

func TestCodexTicketIsolationByRawModel(t *testing.T) {
	account := ticketTestAccount(41)
	account.Extra = map[string]any{openAICodexTicketExtraKey("gpt-6-astra"): verifiedTicket(account, "gpt-6-astra", fakeCodexTicketState(292), "")}
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: true, FailClosed: true}, nil)
	oldModelHeaders := http.Header{}
	require.NoError(t, svc.applyOpenAICodexTicket(context.Background(), account, "gpt-5.5", oldModelHeaders))
	require.Empty(t, oldModelHeaders.Get(openAICodexTurnStateHeader))
	other := ticketTestAccount(42)
	require.ErrorIs(t, svc.applyOpenAICodexTicket(context.Background(), other, "gpt-6-astra", http.Header{}), ErrOpenAICodexTicketUnavailable)
}

// 升级场景：存量 enabled=true + fail_closed=true 部署，turn_state_mode 升级后默认
// relay（probe 默认关闭、业务回收只在 harvest 写库）。存票到达 usable 强制有效期后，
// relay/off 模式不得再把门控模型账号锁死在调度器外——注入侧在 relay/off 本来就
// 直接放行（applyOpenAICodexTicketWithGeneration），调度阻断必须同口径。
func TestOpenAICodexTicketBlocksAccountFollowsTurnStateMode(t *testing.T) {
	account := ticketTestAccount(43)
	account.Extra = map[string]any{"codex_allow_without_ticket": false}
	expired := verifiedTicket(account, "gpt-6-astra", fakeCodexTicketState(780), "")
	expired.CapturedAt = time.Now().Add(-2 * time.Hour)
	expired.ExpiresAt = time.Now().Add(-time.Hour)
	account.Extra[openAICodexTicketExtraKey("gpt-6-astra")] = expired

	// relay（新默认）：不阻断，缺票有自动恢复语义以外的放行渠道。
	relay := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: true, FailClosed: true, TurnStateMode: "relay"}, nil)
	require.False(t, relay.openAICodexTicketBlocksAccount(account, "gpt-6-astra"), "relay 模式不注入票，缺票不得阻断调度")

	// off：同样不注入、不阻断。
	off := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: true, FailClosed: true, TurnStateMode: "off"}, nil)
	require.False(t, off.openAICodexTicketBlocksAccount(account, "gpt-6-astra"))

	// harvest：阻断语义保持不变（票到期且 fail_closed 时账号不可调度）。
	harvest := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: true, FailClosed: true, TurnStateMode: "harvest"}, nil)
	require.True(t, harvest.openAICodexTicketBlocksAccount(account, "gpt-6-astra"), "harvest 模式票到期仍须阻断")

	// 零值配置（未经过 viper 默认值的测试/工具构造）保持 harvest 旧语义。
	legacy := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: true, FailClosed: true}, nil)
	require.True(t, legacy.openAICodexTicketBlocksAccount(account, "gpt-6-astra"))
}

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWebWSModeRouterEnabledWithoutEnvironmentChange(t *testing.T) {
	gateway, account := nativeWSTestServiceAccount()
	gateway.cfg.Gateway.OpenAIWS.ModeRouterV2Enabled = false
	raw := "true"
	settings, _, _ := newWSModeRouterTestSettings(false, &raw)
	gateway.settingService = settings
	require.NoError(t, gateway.validateOpenAINativeWSOnly(account))
	require.True(t, gateway.isOpenAIAccountTransportCompatible(account, OpenAIUpstreamTransportResponsesWebsocketV2Ingress))
	require.Equal(t, OpenAIUpstreamTransportResponsesWebsocketV2, gateway.getOpenAIWSProtocolResolver().Resolve(account).Transport)
	require.False(t, gateway.cfg.Gateway.OpenAIWS.ModeRouterV2Enabled)
	for _, disabled := range []string{"global", "force_http", "v2", "type", "account_force_http", "account_off"} {
		t.Run(disabled, func(t *testing.T) {
			s, a := nativeWSTestServiceAccount()
			s.cfg.Gateway.OpenAIWS.ModeRouterV2Enabled = false
			s.settingService = settings
			switch disabled {
			case "global":
				s.cfg.Gateway.OpenAIWS.Enabled = false
			case "force_http":
				s.cfg.Gateway.OpenAIWS.ForceHTTP = true
			case "v2":
				s.cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = false
			case "type":
				s.cfg.Gateway.OpenAIWS.APIKeyEnabled = false
			case "account_force_http":
				a.Extra["openai_ws_force_http"] = true
			case "account_off":
				a.Extra["openai_apikey_responses_websockets_v2_mode"] = OpenAIWSIngressModeOff
			}
			require.Error(t, s.validateOpenAINativeWSOnly(a))
			require.NotEqual(t, OpenAIUpstreamTransportResponsesWebsocketV2, s.getOpenAIWSProtocolResolver().Resolve(a).Transport)
		})
	}
}

func TestWebWSModeRouterRequestAndPoolSnapshotsSurviveSave(t *testing.T) {
	gateway, account := nativeWSTestServiceAccount()
	gateway.cfg.Gateway.OpenAIWS.ModeRouterV2Enabled = false
	settings, _, _ := newWSModeRouterTestSettings(false, nil)
	gateway.settingService = settings
	enabled := true
	_, err := settings.SetOpenAIWSModeRouterSettings(context.Background(), &enabled)
	require.NoError(t, err)
	c, _ := newFastWSBridgeTestGinContext(t)
	gateway.BindOpenAIWSModeRouterSnapshot(c)
	old := gateway.openAIWSModeRouterRequestContext(context.Background(), c)
	enabled = false
	_, err = settings.SetOpenAIWSModeRouterSettings(context.Background(), &enabled)
	require.NoError(t, err)
	require.True(t, gateway.openAIWSModeRouterSnapshot(old).enabled)
	require.True(t, gateway.openAIWSModeRouterSnapshot(gateway.openAIWSModeRouterRequestContext(context.Background(), c)).enabled)
	require.False(t, gateway.openAIWSModeRouterSnapshot(context.Background()).enabled)
	account.Extra["openai_apikey_responses_websockets_v2_mode"] = OpenAIWSIngressModeHTTPBridge
	delete(account.Extra, OpenAINativeWSOnlyExtraKey)
	account.Extra["openai_apikey_responses_websockets_v2_enabled"] = true
	require.Equal(t, OpenAIUpstreamTransportHTTPSSE, gateway.getOpenAIWSProtocolResolver(old).Resolve(account).Transport)
	require.Equal(t, OpenAIUpstreamTransportResponsesWebsocketV2, gateway.getOpenAIWSProtocolResolver().Resolve(account).Transport)
	pool := newOpenAIWSConnPool(gateway.cfg)
	defer pool.Close()
	account.Concurrency = 0
	snapshot := gateway.openAIWSModeRouterSnapshot(old)
	req := cloneOpenAIWSAcquireRequest(openAIWSAcquireRequest{Account: account, ModeRouter: &snapshot})
	snapshot.enabled = false
	require.True(t, req.ModeRouter.enabled, "pool bookkeeping must own an immutable snapshot")
	require.Zero(t, pool.effectiveMaxConnsByAccount(account, req.ModeRouter))
	legacy := openAIWSModeRouterSnapshot{valid: true}
	require.Positive(t, pool.effectiveMaxConnsByAccount(account, &legacy))
	require.Zero(t, pool.effectiveMaxConnsByAccount(account, &openAIWSModeRouterSnapshot{}))
}

func TestWebWSModeRouterUnavailableDoesNotUseGroupFastOverride(t *testing.T) {
	for _, messages := range []bool{false, true} {
		t.Run(map[bool]string{false: "responses", true: "messages"}[messages], func(t *testing.T) {
			svc, upstream, dialer := newFastWSBridgeTestService(t, newOpenAIWSV2TestConfig(), newAnthropicMessagesBridgeCaptureConn(t))
			useAnthropicMessagesBridgeSSEUpstream(upstream)
			repo := &wsModeRouterRepo{openAIFastPolicyRepoStub: openAIFastPolicyRepoStub{values: map[string]string{SettingKeyOpenAIFastBridgeSettings: `{"enabled":true,"scope":"all"}`}}, readErr: errors.New("database unavailable")}
			svc.settingService = NewSettingService(repo, svc.cfg)
			require.True(t, svc.openAIFastBridgeEnabled(context.Background()), "the group override must remain configured during the router read failure")
			account := fastWSBridgeTestAccount(true, false)
			ctx := fastWSBridgeTestContext(OpenAIFastTierPriority)
			var result *OpenAIForwardResult
			var err error
			if messages {
				body := anthropicMessagesBridgeTestBody(false)
				c, _ := newAnthropicMessagesBridgeTestGinContext(t, body)
				result, err = svc.ForwardAsAnthropic(ctx, c, account, body, "", "gpt-6.1-sol")
			} else {
				c, _ := newFastWSBridgeTestGinContext(t)
				result, err = svc.Forward(ctx, c, account, []byte(fastWSBridgeTestBody))
			}
			require.NoError(t, err)
			require.NotNil(t, result)
			require.False(t, result.OpenAIWSMode)
			require.Zero(t, dialer.DialCount())
			require.NotNil(t, upstream.lastReq)
			require.Equal(t, "mode_router_settings_unavailable", svc.getOpenAIWSProtocolResolver().Resolve(account).Reason)
			require.False(t, svc.isOpenAIAccountTransportCompatible(account, OpenAIUpstreamTransportResponsesWebsocketV2Ingress))
		})
	}
}

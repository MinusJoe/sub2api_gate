package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/MACOS-DO/sub4api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

const tlsWiringCodexProfileName = "codex_cli_rs_0.169.0_linux_rustls"

// tlsWiringUpstreamStub 记录 doOpenAIUpstream 走的是 Do 还是 DoWithTLS，以及传入的 profile。
type tlsWiringUpstreamStub struct {
	mu             sync.Mutex
	doCalls        int
	doWithTLSCalls int
	tlsProfiles    []*tlsfingerprint.Profile
}

func (u *tlsWiringUpstreamStub) Do(*http.Request, string, int64, int) (*http.Response, error) {
	u.mu.Lock()
	u.doCalls++
	u.mu.Unlock()
	return tlsWiringOKResponse(), nil
}

func (u *tlsWiringUpstreamStub) DoWithTLS(
	_ *http.Request,
	_ string,
	_ int64,
	_ int,
	profile *tlsfingerprint.Profile,
) (*http.Response, error) {
	u.mu.Lock()
	u.doWithTLSCalls++
	u.tlsProfiles = append(u.tlsProfiles, profile)
	u.mu.Unlock()
	return tlsWiringOKResponse(), nil
}

func (u *tlsWiringUpstreamStub) counts() (int, int) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.doCalls, u.doWithTLSCalls
}

func (u *tlsWiringUpstreamStub) recordedProfiles() []*tlsfingerprint.Profile {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]*tlsfingerprint.Profile(nil), u.tlsProfiles...)
}

func tlsWiringOKResponse() *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
	}
}

// resetOpenAITLSProfileResolver 保证每个用例结束后全局 resolver 回到未注册状态。
func resetOpenAITLSProfileResolver(t *testing.T) {
	t.Helper()
	t.Cleanup(func() { SetOpenAITLSProfileResolver(nil) })
}

func tlsWiringAccount(platform, accountType string, extra map[string]any) *Account {
	return &Account{
		ID:          101,
		Name:        "tls-wiring",
		Platform:    platform,
		Type:        accountType,
		Concurrency: 2,
		Extra:       extra,
	}
}

func TestTLSProfileWiringIsTLSFingerprintEnabled(t *testing.T) {
	cases := []struct {
		name    string
		account *Account
		want    bool
	}{
		{
			name:    "openai_oauth_enabled",
			account: tlsWiringAccount(PlatformOpenAI, AccountTypeOAuth, map[string]any{"enable_tls_fingerprint": true}),
			want:    true,
		},
		{
			name:    "openai_setup_token_enabled",
			account: tlsWiringAccount(PlatformOpenAI, AccountTypeSetupToken, map[string]any{"enable_tls_fingerprint": true}),
			want:    true,
		},
		{
			name:    "openai_oauth_unset",
			account: tlsWiringAccount(PlatformOpenAI, AccountTypeOAuth, nil),
			want:    false,
		},
		{
			name:    "openai_oauth_explicit_false",
			account: tlsWiringAccount(PlatformOpenAI, AccountTypeOAuth, map[string]any{"enable_tls_fingerprint": false}),
			want:    false,
		},
		{
			name:    "openai_apikey_ignores_flag",
			account: tlsWiringAccount(PlatformOpenAI, AccountTypeAPIKey, map[string]any{"enable_tls_fingerprint": true}),
			want:    false,
		},
		{
			name:    "anthropic_oauth_enabled_regression",
			account: tlsWiringAccount(PlatformAnthropic, AccountTypeOAuth, map[string]any{"enable_tls_fingerprint": true}),
			want:    true,
		},
		{
			name:    "anthropic_setup_token_enabled_regression",
			account: tlsWiringAccount(PlatformAnthropic, AccountTypeSetupToken, map[string]any{"enable_tls_fingerprint": true}),
			want:    true,
		},
		{
			name:    "anthropic_oauth_unset_regression",
			account: tlsWiringAccount(PlatformAnthropic, AccountTypeOAuth, nil),
			want:    false,
		},
		{
			name:    "anthropic_apikey_ignores_flag_regression",
			account: tlsWiringAccount(PlatformAnthropic, AccountTypeAPIKey, map[string]any{"enable_tls_fingerprint": true}),
			want:    false,
		},
		{
			name:    "grok_oauth_ignores_flag",
			account: tlsWiringAccount(PlatformGrok, AccountTypeOAuth, map[string]any{"enable_tls_fingerprint": true}),
			want:    false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, tc.account.IsTLSFingerprintEnabled())
		})
	}
}

func TestTLSProfileWiringResolveTLSProfileDefaults(t *testing.T) {
	resetOpenAITLSProfileResolver(t)

	// repo/cache 置 nil 只用于验证构造不 panic（未装配仓储时 localCache 为空）。
	svc := NewTLSFingerprintProfileService(nil, nil)
	require.NotNil(t, svc)

	t.Run("openai_enabled_without_profile_id_uses_codex_rustls", func(t *testing.T) {
		profile := svc.ResolveTLSProfile(tlsWiringAccount(PlatformOpenAI, AccountTypeOAuth, map[string]any{
			"enable_tls_fingerprint": true,
		}))
		require.NotNil(t, profile)
		require.Equal(t, tlsWiringCodexProfileName, profile.Name)

		want := tlsfingerprint.CodexRustlsProfile()
		require.Equal(t, want.CipherSuites, profile.CipherSuites)
		require.Equal(t, want.Curves, profile.Curves)
		require.Equal(t, want.SignatureAlgorithms, profile.SignatureAlgorithms)
		require.Equal(t, want.SupportedVersions, profile.SupportedVersions)
		require.Equal(t, want.KeyShareGroups, profile.KeyShareGroups)
		require.Equal(t, want.Extensions, profile.Extensions)
		// 官方 Codex CLI 不发 ALPN：扩展列表里不能出现 16。
		require.NotContains(t, profile.Extensions, uint16(16))
	})

	t.Run("openai_setup_token_enabled_without_profile_id_uses_codex_rustls", func(t *testing.T) {
		profile := svc.ResolveTLSProfile(tlsWiringAccount(PlatformOpenAI, AccountTypeSetupToken, map[string]any{
			"enable_tls_fingerprint": true,
		}))
		require.NotNil(t, profile)
		require.Equal(t, tlsWiringCodexProfileName, profile.Name)
	})

	t.Run("openai_enabled_with_unresolvable_profile_id_falls_back_to_codex_rustls", func(t *testing.T) {
		profile := svc.ResolveTLSProfile(tlsWiringAccount(PlatformOpenAI, AccountTypeOAuth, map[string]any{
			"enable_tls_fingerprint":     true,
			"tls_fingerprint_profile_id": float64(424242),
		}))
		require.NotNil(t, profile)
		require.Equal(t, tlsWiringCodexProfileName, profile.Name)
	})

	t.Run("openai_enabled_with_random_profile_id_falls_back_to_codex_rustls", func(t *testing.T) {
		profile := svc.ResolveTLSProfile(tlsWiringAccount(PlatformOpenAI, AccountTypeOAuth, map[string]any{
			"enable_tls_fingerprint":     true,
			"tls_fingerprint_profile_id": float64(-1),
		}))
		require.NotNil(t, profile)
		require.Equal(t, tlsWiringCodexProfileName, profile.Name)
	})

	t.Run("anthropic_enabled_without_profile_id_keeps_node_default", func(t *testing.T) {
		profile := svc.ResolveTLSProfile(tlsWiringAccount(PlatformAnthropic, AccountTypeOAuth, map[string]any{
			"enable_tls_fingerprint": true,
		}))
		require.NotNil(t, profile)
		require.Equal(t, "Built-in Default (Node.js 24.x)", profile.Name)
		// 空 Profile 意味着 dialer 使用内置 Node.js 24.x 默认值。
		require.Empty(t, profile.CipherSuites)
		require.Empty(t, profile.Extensions)
	})

	t.Run("openai_not_enabled_returns_nil", func(t *testing.T) {
		require.Nil(t, svc.ResolveTLSProfile(tlsWiringAccount(PlatformOpenAI, AccountTypeOAuth, nil)))
		require.Nil(t, svc.ResolveTLSProfile(tlsWiringAccount(PlatformOpenAI, AccountTypeSetupToken, map[string]any{
			"enable_tls_fingerprint": false,
		})))
		require.Nil(t, svc.ResolveTLSProfile(tlsWiringAccount(PlatformOpenAI, AccountTypeAPIKey, map[string]any{
			"enable_tls_fingerprint": true,
		})))
	})

	t.Run("anthropic_not_enabled_returns_nil", func(t *testing.T) {
		require.Nil(t, svc.ResolveTLSProfile(tlsWiringAccount(PlatformAnthropic, AccountTypeOAuth, nil)))
	})

	t.Run("nil_account_returns_nil", func(t *testing.T) {
		require.Nil(t, svc.ResolveTLSProfile(nil))
	})
}

func TestTLSProfileWiringDoOpenAIUpstreamUsesResolver(t *testing.T) {
	newService := func(upstream HTTPUpstream) *OpenAIGatewayService {
		return &OpenAIGatewayService{httpUpstream: upstream}
	}
	newRequest := func(t *testing.T) *http.Request {
		t.Helper()
		request, err := http.NewRequestWithContext(
			context.Background(),
			http.MethodPost,
			"https://chatgpt.com/backend-api/codex/responses",
			strings.NewReader(`{"model":"gpt-5"}`),
		)
		require.NoError(t, err)
		return request
	}

	t.Run("resolver_unregistered_calls_do", func(t *testing.T) {
		SetOpenAITLSProfileResolver(nil)
		resetOpenAITLSProfileResolver(t)

		upstream := &tlsWiringUpstreamStub{}
		svc := newService(upstream)
		account := tlsWiringAccount(PlatformOpenAI, AccountTypeOAuth, map[string]any{"enable_tls_fingerprint": true})

		response, err := svc.doOpenAIUpstream(newRequest(t), "", account)
		require.NoError(t, err)
		require.NotNil(t, response)
		_ = response.Body.Close()

		doCalls, tlsCalls := upstream.counts()
		require.Equal(t, 1, doCalls)
		require.Equal(t, 0, tlsCalls)
	})

	t.Run("resolver_returns_nil_calls_do", func(t *testing.T) {
		SetOpenAITLSProfileResolver(func(*Account) *tlsfingerprint.Profile { return nil })
		resetOpenAITLSProfileResolver(t)

		upstream := &tlsWiringUpstreamStub{}
		svc := newService(upstream)
		account := tlsWiringAccount(PlatformOpenAI, AccountTypeOAuth, nil)

		response, err := svc.doOpenAIUpstream(newRequest(t), "", account)
		require.NoError(t, err)
		require.NotNil(t, response)
		_ = response.Body.Close()

		doCalls, tlsCalls := upstream.counts()
		require.Equal(t, 1, doCalls)
		require.Equal(t, 0, tlsCalls)
	})

	t.Run("resolver_returns_codex_profile_calls_do_with_tls", func(t *testing.T) {
		want := tlsfingerprint.CodexRustlsProfile()
		SetOpenAITLSProfileResolver(func(*Account) *tlsfingerprint.Profile { return want })
		resetOpenAITLSProfileResolver(t)

		upstream := &tlsWiringUpstreamStub{}
		svc := newService(upstream)
		account := tlsWiringAccount(PlatformOpenAI, AccountTypeOAuth, map[string]any{"enable_tls_fingerprint": true})

		response, err := svc.doOpenAIUpstream(newRequest(t), "", account)
		require.NoError(t, err)
		require.NotNil(t, response)
		_ = response.Body.Close()

		doCalls, tlsCalls := upstream.counts()
		require.Equal(t, 0, doCalls)
		require.Equal(t, 1, tlsCalls)
		recorded := upstream.recordedProfiles()
		require.Len(t, recorded, 1)
		require.NotNil(t, recorded[0])
		require.Equal(t, tlsWiringCodexProfileName, recorded[0].Name)
		require.Equal(t, want, recorded[0])
	})

	t.Run("plugin_manager_present_but_not_handling_still_uses_resolver", func(t *testing.T) {
		SetOpenAITLSProfileResolver(func(*Account) *tlsfingerprint.Profile {
			return tlsfingerprint.CodexRustlsProfile()
		})
		resetOpenAITLSProfileResolver(t)

		upstream := &tlsWiringUpstreamStub{}
		// 未启用任何插件绑定的 manager：RoundTripOpenAIOAuth 返回 handled=false。
		svc := &OpenAIGatewayService{httpUpstream: upstream, pluginManager: &PluginManager{}}
		account := tlsWiringAccount(PlatformOpenAI, AccountTypeOAuth, map[string]any{"enable_tls_fingerprint": true})

		response, err := svc.doOpenAIUpstream(newRequest(t), "", account)
		require.NoError(t, err)
		require.NotNil(t, response)
		_ = response.Body.Close()

		doCalls, tlsCalls := upstream.counts()
		require.Equal(t, 0, doCalls)
		require.Equal(t, 1, tlsCalls)
		require.Equal(t, tlsWiringCodexProfileName, upstream.recordedProfiles()[0].Name)
	})

	t.Run("registered_service_resolver_applies_only_to_enabled_openai_accounts", func(t *testing.T) {
		resetOpenAITLSProfileResolver(t)
		// 构造时注册 svc.ResolveTLSProfile（生产装配路径）。
		_ = NewTLSFingerprintProfileService(nil, nil)

		upstream := &tlsWiringUpstreamStub{}
		svc := newService(upstream)

		enabled := tlsWiringAccount(PlatformOpenAI, AccountTypeOAuth, map[string]any{"enable_tls_fingerprint": true})
		response, err := svc.doOpenAIUpstream(newRequest(t), "", enabled)
		require.NoError(t, err)
		require.NotNil(t, response)
		_ = response.Body.Close()

		disabled := tlsWiringAccount(PlatformOpenAI, AccountTypeOAuth, nil)
		response, err = svc.doOpenAIUpstream(newRequest(t), "", disabled)
		require.NoError(t, err)
		require.NotNil(t, response)
		_ = response.Body.Close()

		apiKey := tlsWiringAccount(PlatformOpenAI, AccountTypeAPIKey, map[string]any{"enable_tls_fingerprint": true})
		response, err = svc.doOpenAIUpstream(newRequest(t), "", apiKey)
		require.NoError(t, err)
		require.NotNil(t, response)
		_ = response.Body.Close()

		doCalls, tlsCalls := upstream.counts()
		require.Equal(t, 2, doCalls)
		require.Equal(t, 1, tlsCalls)
		recorded := upstream.recordedProfiles()
		require.Len(t, recorded, 1)
		require.Equal(t, tlsWiringCodexProfileName, recorded[0].Name)
	})
}

func TestTLSProfileWiringResolverRegistrationIsReversibleAndNilSafe(t *testing.T) {
	resetOpenAITLSProfileResolver(t)

	require.Nil(t, openAITLSProfileFor(&Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth}))
	require.Nil(t, openAITLSProfileFor(nil))

	SetOpenAITLSProfileResolver(func(*Account) *tlsfingerprint.Profile {
		return tlsfingerprint.CodexRustlsProfile()
	})
	require.NotNil(t, openAITLSProfileFor(&Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth}))

	SetOpenAITLSProfileResolver(nil)
	require.Nil(t, openAITLSProfileFor(&Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth}))
}

// 指纹真正被用上时要可观测：每个账号只累加一次，未启用账号不产生噪声。
func TestOpenAITLSFingerprintAppliedCounter(t *testing.T) {
	SetOpenAITLSProfileResolver(func(a *Account) *tlsfingerprint.Profile {
		if a != nil && a.ID == 901 {
			return tlsfingerprint.CodexRustlsProfile()
		}
		return nil
	})
	t.Cleanup(func() { SetOpenAITLSProfileResolver(nil) })

	before := OpenAITLSFingerprintAppliedAccountCount()
	enabled := &Account{ID: 901, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{"enable_tls_fingerprint": true}}

	require.NotNil(t, openAITLSProfileFor(enabled))
	require.NotNil(t, openAITLSProfileFor(enabled))
	require.Equal(t, before+1, OpenAITLSFingerprintAppliedAccountCount(), "同一账号只累加一次")

	require.Nil(t, openAITLSProfileFor(&Account{ID: 902, Platform: PlatformOpenAI}))
	require.Equal(t, before+1, OpenAITLSFingerprintAppliedAccountCount(), "未启用账号不计数")
}

// ticket 收割与业务请求打的是同一个 /codex/responses：开启指纹后必须同一套 TLS，
// 否则同一账号同一 host 会出现两种 ClientHello。
func TestTicketUpstreamUsesTLSProfile(t *testing.T) {
	upstream := &tlsWiringUpstreamStub{}
	svc := &OpenAIGatewayService{httpUpstream: upstream}
	account := &Account{ID: 77, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{"enable_tls_fingerprint": true}}
	req, _ := http.NewRequest(http.MethodPost, "https://chatgpt.com/backend-api/codex/responses", nil)

	// 未注册 resolver：维持 Do
	SetOpenAITLSProfileResolver(nil)
	if _, err := svc.doOpenAIUpstreamWithTLS(req, "", account); err != nil {
		t.Fatalf("doOpenAIUpstreamWithTLS: %v", err)
	}
	require.Equal(t, 1, upstream.doCalls)
	require.Equal(t, 0, upstream.doWithTLSCalls)

	// 注册后：走 DoWithTLS
	SetOpenAITLSProfileResolver(func(a *Account) *tlsfingerprint.Profile { return tlsfingerprint.CodexRustlsProfile() })
	t.Cleanup(func() { SetOpenAITLSProfileResolver(nil) })
	if _, err := svc.doOpenAIUpstreamWithTLS(req, "", account); err != nil {
		t.Fatalf("doOpenAIUpstreamWithTLS: %v", err)
	}
	require.Equal(t, 1, upstream.doCalls)
	require.Equal(t, 1, upstream.doWithTLSCalls)
	require.Equal(t, "codex_cli_rs_0.169.0_linux_rustls", upstream.tlsProfiles[len(upstream.tlsProfiles)-1].Name)
}

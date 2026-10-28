//go:build unit

package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MACOS-DO/sub4api/internal/config"
	"github.com/MACOS-DO/sub4api/internal/pkg/claude"
	"github.com/MACOS-DO/sub4api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type oauthModelsUpstreamRecorder struct {
	anthropicHTTPUpstreamRecorder
	proxyURL string
}

func (r *oauthModelsUpstreamRecorder) DoWithTLS(req *http.Request, proxyURL string, id int64, concurrency int, profile *tlsfingerprint.Profile) (*http.Response, error) {
	r.proxyURL = proxyURL
	return r.anthropicHTTPUpstreamRecorder.DoWithTLS(req, proxyURL, id, concurrency, profile)
}

func TestKimiOAuthModelSyncRefreshesAuthorization(t *testing.T) {
	flow, client, _, locks := newKimiTestFlow()
	repo := &kimiTestAccountRepo{account: kimiTestAccount()}
	flow.accounts = repo
	flow.refreshAPI = NewOAuthRefreshAPI(repo, locks)
	repo.account.Credentials["base_url"] = "https://untrusted.example/v1"
	proxy := &Proxy{ID: 7, Protocol: "http", Host: "proxy.example", Port: 8080, Status: StatusActive}
	repo.account.ProxyID = &proxy.ID
	flow.proxyRepo = &kimiTestProxyRepo{proxy: proxy}
	upstream := &oauthModelsUpstreamRecorder{anthropicHTTPUpstreamRecorder: anthropicHTTPUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"data":[{"id":"kimi-new-from-upstream"}]}`)),
	}}}
	svc := &AccountTestService{httpUpstream: upstream, openaiGatewayService: &OpenAIGatewayService{kimiOAuthService: flow}}
	models, err := svc.FetchUpstreamSupportedModels(context.Background(), repo.account)
	require.NoError(t, err)
	require.Equal(t, []string{"kimi-new-from-upstream"}, models)
	require.Equal(t, 1, client.refreshes)
	require.Equal(t, "https://api.kimi.com/coding/v1/models", upstream.lastReq.URL.String())
	require.Equal(t, "Bearer new-access", upstream.lastReq.Header.Get("Authorization"))
	require.NotEmpty(t, upstream.lastReq.Header.Get("X-Msh-Device-Id"))
	require.Empty(t, upstream.lastReq.Header.Get("x-api-key"))
	require.Equal(t, "new-refresh", repo.account.GetCredential("refresh_token"))
	require.Equal(t, proxy.URL(), upstream.proxyURL)
	require.Equal(t, DefaultKimiCodingBaseURL, upstreamModelRegistryBaseURL(repo.account))
}

func TestKimiOAuthModelSyncDoesNotSendStaleCredentialsAfterRefreshFailure(t *testing.T) {
	flow, client, _, locks := newKimiTestFlow()
	repo := &kimiTestAccountRepo{account: kimiTestAccount()}
	flow.accounts = repo
	flow.refreshAPI = NewOAuthRefreshAPI(repo, locks)
	client.refreshErr = errors.New("refresh failed")
	upstream := &anthropicHTTPUpstreamRecorder{}
	svc := &AccountTestService{httpUpstream: upstream, openaiGatewayService: &OpenAIGatewayService{kimiOAuthService: flow}}
	_, err := svc.FetchUpstreamSupportedModels(context.Background(), repo.account)
	require.Error(t, err)
	require.Nil(t, upstream.lastReq)
	require.Equal(t, "old-access", repo.account.GetCredential("access_token"))
}

func TestClaudeOAuthModelSyncUsesCurrentTokenProvider(t *testing.T) {
	account := &Account{ID: 12, Platform: PlatformAnthropic, Type: AccountTypeOAuth, Credentials: map[string]any{"access_token": "stale-token"}}
	cache := newClaudeTokenCacheStub()
	cache.tokens[ClaudeTokenCacheKey(account)] = "refreshed-token"
	svc := &AccountTestService{cfg: upstreamModelSyncTestConfig(), claudeTokenProvider: NewClaudeTokenProvider(nil, cache, nil)}
	req, err := svc.buildUpstreamModelsRequest(context.Background(), account)
	require.NoError(t, err)
	require.Equal(t, "https://api.anthropic.com/v1/models", req.URL.String())
	require.Equal(t, "Bearer refreshed-token", req.Header.Get("Authorization"))
	require.Contains(t, req.Header.Get("anthropic-beta"), claude.BetaOAuth)
}

func TestClaudeOAuthExplicitMappingAcrossEndpoints(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, accountType := range []string{AccountTypeOAuth, AccountTypeSetupToken} {
		for _, endpoint := range []string{"messages", "count_tokens", "responses", "chat_completions"} {
			for _, target := range []string{"claude-sonnet-4-20250514", "claude-sonnet-4-5"} {
				t.Run(accountType+"/"+endpoint+"/"+target, func(t *testing.T) {
					const alias = "claude-sonnet-4-5"
					account := &Account{ID: 21, Platform: PlatformAnthropic, Type: accountType,
						Status: StatusActive, Schedulable: true, Concurrency: 1,
						Credentials: map[string]any{"access_token": "token", "model_mapping": map[string]any{alias: target}},
					}
					upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
						StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}},
						Body: io.NopCloser(strings.NewReader(`{"id":"msg-test","type":"message","role":"assistant","model":"` + target + `","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)),
					}}
					cfg := &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}
					svc := &GatewayService{cfg: cfg, responseHeaderFilter: compileResponseHeaderFilter(cfg), httpUpstream: upstream, rateLimitService: &RateLimitService{}, deferredService: &DeferredService{}}
					require.True(t, svc.isModelSupportedByAccount(account, alias))
					require.False(t, svc.isModelSupportedByAccount(account, "unmapped-alias"))
					rec := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(rec)
					c.Request = httptest.NewRequest(http.MethodPost, "/v1/"+endpoint, nil)
					body := []byte(`{"model":"` + alias + `","messages":[{"role":"user","content":"hello"}],"max_tokens":32}`)
					parsed := &ParsedRequest{Body: NewRequestBodyRef(body), Model: alias}
					var err error
					switch endpoint {
					case "messages":
						_, err = svc.Forward(context.Background(), c, account, parsed)
					case "count_tokens":
						upstream.resp.Body = io.NopCloser(strings.NewReader(`{"input_tokens":1}`))
						err = svc.ForwardCountTokens(context.Background(), c, account, parsed)
					case "responses":
						upstream.resp.Body = io.NopCloser(strings.NewReader(namespaceToolAnthropicStream()))
						_, err = svc.ForwardAsResponses(context.Background(), c, account, []byte(`{"model":"`+alias+`","input":"hello"}`), nil)
					case "chat_completions":
						upstream.resp.Body = io.NopCloser(strings.NewReader(namespaceToolAnthropicStream()))
						_, err = svc.ForwardAsChatCompletions(context.Background(), c, account, body, nil)
					}
					require.NoError(t, err)
					require.Equal(t, target, gjson.GetBytes(upstream.lastBody, "model").String())
				})
			}
		}
	}
}

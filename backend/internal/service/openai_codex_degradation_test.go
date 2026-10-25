package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/MACOS-DO/sub4api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func degradationTestConfig(observeOnly bool) config.CodexDegradationConfig {
	return config.CodexDegradationConfig{
		Enabled:                true,
		ObserveOnly:            observeOnly,
		WindowSeconds:          60,
		MinSamples:             1,
		DegradedRatioThreshold: 0.5,
		CooldownSeconds:        60,
	}
}

func degradationTestService(cfg config.CodexDegradationConfig) *OpenAIGatewayService {
	return &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{OpenAICodexDegradation: cfg}}}
}

func TestCodexDegradationDisabledIsNoop(t *testing.T) {
	svc := degradationTestService(config.CodexDegradationConfig{Enabled: false})
	require.Nil(t, svc.observeCodexDegradationHeaders(nil, &Account{ID: 1}, "gpt-6-astra", "http", http.Header{}))
	require.Empty(t, svc.CodexDegradationMetrics().Entries)
}

func TestCodexDegradationObserverParsesAuthorityHeaders(t *testing.T) {
	observer := &codexDegradationObserver{sentModel: "gpt-6.1-sol"}
	headers := http.Header{}
	headers.Set(headerOpenAIServedModel, "gpt-6-luna")
	headers.Set(headerCodexSafetyBuffering, "true")
	headers.Set(headerCodexSafetyFasterModel, "gpt-6-luna")
	observer.observeHeaders(headers)
	sample := observer.snapshot()
	require.True(t, sample.Degraded)
	require.True(t, sample.ModelMismatch)
	require.True(t, sample.SafetyBuffering)

	// 同模型 + 未开启 safety-buffering 不判降智。
	clean := &codexDegradationObserver{sentModel: "gpt-6.1-sol"}
	h := http.Header{}
	h.Set(headerOpenAIServedModel, "GPT-6.1-SOL")
	h.Set(headerCodexSafetyBuffering, "false")
	clean.observeHeaders(h)
	require.False(t, clean.snapshot().Degraded)
}

func TestCodexDegradationObserverParsesSafetyBufferingEvent(t *testing.T) {
	observer := &codexDegradationObserver{sentModel: "gpt-6-astra"}
	payload := []byte(`{"type":"safety_buffering","use_cases":["cyber"],"reasons":["high_risk"],"show_buffering_ui":true,"retry_model":"gpt-6-luna","headers":{"OpenAI-Model":"gpt-6-luna"}}`)
	observer.observeEvent("response.metadata", payload)
	sample := observer.snapshot()
	require.True(t, sample.Degraded)
	require.True(t, sample.SafetyBuffering)
	require.True(t, sample.ModelMismatch)
	require.Equal(t, "gpt-6-luna", observer.servedModel)
	require.Equal(t, "gpt-6-luna", observer.retryModel)
	require.Equal(t, []string{"high_risk"}, observer.safetyReasons)
	require.Equal(t, []string{"cyber"}, observer.safetyUseCases)
	require.True(t, observer.showBufferingUI)
}

func TestCodexDegradationObserverIgnoresUnrelatedEvents(t *testing.T) {
	observer := &codexDegradationObserver{sentModel: "gpt-6-astra"}
	observer.observeEvent("response.output_text.delta", []byte(`{"delta":"hello","model":"gpt-6-luna"}`))
	require.False(t, observer.snapshot().Degraded)
	require.Empty(t, observer.servedModel)
}

// response.metadata 事件 headers JSON 只携带小写 x-openai-model，经 http.Header.Set
// 规范化后落在 X-Openai-Model；只读 OpenAI-Model 会漏检该变体（0.164 sse/responses.rs
// 的 served model 走 openai-model/x-openai-model 两头）。
func TestCodexDegradationObserverParsesLowercaseServedModelVariant(t *testing.T) {
	observer := &codexDegradationObserver{sentModel: "gpt-6-astra"}
	payload := []byte(`{"headers":{"x-openai-model":"gpt-6-luna"}}`)
	observer.observeEvent("response.metadata", payload)
	sample := observer.snapshot()
	require.True(t, sample.Degraded)
	require.True(t, sample.ModelMismatch)
	require.False(t, sample.SafetyBuffering)
	require.Equal(t, "gpt-6-luna", observer.servedModel)
}

// 两处同时存在时 OpenAI-Model 优先。
func TestCodexDegradationServedModelHeaderPriority(t *testing.T) {
	observer := &codexDegradationObserver{sentModel: "gpt-6-astra"}
	headers := http.Header{}
	headers.Set(headerOpenAIServedModel, "gpt-6-luna")
	headers.Set(headerOpenAIServedModelFallback, "gpt-6-sol")
	observer.observeHeaders(headers)
	require.Equal(t, "gpt-6-luna", observer.servedModel)
}

// 配额上下文头族只读捕获并透出到管理端视图（原样字符串，不参与判定）。
func TestCodexDegradationObserverCapturesQuotaHeaders(t *testing.T) {
	observer := &codexDegradationObserver{accountID: 33, sentModel: "gpt-6-astra"}
	headers := http.Header{}
	headers.Set(headerCodexRateLimitReachedType, "usage_cap")
	headers.Set(headerCodexActiveLimit, "12000")
	headers.Set(headerCodexCreditsBalance, "12.5")
	observer.observeHeaders(headers)
	require.Equal(t, "usage_cap", observer.rateLimitReachedType)
	require.Equal(t, "12000", observer.activeLimit)
	require.Equal(t, "12.5", observer.creditsBalance)
	require.False(t, observer.snapshot().Degraded, "配额头族不参与降智判定")

	svc := degradationTestService(degradationTestConfig(true))
	svc.recordCodexDegradation(observer)
	entries := svc.CodexDegradationMetrics().Entries
	require.Len(t, entries, 1)
	require.Equal(t, "usage_cap", entries[0].LastRateLimitReachedType)
	require.Equal(t, "12000", entries[0].LastActiveLimit)
	require.Equal(t, "12.5", entries[0].LastCreditsBalance)
}

// 下游透明头默认关闭：不向下游写任何降智头。
func TestCodexDegradationDownstreamHeadersDisabledByDefault(t *testing.T) {
	svc := degradationTestService(degradationTestConfig(true))
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	headers := http.Header{}
	headers.Set(headerOpenAIServedModel, "gpt-6-luna")
	svc.observeCodexDegradationHeaders(c, &Account{ID: 41}, "gpt-6-astra", "http", headers)
	svc.applyCodexDegradationDownstreamHeaders(c)
	require.Empty(t, rec.Header().Get(headerDownstreamServedModel))
	require.Empty(t, rec.Header().Get(headerDownstreamDegraded))
}

// 显式开启后写入 served-model 与 degraded 头（无判定则不写）。
func TestCodexDegradationDownstreamHeadersWrittenWhenEnabled(t *testing.T) {
	cfg := degradationTestConfig(true)
	cfg.DownstreamHeaderEnabled = true
	svc := degradationTestService(cfg)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	headers := http.Header{}
	headers.Set(headerOpenAIServedModel, "gpt-6-luna")
	headers.Set(headerCodexSafetyBuffering, "true")
	svc.observeCodexDegradationHeaders(c, &Account{ID: 42}, "gpt-6-astra", "http", headers)
	svc.applyCodexDegradationDownstreamHeaders(c)
	require.Equal(t, "gpt-6-luna", rec.Header().Get(headerDownstreamServedModel))
	require.Equal(t, "model_mismatch|safety_buffering", rec.Header().Get(headerDownstreamDegraded))

	// 无判定（未触达上游信号）时不写。
	rec2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(rec2)
	svc2 := degradationTestService(cfg)
	svc2.observeCodexDegradationHeaders(c2, &Account{ID: 43}, "gpt-6-astra", "http", http.Header{})
	svc2.applyCodexDegradationDownstreamHeaders(c2)
	require.Empty(t, rec2.Header().Get(headerDownstreamServedModel))
	require.Empty(t, rec2.Header().Get(headerDownstreamDegraded))
}

func TestCodexDegradationRegistryAggregatesAndResets(t *testing.T) {
	svc := degradationTestService(degradationTestConfig(true))
	svc.recordCodexDegradation(&codexDegradationObserver{accountID: 7, sentModel: "gpt-6-astra", transport: "http", servedModel: "gpt-6-luna", modelMismatch: true, safetyBuffering: true, headersObserved: true})
	svc.recordCodexDegradation(&codexDegradationObserver{accountID: 7, sentModel: "gpt-6-astra", transport: "http", servedModel: "gpt-6-astra", headersObserved: true})
	metrics := svc.CodexDegradationMetrics()
	require.Len(t, metrics.Entries, 1)
	entry := metrics.Entries[0]
	require.Equal(t, int64(7), entry.AccountID)
	require.Equal(t, int64(2), entry.Total)
	require.Equal(t, int64(1), entry.Degraded)
	require.Equal(t, int64(1), entry.SafetyBuffering)
	require.InDelta(t, 0.5, entry.WindowDegradedRatio, 1e-9)
	svc.ResetCodexDegradationMetrics()
	require.Empty(t, svc.CodexDegradationMetrics().Entries)
}

type degradationRepoStub struct {
	AccountRepository
	mu    sync.Mutex
	calls []degradationCooldownCall
}

type degradationCooldownCall struct {
	accountID int64
	scope     string
	resetAt   time.Time
}

func (r *degradationRepoStub) SetModelRateLimit(_ context.Context, id int64, scope string, resetAt time.Time, _ ...string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, degradationCooldownCall{accountID: id, scope: scope, resetAt: resetAt})
	return nil
}

func (r *degradationRepoStub) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

func TestCodexDegradationObserveOnlyNeverCoolsDown(t *testing.T) {
	repo := &degradationRepoStub{}
	svc := degradationTestService(degradationTestConfig(true))
	svc.accountRepo = repo
	svc.recordCodexDegradation(&codexDegradationObserver{accountID: 8, sentModel: "gpt-6-astra", servedModel: "gpt-6-luna", modelMismatch: true, headersObserved: true})
	time.Sleep(50 * time.Millisecond)
	require.Zero(t, repo.callCount())
}

func TestCodexDegradationCooldownAppliedOncePerWindow(t *testing.T) {
	repo := &degradationRepoStub{}
	svc := degradationTestService(degradationTestConfig(false))
	svc.accountRepo = repo
	svc.recordCodexDegradation(&codexDegradationObserver{accountID: 9, sentModel: "gpt-6-astra", servedModel: "gpt-6-luna", modelMismatch: true, headersObserved: true})
	require.Eventually(t, func() bool { return repo.callCount() == 1 }, time.Second, 10*time.Millisecond)
	// 冷却窗口内再次命中不得重复调用。
	svc.recordCodexDegradation(&codexDegradationObserver{accountID: 9, sentModel: "gpt-6-astra", servedModel: "gpt-6-luna", modelMismatch: true, headersObserved: true})
	time.Sleep(50 * time.Millisecond)
	require.Equal(t, 1, repo.callCount())
	require.Equal(t, int64(9), repo.calls[0].accountID)
	require.Contains(t, repo.calls[0].scope, "gpt-6-astra")
}

func TestCodexDegradationAllowCooldownDebounces(t *testing.T) {
	svc := degradationTestService(degradationTestConfig(false))
	registry := svc.codexDegradationRegistry()
	cfg := svc.codexDegradationConfig()
	now := time.Now()
	seconds, consecutive, ok := svc.allowCodexDegradationCooldown(registry, "k", now, cfg)
	require.True(t, ok)
	require.Equal(t, cfg.CooldownSeconds, seconds)
	require.Equal(t, 1, consecutive)
	_, _, ok = svc.allowCodexDegradationCooldown(registry, "k", now.Add(30*time.Second), cfg)
	require.False(t, ok)
	seconds, consecutive, ok = svc.allowCodexDegradationCooldown(registry, "k", now.Add(61*time.Second), cfg)
	require.True(t, ok)
	require.Equal(t, 2*cfg.CooldownSeconds, seconds, "第二次连续触发按 multiplier=2 退避")
	require.Equal(t, 2, consecutive)
}

// 重复触发退避：第 n 次连续触发时长 = cooldown × multiplier^(n-1)，封顶 max。
func TestCodexDegradationCooldownBackoffEscalatesAndCaps(t *testing.T) {
	cfg := degradationTestConfig(false).Normalized() // 复现生产路径：先填充 multiplier/max 默认值
	cfg.CooldownSeconds = 60
	cfg.MaxCooldownSeconds = 200 // 压低上限以覆盖封顶分支
	svc := degradationTestService(cfg)
	registry := svc.codexDegradationRegistry()
	now := time.Now()
	expected := []int{60, 120, 200, 200, 200}
	for i, want := range expected {
		seconds, consecutive, ok := svc.allowCodexDegradationCooldown(registry, "k", now, cfg)
		require.True(t, ok)
		require.Equal(t, i+1, consecutive)
		require.Equal(t, want, seconds, "第 %d 次连续触发", i+1)
		now = now.Add(time.Duration(seconds+1) * time.Second)
	}
}

// 窗口内出现无降智样本即清零连续触发计数。
func TestCodexDegradationBackoffResetsOnCleanSample(t *testing.T) {
	repo := &degradationRepoStub{}
	svc := degradationTestService(degradationTestConfig(false))
	svc.accountRepo = repo
	registry := svc.codexDegradationRegistry()
	key := openAICodexTicketKey(31, "gpt-6-astra")
	now := time.Now()
	_, _, ok := svc.allowCodexDegradationCooldown(registry, key, now, svc.codexDegradationConfig())
	require.True(t, ok)
	require.Equal(t, 1, registry.consecutiveTriggers[key])
	// 无降智样本入库 → 计数清零，下次冷却回到基准时长。
	svc.recordCodexDegradation(&codexDegradationObserver{accountID: 31, sentModel: "gpt-6-astra", servedModel: "gpt-6-astra", headersObserved: true})
	require.Zero(t, registry.consecutiveTriggers[key])
	seconds, consecutive, ok := svc.allowCodexDegradationCooldown(registry, key, now.Add(61*time.Second), svc.codexDegradationConfig())
	require.True(t, ok)
	require.Equal(t, 1, consecutive)
	require.Equal(t, svc.codexDegradationConfig().CooldownSeconds, seconds)
}

// multiplier=1 显式关闭退避累积（每次都按基准时长冷却）。
func TestCodexDegradationBackoffDisabledWithUnitMultiplier(t *testing.T) {
	cfg := degradationTestConfig(false).Normalized()
	cfg.CooldownBackoffMultiplier = 1
	svc := degradationTestService(cfg)
	registry := svc.codexDegradationRegistry()
	now := time.Now()
	_, _, ok := svc.allowCodexDegradationCooldown(registry, "k", now, cfg)
	require.True(t, ok)
	seconds, _, ok := svc.allowCodexDegradationCooldown(registry, "k", now.Add(61*time.Second), cfg)
	require.True(t, ok)
	require.Equal(t, cfg.CooldownSeconds, seconds)
}

// WS attempt 在函数顶部 begin+defer finalize：build_ws_url / 拨号失败等未触达
// 上游的路径同样会结算。零信号 observer（headersObserved/eventObserved 均否）
// 不得记为 Degraded=false 假阴性样本。
func TestCodexDegradationZeroSignalTurnNotRecorded(t *testing.T) {
	svc := degradationTestService(degradationTestConfig(true))
	observer := svc.beginCodexDegradationObservation(nil, &Account{ID: 21}, "gpt-6-astra", "ws")
	require.NotNil(t, observer)
	svc.finalizeCodexDegradationTurn(observer)
	require.Empty(t, svc.CodexDegradationMetrics().Entries, "未触达上游的失败 attempt 不得计入样本")
}

// 对照组：握手响应头已到达（触达上游）的失败 attempt 是有效样本。
func TestCodexDegradationHandshakeObservedTurnRecorded(t *testing.T) {
	svc := degradationTestService(degradationTestConfig(true))
	observer := svc.beginCodexDegradationObservation(nil, &Account{ID: 22}, "gpt-6-astra", "ws")
	require.NotNil(t, observer)
	headers := http.Header{}
	headers.Set(headerOpenAIServedModel, "gpt-6-luna")
	observer.observeHeaders(headers)
	svc.finalizeCodexDegradationTurn(observer)
	entries := svc.CodexDegradationMetrics().Entries
	require.Len(t, entries, 1)
	require.Equal(t, int64(1), entries[0].Total)
	require.Equal(t, int64(1), entries[0].Degraded)
}

// WS 失败回退 HTTP 复用同一 gin 上下文：已 finalize 的 WS observer 必须被替换，
// 否则 finalize 见 finalized=true 直接返回，回退响应里的真实降智信号永不入库。
func TestCodexDegradationWSFallbackRecordsHTTPSignals(t *testing.T) {
	svc := degradationTestService(degradationTestConfig(true))
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	account := &Account{ID: 23}

	// WS attempt：begin → 零信号失败 → defer finalize（不得入库）。
	wsObserver := svc.beginCodexDegradationObservation(c, account, "gpt-6-astra", "ws")
	require.NotNil(t, wsObserver)
	svc.finalizeCodexDegradationTurn(wsObserver)

	// 回退 HTTP：同账号同模型，响应头携带真实降智信号。
	headers := http.Header{}
	headers.Set(headerOpenAIServedModel, "gpt-6-luna")
	httpObserver := svc.observeCodexDegradationHeaders(c, account, "gpt-6-astra", "http", headers)
	require.NotNil(t, httpObserver)
	require.NotSame(t, wsObserver, httpObserver, "已结算的 WS observer 不得被回退路径复用")
	svc.finalizeCodexDegradation(c)

	entries := svc.CodexDegradationMetrics().Entries
	require.Len(t, entries, 1)
	require.Equal(t, int64(1), entries[0].Total, "只有回退 HTTP 的真实观测计一次样本")
	require.Equal(t, int64(1), entries[0].Degraded)
	require.Equal(t, "http", entries[0].LastTransport)
}

// 未结算的同账号同模型 observer 仍按原语义复用（HTTP failover 同号重试路径）。
func TestCodexDegradationUnfinalizedObserverReusedForSameAccountModel(t *testing.T) {
	svc := degradationTestService(degradationTestConfig(true))
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	account := &Account{ID: 24}

	first := svc.observeCodexDegradationHeaders(c, account, "gpt-6-astra", "http", http.Header{})
	require.NotNil(t, first)
	second := svc.observeCodexDegradationHeaders(c, account, "gpt-6-astra", "http", http.Header{})
	require.Same(t, first, second)
	svc.finalizeCodexDegradation(c)
	require.Len(t, svc.CodexDegradationMetrics().Entries, 1)
}

package service

import (
	"context"
	"math"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/MACOS-DO/sub4api/internal/config"
	"github.com/MACOS-DO/sub4api/internal/pkg/logger"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

// Codex 降智（degradation）权威信号采集。
//
// 官方 Codex 客户端把"服务端实际使用的模型"与"安全缓冲降载"作为可观测事实：
//   - 响应头 OpenAI-Model：实际服务模型（可与请求模型不同）
//   - 响应头 x-codex-safety-buffering-enabled / -faster-model
//   - SSE/WS 事件 response.metadata type=safety_buffering（含 use_cases/reasons/retry_model）
//   - 流内 response.created/response.model
//
// 本模块只做观测与聚合；默认 observe-only，不改变调度。显式关闭 observe_only 后，
// 命中阈值的 (账号, 模型) 会复用既有 SetModelRateLimit 通道进入冷却，限制账号抖动。
const (
	codexDegradationContextKey = "codex_degradation_observer"

	headerOpenAIServedModel = "OpenAI-Model"
	// headerOpenAIServedModelFallback 兜底读 0.164 sse/responses.rs 的另一变体头
	// x-openai-model：response.metadata 事件的 headers JSON 键为小写，经
	// http.Header.Set 规范化后落在 X-Openai-Model，Get("OpenAI-Model") 取不到。
	headerOpenAIServedModelFallback = "X-Openai-Model"
	headerCodexSafetyBuffering      = "x-codex-safety-buffering-enabled"
	headerCodexSafetyFasterModel    = "x-codex-safety-buffering-faster-model"

	// 配额上下文头族（rea-11：codex-api/src/rate_limits.rs / api_bridge.rs 解析面）。
	// 只读捕获原样字符串，不参与降智判定；上游未下发时保持空值。
	headerCodexRateLimitReachedType = "x-codex-rate-limit-reached-type"
	headerCodexActiveLimit          = "x-codex-active-limit"
	headerCodexCreditsBalance       = "x-codex-credits-balance"

	// 下游透明头（downstream_header_enabled，默认关）：把 served-model 判定
	// 转译给 API 消费者；仅影响网关→下游响应头，不改变任何出站行为。
	headerDownstreamServedModel = "x-sub4api-codex-served-model"
	headerDownstreamDegraded    = "x-sub4api-codex-degraded"

	codexDegradedReasonModelMismatch   = "model_mismatch"
	codexDegradedReasonSafetyBuffering = "safety_buffering"

	codexDegradationMaxSamples = 256
	codexDegradationEventType  = "safety_buffering"
)

// codexDegradationSample 是一次业务请求/回合的降智判定结果。
type codexDegradationSample struct {
	At              time.Time `json:"at"`
	Degraded        bool      `json:"degraded"`
	ModelMismatch   bool      `json:"model_mismatch"`
	SafetyBuffering bool      `json:"safety_buffering"`
}

// codexDegradationObserver 跟踪一次请求（HTTP）或一个 WS 回合的降智信号。
type codexDegradationObserver struct {
	mu sync.Mutex

	accountID int64
	sentModel string
	transport string

	servedModel     string
	modelMismatch   bool
	safetyBuffering bool
	fasterModel     string
	safetyReasons   []string
	safetyUseCases  []string
	retryModel      string
	showBufferingUI bool

	// 配额上下文头族快照（观测-only，原样字符串）。
	rateLimitReachedType string
	activeLimit          string
	creditsBalance       string

	headersObserved bool
	eventObserved   bool
	finalized       bool
}

func normalizeCodexDegradationModel(model string) string {
	return strings.TrimSpace(model)
}

func (o *codexDegradationObserver) observeHeaders(headers http.Header) {
	if o == nil || headers == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.observeHeadersLocked(headers)
}

func (o *codexDegradationObserver) observeHeadersLocked(headers http.Header) {
	if headers == nil {
		return
	}
	o.headersObserved = true
	served := normalizeCodexDegradationModel(headers.Get(headerOpenAIServedModel))
	if served == "" {
		// 事件 headers JSON 只携带小写 x-openai-model，经 http.Header.Set 规范化后
		// Get("OpenAI-Model") 取不到；两处都存在时 OpenAI-Model 优先。
		served = normalizeCodexDegradationModel(headers.Get(headerOpenAIServedModelFallback))
	}
	if served != "" {
		o.setServedModelLocked(served)
	}
	if enabled := strings.TrimSpace(headers.Get(headerCodexSafetyBuffering)); enabled != "" {
		if strings.EqualFold(enabled, "true") {
			o.safetyBuffering = true
		}
	}
	if faster := normalizeCodexDegradationModel(headers.Get(headerCodexSafetyFasterModel)); faster != "" {
		o.fasterModel = faster
	}
	if v := strings.TrimSpace(headers.Get(headerCodexRateLimitReachedType)); v != "" {
		o.rateLimitReachedType = v
	}
	if v := strings.TrimSpace(headers.Get(headerCodexActiveLimit)); v != "" {
		o.activeLimit = v
	}
	if v := strings.TrimSpace(headers.Get(headerCodexCreditsBalance)); v != "" {
		o.creditsBalance = v
	}
}

func (o *codexDegradationObserver) setServedModelLocked(model string) {
	model = normalizeCodexDegradationModel(model)
	if model == "" {
		return
	}
	o.servedModel = model
	if o.sentModel != "" && !strings.EqualFold(o.sentModel, model) {
		o.modelMismatch = true
	}
}

// observeEvent 解析 SSE/WS 事件。仅对可能携带降智信号的少量事件做 gjson，
// 其余事件立刻返回，避免给流式热路径增加解析成本。
func (o *codexDegradationObserver) observeEvent(eventType string, payload []byte) {
	if o == nil || len(payload) == 0 {
		return
	}
	eventType = strings.TrimSpace(eventType)
	switch eventType {
	case "response.metadata", "response.created", "response.completed", "response.done", "response.incomplete":
	default:
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()

	if eventType == "response.metadata" {
		o.eventObserved = true
		if headers := gjson.GetBytes(payload, "headers"); headers.IsObject() {
			header := make(http.Header, 4)
			headers.ForEach(func(key, value gjson.Result) bool {
				header.Set(key.String(), value.String())
				return true
			})
			o.observeHeadersLocked(header)
		}
		if typ := strings.TrimSpace(gjson.GetBytes(payload, "type").String()); strings.EqualFold(typ, codexDegradationEventType) {
			o.safetyBuffering = true
			if retry := normalizeCodexDegradationModel(gjson.GetBytes(payload, "retry_model").String()); retry != "" {
				o.retryModel = retry
			}
			o.showBufferingUI = o.showBufferingUI || gjson.GetBytes(payload, "show_buffering_ui").Bool()
			if reasons := gjson.GetBytes(payload, "reasons"); reasons.IsArray() {
				reasons.ForEach(func(_, value gjson.Result) bool {
					if text := strings.TrimSpace(value.String()); text != "" && len(o.safetyReasons) < 8 {
						o.safetyReasons = append(o.safetyReasons, text)
					}
					return true
				})
			}
			if useCases := gjson.GetBytes(payload, "use_cases"); useCases.IsArray() {
				useCases.ForEach(func(_, value gjson.Result) bool {
					if text := strings.TrimSpace(value.String()); text != "" && len(o.safetyUseCases) < 8 {
						o.safetyUseCases = append(o.safetyUseCases, text)
					}
					return true
				})
			}
		}
	}
	if model := normalizeCodexDegradationModel(firstValidTrimmedGJSONString(payload, "response.model", "model")); model != "" {
		o.setServedModelLocked(model)
	}
}

func (o *codexDegradationObserver) snapshot() codexDegradationSample {
	o.mu.Lock()
	defer o.mu.Unlock()
	degraded := o.modelMismatch || o.safetyBuffering
	return codexDegradationSample{
		At:              time.Now().UTC(),
		Degraded:        degraded,
		ModelMismatch:   o.modelMismatch,
		SafetyBuffering: o.safetyBuffering,
	}
}

// downstreamSnapshot 返回当前累积的实际服务模型与降智原因（"|" 连接，均可能为空），
// 供下游透明头使用。只读已落定信号，不触发任何上游行为。
func (o *codexDegradationObserver) downstreamSnapshot() (servedModel, degraded string) {
	if o == nil {
		return "", ""
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	var reasons []string
	if o.modelMismatch {
		reasons = append(reasons, codexDegradedReasonModelMismatch)
	}
	if o.safetyBuffering {
		reasons = append(reasons, codexDegradedReasonSafetyBuffering)
	}
	return o.servedModel, strings.Join(reasons, "|")
}

// isFinalized 报告该 observer 是否已结算。已结算的 observer 不可再复用：
// finalize 见 finalized=true 直接返回，复用它会让后续路径观察到的真实信号
// 永不入库（典型场景：WS attempt 失败后回退 HTTP 复用同一 gin 上下文）。
func (o *codexDegradationObserver) isFinalized() bool {
	if o == nil {
		return false
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.finalized
}

// beginCodexDegradationObservation 创建并挂到 gin 上下文；HTTP 路径在出站响应头到达
// 后调用，WS 路径可直接持有返回值（不走 gin 上下文）。
func (s *OpenAIGatewayService) beginCodexDegradationObservation(c *gin.Context, account *Account, sentModel, transport string) *codexDegradationObserver {
	if s == nil || !s.codexDegradationEnabled() {
		return nil
	}
	observer := &codexDegradationObserver{
		sentModel: normalizeCodexDegradationModel(sentModel),
		transport: strings.TrimSpace(transport),
	}
	if account != nil {
		observer.accountID = account.ID
	}
	if c != nil {
		c.Set(codexDegradationContextKey, observer)
	}
	return observer
}

func codexDegradationObserverFromContext(c *gin.Context) *codexDegradationObserver {
	if c == nil {
		return nil
	}
	value, ok := c.Get(codexDegradationContextKey)
	if !ok {
		return nil
	}
	observer, _ := value.(*codexDegradationObserver)
	return observer
}

// observeCodexDegradationHeaders 供 HTTP/WS 响应头路径调用。
// failover 换号/换模型时先结算上一 attempt，避免把新账号的服务模型记到旧账号上。
// 已结算的 observer（如 WS attempt 失败后 defer finalize，再回退 HTTP 复用同一
// gin 上下文）不可复用：必须重开新 observer，回退响应里的真实降智信号才能入库。
func (s *OpenAIGatewayService) observeCodexDegradationHeaders(c *gin.Context, account *Account, sentModel, transport string, headers http.Header) *codexDegradationObserver {
	observer := codexDegradationObserverFromContext(c)
	if observer != nil {
		sameAccount := account == nil || observer.accountID == account.ID
		sameModel := observer.sentModel == normalizeCodexDegradationModel(sentModel)
		if observer.isFinalized() || !sameAccount || !sameModel {
			s.finalizeCodexDegradation(c)
			observer = nil
		}
	}
	if observer == nil {
		observer = s.beginCodexDegradationObservation(c, account, sentModel, transport)
	}
	if observer == nil {
		return nil
	}
	observer.observeHeaders(headers)
	return observer
}

// observeCodexDegradationEvent 供 SSE/WS 事件解析路径调用。
func observeCodexDegradationEvent(c *gin.Context, eventType string, payload []byte) {
	if observer := codexDegradationObserverFromContext(c); observer != nil {
		observer.observeEvent(eventType, payload)
	}
}

// applyCodexDegradationDownstreamHeaders 把 observer 当前快照以响应头透出给下游
// （x-sub4api-codex-served-model / x-sub4api-codex-degraded）。受
// gateway.openai_codex_degradation.downstream_header_enabled 控制，默认关闭；
// 无任何判定时不写。必须在下游响应体开始前调用（流式在首输出提交点）。
func (s *OpenAIGatewayService) applyCodexDegradationDownstreamHeaders(c *gin.Context) {
	if c == nil || !s.codexDegradationConfig().DownstreamHeaderEnabled || c.Writer.Written() {
		return
	}
	observer := codexDegradationObserverFromContext(c)
	if observer == nil {
		return
	}
	servedModel, degraded := observer.downstreamSnapshot()
	if servedModel == "" && degraded == "" {
		return
	}
	if servedModel != "" {
		c.Header(headerDownstreamServedModel, servedModel)
	}
	if degraded != "" {
		c.Header(headerDownstreamDegraded, degraded)
	}
}

// finalizeCodexDegradation 汇总一次请求/回合并写入进程内聚合；每个 observer 只生效一次。
func (s *OpenAIGatewayService) finalizeCodexDegradation(c *gin.Context) {
	observer := codexDegradationObserverFromContext(c)
	if observer == nil {
		return
	}
	observer.mu.Lock()
	if observer.finalized {
		observer.mu.Unlock()
		return
	}
	observer.finalized = true
	// 没有拿到 served model 时，用既有 observer 的终态模型兜底（HTTP 非流式/流式均适用）。
	if observer.servedModel == "" {
		if model := normalizeCodexDegradationModel(observedUpstreamResponseModel(c)); model != "" {
			observer.setServedModelLocked(model)
		}
	}
	observer.mu.Unlock()
	s.recordCodexDegradation(observer)
}

// finalizeCodexDegradationTurn 是 WS 路径的收尾（不依赖 gin 上下文存活）。
func (s *OpenAIGatewayService) finalizeCodexDegradationTurn(observer *codexDegradationObserver) {
	if observer == nil {
		return
	}
	observer.mu.Lock()
	if observer.finalized {
		observer.mu.Unlock()
		return
	}
	observer.finalized = true
	observer.mu.Unlock()
	s.recordCodexDegradation(observer)
}

// ---------------------------------------------------------------------------
// 聚合与策略
// ---------------------------------------------------------------------------

type codexDegradationEntry struct {
	accountID int64
	model     string

	total           int64
	degraded        int64
	modelMismatch   int64
	safetyBuffering int64

	samples    []codexDegradationSample
	lastSignal time.Time

	lastServedModel string
	lastFasterModel string
	lastTransport   string
	lastReasons     []string

	// 配额上下文头族最近一次观测值（观测-only，原样字符串）。
	lastRateLimitReachedType string
	lastActiveLimit          string
	lastCreditsBalance       string

	lastDegradedAt time.Time
}

func (e *codexDegradationEntry) windowRatios(now time.Time, window time.Duration) (total, degraded int, ok bool) {
	if e == nil {
		return 0, 0, false
	}
	cutoff := now.Add(-window)
	for _, sample := range e.samples {
		if sample.At.Before(cutoff) {
			continue
		}
		total++
		if sample.Degraded {
			degraded++
		}
	}
	return total, degraded, total > 0
}

type codexDegradationRegistry struct {
	mu           sync.Mutex
	entries      map[string]*codexDegradationEntry
	cooldownNext map[string]time.Time
	// consecutiveTriggers 记录 (账号, 模型) 连续触发冷却的次数（窗口内出现
	// 无降智样本即清零），用于重复触发指数退避；仅 observe_only=false 可达。
	consecutiveTriggers map[string]int
}

func newCodexDegradationRegistry() *codexDegradationRegistry {
	return &codexDegradationRegistry{
		entries:             make(map[string]*codexDegradationEntry),
		cooldownNext:        make(map[string]time.Time),
		consecutiveTriggers: make(map[string]int),
	}
}

func (s *OpenAIGatewayService) codexDegradationRegistry() *codexDegradationRegistry {
	if s == nil {
		return nil
	}
	s.openaiCodexDegradationOnce.Do(func() {
		s.openaiCodexDegradation = newCodexDegradationRegistry()
	})
	return s.openaiCodexDegradation
}

func (s *OpenAIGatewayService) codexDegradationEnabled() bool {
	if s == nil {
		return false
	}
	return s.codexDegradationConfig().Enabled
}

func (s *OpenAIGatewayService) codexDegradationConfig() config.CodexDegradationConfig {
	cfg := config.CodexDegradationConfig{}
	if s != nil && s.cfg != nil {
		cfg = s.cfg.Gateway.OpenAICodexDegradation
	}
	return cfg.Normalized()
}

func (s *OpenAIGatewayService) recordCodexDegradation(observer *codexDegradationObserver) {
	if observer == nil || observer.accountID <= 0 {
		return
	}
	// 零信号样本不得入库：WS attempt 在顶部 begin+defer finalize，build_ws_url /
	// 拨号失败等未触达上游的路径也会走到这里；若不门控，会被记成 Degraded=false
	// 的假阴性样本，稀释窗口比例并在 observe_only=false 时误导冷却。
	observer.mu.Lock()
	upstreamObserved := observer.headersObserved || observer.eventObserved
	observer.mu.Unlock()
	if !upstreamObserved {
		return
	}
	registry := s.codexDegradationRegistry()
	if registry == nil {
		return
	}
	cfg := s.codexDegradationConfig()
	now := time.Now().UTC()
	sample := observer.snapshot()
	key := openAICodexTicketKey(observer.accountID, observer.sentModel)

	registry.mu.Lock()
	entry := registry.entries[key]
	if entry == nil {
		entry = &codexDegradationEntry{accountID: observer.accountID, model: observer.sentModel}
		registry.entries[key] = entry
	}
	entry.total++
	if sample.Degraded {
		entry.degraded++
	}
	if sample.ModelMismatch {
		entry.modelMismatch++
	}
	if sample.SafetyBuffering {
		entry.safetyBuffering++
	}
	entry.samples = append(entry.samples, sample)
	if len(entry.samples) > codexDegradationMaxSamples {
		entry.samples = append([]codexDegradationSample(nil), entry.samples[len(entry.samples)-codexDegradationMaxSamples:]...)
	}
	if sample.Degraded {
		entry.lastDegradedAt = sample.At
	} else {
		// 窗口内出现无降智样本：清零连续触发计数，退避回到首次冷却时长。
		delete(registry.consecutiveTriggers, key)
	}
	entry.lastSignal = now
	observer.mu.Lock()
	if observer.servedModel != "" {
		entry.lastServedModel = observer.servedModel
	}
	if observer.fasterModel != "" {
		entry.lastFasterModel = observer.fasterModel
	}
	if observer.transport != "" {
		entry.lastTransport = observer.transport
	}
	if len(observer.safetyReasons) > 0 {
		entry.lastReasons = append([]string(nil), observer.safetyReasons...)
	}
	if observer.rateLimitReachedType != "" {
		entry.lastRateLimitReachedType = observer.rateLimitReachedType
	}
	if observer.activeLimit != "" {
		entry.lastActiveLimit = observer.activeLimit
	}
	if observer.creditsBalance != "" {
		entry.lastCreditsBalance = observer.creditsBalance
	}
	accountID := observer.accountID
	model := observer.sentModel
	servedModel := observer.servedModel
	fasterModel := observer.fasterModel
	transport := observer.transport
	observer.mu.Unlock()

	windowTotal, windowDegraded, hasWindow := entry.windowRatios(now, cfg.Window())
	registry.mu.Unlock()

	if !sample.Degraded {
		return
	}
	logger.L().Debug("codex degradation signal",
		zap.Int64("account_id", accountID),
		zap.String("model", model),
		zap.String("served_model", servedModel),
		zap.String("faster_model", fasterModel),
		zap.Bool("model_mismatch", sample.ModelMismatch),
		zap.Bool("safety_buffering", sample.SafetyBuffering),
		zap.String("transport", transport),
	)
	if cfg.ObserveOnly || !hasWindow || windowTotal < cfg.MinSamples {
		return
	}
	degradedRatio := float64(windowDegraded) / float64(windowTotal)
	if degradedRatio+1e-9 < cfg.DegradedRatioThreshold {
		return
	}
	cooldownSeconds, consecutiveTriggers, ok := s.allowCodexDegradationCooldown(registry, key, now, cfg)
	if !ok {
		return
	}
	logger.L().Warn("codex degradation cooldown applied",
		zap.Int64("account_id", accountID),
		zap.String("model", model),
		zap.Int("window_samples", windowTotal),
		zap.Int("window_degraded", windowDegraded),
		zap.Float64("degraded_ratio", degradedRatio),
		zap.Int("consecutive_triggers", consecutiveTriggers),
		zap.Int("cooldown_seconds", cooldownSeconds),
	)
	s.applyCodexDegradationCooldown(accountID, model, cooldownSeconds)
}

// allowCodexDegradationCooldown 判定本窗口是否允许对 key 再次冷却；允许时按连续触发
// 次数计算本次退避时长并在 registry.mu 内推进计数与下次可触发时间，保证并发触发只
// 生效一次。第 n 次连续触发（n≥1）时长 = cooldown_seconds × multiplier^(n-1)，封顶
// max_cooldown_seconds；multiplier<=1 时退避关闭，保持一次性冷却语义。
func (s *OpenAIGatewayService) allowCodexDegradationCooldown(registry *codexDegradationRegistry, key string, now time.Time, cfg config.CodexDegradationConfig) (int, int, bool) {
	if registry == nil {
		return 0, 0, false
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if next, ok := registry.cooldownNext[key]; ok && now.Before(next) {
		return 0, 0, false
	}
	consecutiveTriggers := registry.consecutiveTriggers[key]
	cooldownSeconds := codexDegradationBackoffSeconds(cfg, consecutiveTriggers)
	if cooldownSeconds <= 0 {
		return 0, 0, false
	}
	registry.cooldownNext[key] = now.Add(time.Duration(cooldownSeconds) * time.Second)
	registry.consecutiveTriggers[key] = consecutiveTriggers + 1
	return cooldownSeconds, consecutiveTriggers + 1, true
}

// codexDegradationBackoffSeconds 计算第 consecutive+1 次连续触发的冷却时长
// （consecutive 为已累积的连续触发次数）。
func codexDegradationBackoffSeconds(cfg config.CodexDegradationConfig, consecutive int) int {
	if cfg.CooldownSeconds <= 0 {
		return 0
	}
	seconds := cfg.CooldownSeconds
	if cfg.CooldownBackoffMultiplier > 1 && consecutive > 0 {
		scaled := float64(cfg.CooldownSeconds) * math.Pow(cfg.CooldownBackoffMultiplier, float64(consecutive))
		if math.IsInf(scaled, 0) || scaled > math.MaxInt32 {
			seconds = math.MaxInt32
		} else {
			seconds = int(scaled)
		}
	}
	if cfg.MaxCooldownSeconds > 0 && seconds > cfg.MaxCooldownSeconds {
		return cfg.MaxCooldownSeconds
	}
	return seconds
}

func (s *OpenAIGatewayService) applyCodexDegradationCooldown(accountID int64, model string, cooldownSeconds int) {
	if s == nil || s.accountRepo == nil || accountID <= 0 || cooldownSeconds <= 0 {
		return
	}
	// 必须使用调度器会检查的模型限流 scope（GetMappedModel 的结果）；
	// 自定义前缀 scope 不会被 isModelRateLimitedWithContext 读取，等于白写。
	model = normalizeCodexDegradationModel(model)
	if model == "" {
		return
	}
	resetAt := time.Now().Add(time.Duration(cooldownSeconds) * time.Second)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.accountRepo.SetModelRateLimit(ctx, accountID, model, resetAt, "codex_degradation"); err != nil {
			logger.L().Warn("codex degradation cooldown persist failed",
				zap.Int64("account_id", accountID),
				zap.String("model", model),
				zap.Error(err),
			)
		}
	}()
}

// CodexDegradationSnapshot 是管理端只读视图。
type CodexDegradationSnapshot struct {
	Config  config.CodexDegradationConfig `json:"config"`
	Entries []CodexDegradationEntryView   `json:"entries"`
}

type CodexDegradationEntryView struct {
	AccountID           int64     `json:"account_id"`
	Model               string    `json:"model"`
	Total               int64     `json:"total"`
	Degraded            int64     `json:"degraded"`
	ModelMismatch       int64     `json:"model_mismatch"`
	SafetyBuffering     int64     `json:"safety_buffering"`
	WindowSamples       int       `json:"window_samples"`
	WindowDegraded      int       `json:"window_degraded"`
	WindowDegradedRatio float64   `json:"window_degraded_ratio"`
	ConsecutiveTriggers int       `json:"consecutive_triggers,omitempty"`
	NextCooldownAt      time.Time `json:"next_cooldown_at,omitempty"`
	LastSignalAt        time.Time `json:"last_signal_at"`
	LastDegradedAt      time.Time `json:"last_degraded_at,omitempty"`
	LastServedModel     string    `json:"last_served_model,omitempty"`
	LastFasterModel     string    `json:"last_faster_model,omitempty"`
	LastTransport       string    `json:"last_transport,omitempty"`
	LastReasons         []string  `json:"last_reasons,omitempty"`
	// 配额上下文头族最近观测值（观测-only，上游未下发时为空）。
	LastRateLimitReachedType string `json:"last_rate_limit_reached_type,omitempty"`
	LastActiveLimit          string `json:"last_active_limit,omitempty"`
	LastCreditsBalance       string `json:"last_credits_balance,omitempty"`
}

func (s *OpenAIGatewayService) CodexDegradationMetrics() CodexDegradationSnapshot {
	cfg := s.codexDegradationConfig()
	snapshot := CodexDegradationSnapshot{Config: cfg}
	registry := s.codexDegradationRegistry()
	if registry == nil {
		return snapshot
	}
	now := time.Now().UTC()
	registry.mu.Lock()
	entries := make([]CodexDegradationEntryView, 0, len(registry.entries))
	for key, entry := range registry.entries {
		windowTotal, windowDegraded, _ := entry.windowRatios(now, cfg.Window())
		ratio := 0.0
		if windowTotal > 0 {
			ratio = float64(windowDegraded) / float64(windowTotal)
		}
		entries = append(entries, CodexDegradationEntryView{
			AccountID:                entry.accountID,
			Model:                    entry.model,
			Total:                    entry.total,
			Degraded:                 entry.degraded,
			ModelMismatch:            entry.modelMismatch,
			SafetyBuffering:          entry.safetyBuffering,
			WindowSamples:            windowTotal,
			WindowDegraded:           windowDegraded,
			WindowDegradedRatio:      ratio,
			ConsecutiveTriggers:      registry.consecutiveTriggers[key],
			NextCooldownAt:           registry.cooldownNext[key],
			LastSignalAt:             entry.lastSignal,
			LastDegradedAt:           entry.lastDegradedAt,
			LastServedModel:          entry.lastServedModel,
			LastFasterModel:          entry.lastFasterModel,
			LastTransport:            entry.lastTransport,
			LastReasons:              append([]string(nil), entry.lastReasons...),
			LastRateLimitReachedType: entry.lastRateLimitReachedType,
			LastActiveLimit:          entry.lastActiveLimit,
			LastCreditsBalance:       entry.lastCreditsBalance,
		})
	}
	registry.mu.Unlock()
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].AccountID != entries[j].AccountID {
			return entries[i].AccountID < entries[j].AccountID
		}
		return entries[i].Model < entries[j].Model
	})
	snapshot.Entries = entries
	return snapshot
}

// ResetCodexDegradationMetrics 清空进程内聚合（运维排障用）。
func (s *OpenAIGatewayService) ResetCodexDegradationMetrics() {
	registry := s.codexDegradationRegistry()
	if registry == nil {
		return
	}
	registry.mu.Lock()
	registry.entries = make(map[string]*codexDegradationEntry)
	registry.cooldownNext = make(map[string]time.Time)
	registry.consecutiveTriggers = make(map[string]int)
	registry.mu.Unlock()
}

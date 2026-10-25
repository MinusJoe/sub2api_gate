// Package transport 实现 openai.oauth.outbound_transport.v1 出站传输：
// HTTP 透传 + 官方契约对齐（turn-state 捕获/同线程回带、per-account cookie jar、
// (账号,代理) 粘性连接池）+ 降智观测。设计依据见插件 README 与
// scratch/REPORT-codex-ticket-alternatives-v2.md 的 P0 方案。
package transport

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/MACOS-DO/sub4api-plugins/codex-bridge/internal/metrics"
	"github.com/MACOS-DO/sub4api-plugins/codex-bridge/internal/pluginconfig"
	"github.com/MACOS-DO/sub4api-plugins/codex-bridge/internal/turnstate"
	"github.com/MACOS-DO/sub4api-plugins/codex-bridge/internal/wsbridge"
)

// 与宿主 service.PluginCapabilityOpenAIOAuthOutbound 同值（internal 包不可导入，本地声明）。
const capabilityOpenAIOAuthOutbound = "openai.oauth.outbound_transport.v1"

const (
	headerTurnState            = "x-codex-turn-state"
	headerClientRequestID      = "x-client-request-id"
	headerSessionID            = "session-id"
	headerSessionIDUnderscore  = "session_id"
	headerSafetyBuffering      = "x-codex-safety-buffering-enabled"
	headerSafetyBufferingModel = "x-codex-safety-buffering-faster-model"
	headerOpenAIModel          = "openai-model"
)

// cloudflareAllowlist 与官方客户端 chatgpt_cloudflare_cookies.rs 的白名单一致：
// 只持久化基础设施路由/防护 cookie，绝不持久化任何认证态。
var cloudflareAllowlist = map[string]struct{}{
	"__cf_bm": {}, "__cflb": {}, "__cfruid": {}, "__cfseq": {},
	"__cfwaitingroom": {}, "__oailb": {}, "_cfuvid": {},
	"cf_clearance": {}, "cf_ob_info": {}, "cf_use_ob": {},
}

func allowedCookie(name string) bool {
	if _, ok := cloudflareAllowlist[name]; ok {
		return true
	}
	return strings.HasPrefix(name, "cf_chl_")
}

// filteredJar 在官方白名单上过滤 Set-Cookie。
type filteredJar struct {
	inner http.CookieJar
}

func (j filteredJar) SetCookies(u *url.URL, cookies []*http.Cookie) {
	kept := cookies[:0]
	for _, c := range cookies {
		if allowedCookie(c.Name) {
			kept = append(kept, c)
		}
	}
	if len(kept) > 0 {
		j.inner.SetCookies(u, kept)
	}
}

func (j filteredJar) Cookies(u *url.URL) []*http.Cookie { return j.inner.Cookies(u) }

var modelFieldPattern = regexp.MustCompile(`"model"\s*:\s*"([^"]{1,128})"`)

// sniffingReader 在前 max 字节窗口内提取请求体的 "model" 字段（降智比对用），
// 不缓存、不改写字节流。
type sniffingReader struct {
	r        io.Reader
	max      int
	window   []byte
	modelOut *string
	done     bool
}

func (s *sniffingReader) Read(p []byte) (int, error) {
	n, err := s.r.Read(p)
	if n > 0 && !s.done {
		remaining := s.max - len(s.window)
		if remaining > n {
			remaining = n
		}
		s.window = append(s.window, p[:remaining]...)
		if match := modelFieldPattern.FindSubmatch(s.window); match != nil {
			*s.modelOut = string(match[1])
			s.done = true
		} else if len(s.window) >= s.max {
			s.done = true
		}
	}
	return n, err
}

// Outbound 是一次出站请求的完整描述（来自 Forward Start 帧）。
type Outbound struct {
	Method        string
	URL           string
	Host          string
	Header        http.Header
	Body          io.Reader
	ContentLength int64
	AccountID     int64
	ProxyURL      string
}

// Capture 是从上游响应头提取的观测信息。
type Capture struct {
	Ticket          string
	SafetyBuffering bool
	FasterModel     string
	ServedModel     string
}

// Forwarder 持有连接池与票存储；配置变更时整体重建。
type Forwarder struct {
	mu       sync.Mutex
	cfg      pluginconfig.Config
	clients  map[string]*http.Client
	store    *turnstate.Store
	recorder *metrics.Recorder
	counters *metrics.Counters
	bridge   *wsbridge.Bridge
}

// NewForwarder 创建转发器。
func NewForwarder(cfg pluginconfig.Config, store *turnstate.Store, recorder *metrics.Recorder, counters *metrics.Counters) *Forwarder {
	return &Forwarder{
		cfg:      cfg,
		clients:  make(map[string]*http.Client),
		store:    store,
		recorder: recorder,
		counters: counters,
		bridge:   wsbridge.New(store, recorder, counters),
	}
}

// ApplyConfig 原子切换配置：先校验，成功后重建连接池并关闭旧池空闲连接。
func (f *Forwarder) ApplyConfig(cfg pluginconfig.Config) error {
	normalized, err := cfg.Validate()
	if err != nil {
		return err
	}
	f.mu.Lock()
	old := f.clients
	f.cfg = normalized
	f.clients = make(map[string]*http.Client)
	f.mu.Unlock()
	for _, client := range old {
		if transport, ok := client.Transport.(*http.Transport); ok {
			transport.CloseIdleConnections()
		}
	}
	if !normalized.WSBridgeEnabled {
		f.bridge.CloseIdleConnections()
	}
	f.store.ReplaceTTL(time.Duration(normalized.TurnStateTTLSeconds) * time.Second)
	return nil
}

func (f *Forwarder) config() pluginconfig.Config {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cfg
}

// clientFor 返回 (账号, 代理) 维度的 HTTP 客户端：连接池与 cookie jar 都按账号
// 隔离——同一代理被多账号共用时绝不共享 cookie/连接（__cf_bm 跨账号复用是
// 官方单用户客户端不会产生的信号）。
func (f *Forwarder) clientFor(accountID int64, proxyURL string) (*http.Client, error) {
	key := fmt.Sprintf("%d\x00%s", accountID, proxyURL)
	f.mu.Lock()
	defer f.mu.Unlock()
	if client, ok := f.clients[key]; ok {
		return client, nil
	}
	transport := &http.Transport{
		MaxIdleConns:          f.cfg.MaxIdleConnsPerAccount * 2,
		MaxIdleConnsPerHost:   f.cfg.MaxIdleConnsPerAccount,
		IdleConnTimeout:       time.Duration(f.cfg.IdleConnTimeoutSeconds) * time.Second,
		TLSHandshakeTimeout:   time.Duration(f.cfg.DialTimeoutSeconds) * time.Second,
		ResponseHeaderTimeout: time.Duration(f.cfg.ResponseHeaderTimeoutSeconds) * time.Second,
		ExpectContinueTimeout: time.Second,
		ForceAttemptHTTP2:     true,
		DialContext: (&net.Dialer{
			Timeout:   time.Duration(f.cfg.DialTimeoutSeconds) * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
	}
	if proxyURL != "" {
		parsed, err := url.Parse(proxyURL)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			// 出站 IP 一致性红线：代理无效时宁可失败也不直连。
			return nil, fmt.Errorf("codex-bridge: 无效的代理 URL（拒绝直连）")
		}
		transport.Proxy = http.ProxyURL(parsed)
	}
	client := &http.Client{Transport: transport}
	if f.cfg.CookieJarEnabled {
		inner, err := cookiejar.New(nil)
		if err != nil {
			return nil, fmt.Errorf("codex-bridge: 初始化 cookie jar: %w", err)
		}
		client.Jar = filteredJar{inner: inner}
	}
	// 3xx 原样透传给宿主，由宿主决定后续行为。
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}
	f.clients[key] = client
	return client, nil
}

// threadKey 从请求头取会话线程标识：官方客户端 x-client-request-id=thread_id，
// 其次 session-id / session_id（宿主出站白名单按下划线键透传，Go canonical 键
// 是 Session_Id，与连字符形态互不命中）。三者皆缺时无线程归属：票簿记按
// turnstate.Key 的 fail-closed 语义整体跳过（不捕获/不回带），绝不退化为账号级
// 跨线程共享——OpenAI 兼容 API 调用方通常不带这些头，账号级键会把 A 会话的
// turn-state 注入 B 会话请求，官方协议不允许。
func threadKey(header http.Header) string {
	if v := header.Get(headerClientRequestID); v != "" {
		return v
	}
	if v := header.Get(headerSessionID); v != "" {
		return v
	}
	return header.Get(headerSessionIDUnderscore)
}

// bridgeCandidate 判断请求是否走 WS 桥：POST 且路径以 /responses 结尾
// （官方 Responses 端点；URL 见 codex-rs core/src/client.rs 的 /responses 常量）。
func bridgeCandidate(spec Outbound) bool {
	if spec.Method != http.MethodPost || spec.Body == nil {
		return false
	}
	parsed, err := url.Parse(spec.URL)
	if err != nil {
		return false
	}
	return strings.HasSuffix(parsed.Path, "/responses")
}

// observeDowngrade 统一降智观测（HTTP 与 WS 桥路径共用）。
func (f *Forwarder) observeDowngrade(cfg pluginconfig.Config, requestedModel string, capture Capture) {
	if !cfg.ObserveDowngrade {
		return
	}
	if capture.SafetyBuffering {
		f.counters.SafetyBufferingResponses.Add(1)
	}
	if requestedModel != "" && capture.ServedModel != "" && requestedModel != capture.ServedModel {
		f.recorder.ObserveDowngrade(metrics.DowngradeSample{
			RequestedModel: requestedModel,
			ServedModel:    capture.ServedModel,
			FasterModel:    capture.FasterModel,
			At:             time.Now().UTC().Format(time.RFC3339),
		})
	}
}

// Do 执行一次契约对齐后的出站请求。requestSent 仅在确认未触达上游时为 false。
// WS 桥开启时先尝试官方 WS 通道（预热铸票→帧级回带→事件流转 SSE），
// 桥失败自动回退 HTTP 透传（body 已缓冲，可安全重放）。
func (f *Forwarder) Do(ctx context.Context, spec Outbound) (resp *http.Response, capture Capture, requestSent bool, err error) {
	cfg := f.config()

	if cfg.WSBridgeEnabled && bridgeCandidate(spec) {
		buffered, readErr := io.ReadAll(spec.Body)
		if readErr != nil {
			return nil, capture, false, fmt.Errorf("codex-bridge: 读取请求体: %w", readErr)
		}
		if cfg.ManageTurnState {
			// 官方 HTTP 通道票回带走请求头：body client_metadata 里的外来票属
			// 异常形状。body 已缓冲（可安全重写），剥离后桥接与 HTTP 回退共用。
			buffered = wsbridge.StripClientMetadataTicket(buffered)
		}
		wsResp, wsCapture, bridgeErr := f.bridge.Request(ctx, cfg, wsbridge.Spec{
			URL:       spec.URL,
			Header:    spec.Header,
			AccountID: spec.AccountID,
			ProxyURL:  spec.ProxyURL,
			StoreKey:  turnstate.Key(spec.AccountID, threadKey(spec.Header)),
		}, buffered)
		if bridgeErr == nil {
			// 降智观测由桥在响应终态统一执行（served model 来自事件流）。
			merged := Capture{
				Ticket:          wsCapture.Ticket,
				SafetyBuffering: wsCapture.SafetyBuffering,
				FasterModel:     wsCapture.FasterModel,
			}
			f.counters.ResponsesTotal.Add(1)
			return wsResp, merged, true, nil
		}
		if errors.Is(bridgeErr, wsbridge.ErrRequestSent) {
			// 业务帧可能已送达上游：直接失败，绝不回退 HTTP 重放（双发风险）。
			return nil, capture, true, bridgeErr
		}
		f.counters.WSFallbackHTTP.Add(1)
		spec.Body = bytes.NewReader(buffered)
		spec.ContentLength = int64(len(buffered))
	}

	var requestedModel string
	body := spec.Body
	if cfg.ObserveDowngrade && cfg.ModelSniffBytes > 0 && body != nil {
		body = &sniffingReader{r: body, max: cfg.ModelSniffBytes, modelOut: &requestedModel}
	}

	req, err := http.NewRequestWithContext(ctx, spec.Method, spec.URL, body)
	if err != nil {
		return nil, capture, false, fmt.Errorf("codex-bridge: 构造请求: %w", err)
	}
	req.Header = spec.Header.Clone()
	if spec.Host != "" {
		req.Host = spec.Host
	}
	if spec.ContentLength > 0 {
		// 0（空体）与 -1（未知长度）交给 NewRequest 默认语义，
		// 避免误写 Content-Length: 0 截断仍有数据的请求体。
		req.ContentLength = spec.ContentLength
	}

	if cfg.ManageTurnState {
		// 接管票：先剥离宿主/下游注入的值（可能是跨账号共享票，协议上不允许），
		// 再按 (账号, 线程) 回带本插件捕获的票。无线程标识时键为空（turnstate.Key
		// fail-closed），Replay 恒 miss、Capture 恒 no-op，绝不退化为账号级回带。
		req.Header.Del(headerTurnState)
		key := turnstate.Key(spec.AccountID, threadKey(req.Header))
		if ticket, ok := f.store.Replay(key); ok {
			req.Header.Set(headerTurnState, ticket)
			f.counters.TicketsReplayed.Add(1)
		}
	}

	client, err := f.clientFor(spec.AccountID, spec.ProxyURL)
	if err != nil {
		return nil, capture, false, err
	}

	requestSent = true
	resp, err = client.Do(req)
	if err != nil {
		return nil, capture, requestSent, fmt.Errorf("codex-bridge: 上游请求: %w", err)
	}

	capture.Ticket = resp.Header.Get(headerTurnState)
	capture.SafetyBuffering = strings.EqualFold(strings.TrimSpace(resp.Header.Get(headerSafetyBuffering)), "true")
	capture.FasterModel = resp.Header.Get(headerSafetyBufferingModel)
	capture.ServedModel = resp.Header.Get(headerOpenAIModel)

	if cfg.ManageTurnState && capture.Ticket != "" {
		key := turnstate.Key(spec.AccountID, threadKey(req.Header))
		f.store.Capture(key, capture.Ticket)
		if key != "" {
			// 无线程标识的票没有可回带的归属，不入库也不计捕获。
			f.counters.TicketsCaptured.Add(1)
		}
	}
	f.observeDowngrade(cfg, requestedModel, capture)
	f.counters.ResponsesTotal.Add(1)
	return resp, capture, requestSent, nil
}

// StoreLen 暴露票存储大小（观测）。
func (f *Forwarder) StoreLen() int { return f.store.Len() }

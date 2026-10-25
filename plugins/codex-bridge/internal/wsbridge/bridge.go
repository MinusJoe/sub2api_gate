// Package wsbridge 把宿主发来的 HTTP POST /responses 请求桥接到官方 Responses
// WebSocket 通道（wss + OpenAI-Beta: responses_websockets=2026-02-06），
// 对宿主仍呈现 HTTP+SSE 响应。实现官方契约三件套：
//
//  1. generate=false 预热帧：连接建立即铸票（官方线程启动同款，无生成 token）
//  2. response.metadata 事件捕获：官方 WS 读票通道（headers map 里的 x-codex-turn-state）
//  3. 帧级回带：票写进每条 response.create 的请求体 client_metadata（官方 WS 回带通道）
//
// 契约锚点（rust-v0.164.0-alpha.11.1）：codex-rs/core/src/client.rs:2117-2119（帧级回带）、
// codex-api/src/endpoint/responses_websocket.rs:849-853（事件捕获）、client.rs:2187（generate=false）、
// 官方测试 agent_websocket.rs:564-642（预热铸票→首轮回带，逐项断言）。
package wsbridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"github.com/MACOS-DO/sub4api-plugins/codex-bridge/internal/metrics"
	"github.com/MACOS-DO/sub4api-plugins/codex-bridge/internal/pluginconfig"
	"github.com/MACOS-DO/sub4api-plugins/codex-bridge/internal/turnstate"
)

const (
	openAIBetaWebsockets = "responses_websockets=2026-02-06"
	frameResponseCreate  = "response.create"
	eventResponseMeta    = "response.metadata"
	clientMetadataField  = "client_metadata"
	clientMetadataTicket = "x-codex-turn-state"
	headerSafetyEnabled  = "x-codex-safety-buffering-enabled"
	headerSafetyFaster   = "x-codex-safety-buffering-faster-model"
	headerOpenAIModel    = "openai-model"
	// maxBridgeBodyBytes 超过该大小的请求体不桥接（回退 HTTP 流式）。
	maxBridgeBodyBytes = 16 << 20
)

// ErrRequestSent 标记"业务帧可能已送达上游"的失败：调用方不得回退 HTTP 重放
// （会造成同一请求双发），必须直接失败并如实上报 request_sent=true。
var ErrRequestSent = errors.New("wsbridge: 业务帧已尝试发送，禁止重放")

// Spec 是一次桥接请求的入参（由 transport.Forwarder 组装）。
type Spec struct {
	URL       string
	Header    http.Header
	AccountID int64
	ProxyURL  string
	// StoreKey 是票存储键（账号+线程），由调用方按统一规则生成。
	StoreKey string
}

// Capture 是桥接过程中的观测信息（与 transport.Capture 同形，避免环导入）。
// 同步字段在 Request 返回时已填好；流式阶段才知道的值在 Live 里（Done 在响应体
// EOF 前置位，调用方读完 Body 后即可读取）。
type Capture struct {
	Ticket          string
	PrewarmTicket   bool
	SafetyBuffering bool
	FasterModel     string
	// RequestedModel 是从请求体提取的模型名（观测用）。
	RequestedModel string
	Live           *LiveFields
}

// LiveFields 承载事件流阶段补齐的观测值（并发安全）。
type LiveFields struct {
	mu          sync.Mutex
	done        bool
	servedModel string
	ticket      string
}

func (l *LiveFields) setServed(model string) {
	if l == nil || model == "" {
		return
	}
	l.mu.Lock()
	l.servedModel = model
	l.mu.Unlock()
}

func (l *LiveFields) setTicket(ticket string) {
	if l == nil || ticket == "" {
		return
	}
	l.mu.Lock()
	l.ticket = ticket
	l.mu.Unlock()
}

func (l *LiveFields) finish() {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.done = true
	l.mu.Unlock()
}

// Snapshot 返回 (done, servedModel, ticket)；响应体 EOF 后 done 恒为 true。
func (l *LiveFields) Snapshot() (bool, string, string) {
	if l == nil {
		return true, "", ""
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.done, l.servedModel, l.ticket
}

// Bridge 维护 (账号, 代理) 维度的 WS 连接池并执行桥接。
type Bridge struct {
	store    *turnstate.Store
	recorder *metrics.Recorder
	counters *metrics.Counters

	mu     sync.Mutex
	pools  map[string]*connPool
	dialer map[string]*http.Transport
}

// New 创建桥。store 与 HTTP 路径共享（票互通）。
func New(store *turnstate.Store, recorder *metrics.Recorder, counters *metrics.Counters) *Bridge {
	return &Bridge{
		store:    store,
		recorder: recorder,
		counters: counters,
		pools:    make(map[string]*connPool),
		dialer:   make(map[string]*http.Transport),
	}
}

// CloseIdleConnections 关闭全部空闲连接（配置切换时调用）。
func (b *Bridge) CloseIdleConnections() {
	b.mu.Lock()
	pools := b.pools
	b.pools = make(map[string]*connPool)
	dialers := b.dialer
	b.dialer = make(map[string]*http.Transport)
	b.mu.Unlock()
	for _, pool := range pools {
		pool.retire()
	}
	for _, transport := range dialers {
		transport.CloseIdleConnections()
	}
}

type pooledConn struct {
	ws       *websocket.Conn
	lastUsed time.Time
	// dead 是 ws 判死的自有标记（coder/websocket v1.8.14 未导出 IsClosed）。
	// 所有主动关闭路径必须先 markDead，acquire 据此等价跳过已关闭连接，
	// 避免陈旧连接写帧失败走 ErrRequestSent 硬失败。
	dead atomic.Bool
}

func (c *pooledConn) markDead() {
	if c != nil {
		c.dead.Store(true)
	}
}

func (c *pooledConn) isDead() bool {
	return c != nil && c.dead.Load()
}

type connPool struct {
	mu    sync.Mutex
	idle  []*pooledConn
	limit int
	// retired 标记池已退役（配置切换换池）：在飞 streamEvents 的 defer release
	// 据此关连接不入池，否则连接还进无人引用的旧池造成 FD 泄漏。
	retired atomic.Bool
}

func (p *connPool) acquire(idleTTL time.Duration) *pooledConn {
	p.mu.Lock()
	defer p.mu.Unlock()
	for len(p.idle) > 0 {
		conn := p.idle[len(p.idle)-1]
		p.idle = p.idle[:len(p.idle)-1]
		if conn.isDead() || (idleTTL > 0 && time.Since(conn.lastUsed) > idleTTL) {
			// 已关闭或空闲超龄的连接弃用：关连接不入池、不交给调用方。
			conn.markDead()
			conn.ws.CloseNow()
			continue
		}
		return conn
	}
	return nil
}

func (p *connPool) release(conn *pooledConn) {
	if conn == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.retired.Load() || conn.isDead() || len(p.idle) >= p.limit {
		conn.markDead()
		conn.ws.CloseNow()
		return
	}
	conn.lastUsed = time.Now()
	p.idle = append(p.idle, conn)
}

func (p *connPool) closeAll() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, conn := range p.idle {
		conn.markDead()
		conn.ws.CloseNow()
	}
	p.idle = nil
}

// retire 标记池退役并关闭全部空闲连接（必须在 closeAll 前置位，保证此后任何
// release 都在 p.mu 下看到 retired 并关连接，不再入池）。
func (p *connPool) retire() {
	p.retired.Store(true)
	p.closeAll()
}

func (p *connPool) setLimit(limit int) {
	p.mu.Lock()
	p.limit = limit
	p.mu.Unlock()
}

func poolKey(accountID int64, proxyURL string) string {
	return fmt.Sprintf("%d\x00%s", accountID, proxyURL)
}

func (b *Bridge) poolFor(accountID int64, proxyURL string, limit int) *connPool {
	key := poolKey(accountID, proxyURL)
	b.mu.Lock()
	pool, ok := b.pools[key]
	if !ok {
		pool = &connPool{limit: limit}
		b.pools[key] = pool
	}
	b.mu.Unlock()
	// limit 的读写统一走 p.mu：此前在 b.mu 下直写，与 release/acquire 的
	// p.mu 不构成同步（数据竞争）。
	pool.setLimit(limit)
	return pool
}

func (b *Bridge) dialTransport(proxyURL string, timeout time.Duration) (*http.Transport, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if transport, ok := b.dialer[proxyURL]; ok {
		return transport, nil
	}
	transport := &http.Transport{
		TLSHandshakeTimeout:   timeout,
		ExpectContinueTimeout: time.Second,
		ForceAttemptHTTP2:     false,
		DialContext: (&net.Dialer{
			Timeout:   timeout,
			KeepAlive: 30 * time.Second,
		}).DialContext,
	}
	if proxyURL != "" {
		parsed, err := url.Parse(proxyURL)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			// 出站 IP 一致性红线：代理无效时宁可失败也不直连。
			return nil, errors.New("wsbridge: 无效的代理 URL（拒绝直连）")
		}
		transport.Proxy = http.ProxyURL(parsed)
	}
	b.dialer[proxyURL] = transport
	return transport, nil
}

// toWSURL 把 https/http URL 换成 wss/ws（官方由 /responses URL 换 scheme 建连，
// codex-client/src/provider.rs:91-102）。
func toWSURL(rawURL string) (string, error) {
	switch {
	case strings.HasPrefix(rawURL, "https://"):
		return "wss://" + strings.TrimPrefix(rawURL, "https://"), nil
	case strings.HasPrefix(rawURL, "http://"):
		return "ws://" + strings.TrimPrefix(rawURL, "http://"), nil
	case strings.HasPrefix(rawURL, "wss://"), strings.HasPrefix(rawURL, "ws://"):
		return rawURL, nil
	}
	return "", fmt.Errorf("wsbridge: 不支持的 URL scheme: 需 http(s)")
}

// handshakeHeader 从宿主请求头构造 WS 握手头：保留身份/认证/业务头，
// 剥离逐跳与 HTTP body 语义头；OpenAI-Beta 覆盖为 WS 专用值。
func handshakeHeader(src http.Header) http.Header {
	out := src.Clone()
	for _, name := range []string{
		"Host", "Content-Length", "Content-Type", "Transfer-Encoding",
		"Connection", "Upgrade", "Keep-Alive", "TE", "Trailer",
		"Accept", "Accept-Encoding", "OpenAI-Beta",
		// 官方 WS 握手从不带票（client.rs:1425 turn_state=None）：宿主注入的
		// 逐跳票必须剥离，票只走帧级 client_metadata 回带。
		clientMetadataTicket,
	} {
		out.Del(name)
	}
	out.Set("OpenAI-Beta", openAIBetaWebsockets)
	return out
}

type eventEnvelope struct {
	Type     string            `json:"type"`
	Headers  map[string]string `json:"headers"`
	Response struct {
		Model string `json:"model"`
	} `json:"response"`
}

func terminalEvent(eventType string) bool {
	switch eventType {
	case "response.completed", "response.failed", "response.cancelled", "response.terminated", "error":
		return true
	}
	return false
}

// BuildCreateFrame 把 HTTP 请求体转换为 response.create 帧：
// +type +stream=true +store=false -background；票注入 client_metadata（帧级回带）。
// 无票时剥离 client_metadata 里的外来票（跨账号/跨线程共享票协议上不允许穿透）。
// 返回帧字节与请求模型名。非 JSON 对象体返回错误（调用方回退 HTTP）。
func BuildCreateFrame(body []byte, ticket string) (frame []byte, requestedModel string, err error) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, "", fmt.Errorf("wsbridge: 请求体不是 JSON: %w", err)
	}
	delete(payload, "background")
	payload["stream"] = true
	payload["type"] = frameResponseCreate
	payload["store"] = false
	metadata, _ := payload[clientMetadataField].(map[string]any)
	if metadata == nil {
		metadata = make(map[string]any, 1)
	}
	if ticket != "" {
		metadata[clientMetadataTicket] = ticket
	} else {
		delete(metadata, clientMetadataTicket)
	}
	if len(metadata) > 0 {
		payload[clientMetadataField] = metadata
	} else {
		delete(payload, clientMetadataField)
	}
	if model, ok := payload["model"].(string); ok {
		requestedModel = model
	}
	frame, err = json.Marshal(payload)
	if err != nil {
		return nil, "", fmt.Errorf("wsbridge: 序列化 response.create: %w", err)
	}
	return frame, requestedModel, nil
}

// StripClientMetadataTicket 从已缓冲的 JSON 请求体剥离 client_metadata 里的外来票
// （官方 HTTP 通道票回带走请求头，body 内票属异常形状），供 HTTP 回退路径重放前
// 清洗。非 JSON 对象体或无票时原样返回。
func StripClientMetadataTicket(body []byte) []byte {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return body
	}
	metadata, ok := payload[clientMetadataField].(map[string]any)
	if !ok {
		return body
	}
	if _, has := metadata[clientMetadataTicket]; !has {
		return body
	}
	delete(metadata, clientMetadataTicket)
	if len(metadata) == 0 {
		delete(payload, clientMetadataField)
	}
	cleaned, err := json.Marshal(payload)
	if err != nil {
		return body
	}
	return cleaned
}

func writeFrame(ctx context.Context, conn *websocket.Conn, payload []byte) error {
	writer, err := conn.Writer(ctx, websocket.MessageText)
	if err != nil {
		return err
	}
	if _, err := writer.Write(payload); err != nil {
		writer.Close()
		return err
	}
	return writer.Close()
}

func readEvent(ctx context.Context, conn *websocket.Conn) (raw json.RawMessage, envelope eventEnvelope, err error) {
	_, reader, err := conn.Reader(ctx)
	if err != nil {
		return nil, envelope, err
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, envelope, err
	}
	if err := json.Unmarshal(data, &envelope); err != nil || envelope.Type == "" {
		// 非事件 JSON（心跳等）跳过。
		return raw, eventEnvelope{}, nil
	}
	return json.RawMessage(data), envelope, nil
}

// prewarm 在新连接上发 generate=false 帧（复制业务帧形状，与网关生产实现一致），
// 等待终态事件，捕获 response.metadata 里的票。
func (b *Bridge) prewarm(ctx context.Context, conn *websocket.Conn, businessFrame []byte, storeKey string) (string, error) {
	var payload map[string]any
	if err := json.Unmarshal(businessFrame, &payload); err != nil {
		return "", err
	}
	payload["generate"] = false
	// 官方 Base 预热是空 input（startup_prewarm.rs:398-399 PrewarmInput::Base => 空），
	// 带 generate=false 就不发全量上下文——剥离 input 对齐官方形状。
	delete(payload, "input")
	// 官方预热不带链锚（client.rs:2169-2185 continuation=None）。
	delete(payload, "previous_response_id")
	// 预热帧自身不再回带票。
	if metadata, ok := payload["client_metadata"].(map[string]any); ok {
		delete(metadata, clientMetadataTicket)
		if len(metadata) == 0 {
			delete(payload, "client_metadata")
		}
	}
	frame, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	b.counters.WSPrewarmAttempts.Add(1)
	if err := writeFrame(ctx, conn, frame); err != nil {
		return "", fmt.Errorf("wsbridge: 预热帧写入: %w", err)
	}
	var ticket string
	for {
		_, envelope, err := readEvent(ctx, conn)
		if err != nil {
			return "", fmt.Errorf("wsbridge: 预热读事件: %w", err)
		}
		if envelope.Type == eventResponseMeta {
			if value, ok := lookupHeader(envelope.Headers, clientMetadataTicket); ok {
				ticket = value
			}
			continue
		}
		if terminalEvent(envelope.Type) {
			break
		}
	}
	if ticket != "" {
		b.counters.WSPrewarmTickets.Add(1)
		// 无线程标识（storeKey 为空）的票没有可回带的归属，不入库也不计捕获；
		// 预热票仍经返回值在同回合注入（官方契约）。
		if storeKey != "" {
			b.counters.TicketsCaptured.Add(1)
			b.store.Capture(storeKey, ticket)
		}
	}
	return ticket, nil
}

func lookupHeader(headers map[string]string, name string) (string, bool) {
	for key, value := range headers {
		if strings.EqualFold(key, name) {
			return value, true
		}
	}
	return "", false
}

// Request 执行一次完整桥接。返回的 http.Response 是合成的 SSE 流（200），
// 调用方负责读完并关闭 Body。失败发生在回传开始前时返回 error（调用方回退 HTTP）。
func (b *Bridge) Request(ctx context.Context, cfg pluginconfig.Config, spec Spec, body []byte) (*http.Response, Capture, error) {
	var capture Capture
	capture.Live = &LiveFields{}
	if len(body) > maxBridgeBodyBytes {
		return nil, capture, errors.New("wsbridge: 请求体过大")
	}
	wsURL, err := toWSURL(spec.URL)
	if err != nil {
		return nil, capture, err
	}

	pool := b.poolFor(spec.AccountID, spec.ProxyURL, cfg.WSMaxPoolSize)
	// storeKey 为空表示无线程标识（turnstate.Key 的 fail-closed 语义）：本请求
	// 不从存储回带票、票捕获不落库；预热铸票仍按官方契约在同回合注入
	// （BuildCreateFrame 直用 prewarm 返回值，不经存储）。
	storeKey := spec.StoreKey

	// 复用空闲连接；无则拨新（带预热）。
	var conn *pooledConn
	reused := false
	if idle := pool.acquire(time.Duration(cfg.WSIdleTTLSeconds) * time.Second); idle != nil {
		conn = idle
		reused = true
	} else {
		handshakeCtx, cancel := context.WithTimeout(ctx, time.Duration(cfg.WSHandshakeTimeoutSeconds)*time.Second)
		transport, err := b.dialTransport(spec.ProxyURL, time.Duration(cfg.WSHandshakeTimeoutSeconds)*time.Second)
		if err != nil {
			cancel()
			return nil, capture, err
		}
		ws, resp, err := websocket.Dial(handshakeCtx, wsURL, &websocket.DialOptions{
			HTTPHeader: handshakeHeader(spec.Header),
			HTTPClient: &http.Client{Transport: transport},
		})
		cancel()
		if err != nil {
			return nil, capture, fmt.Errorf("wsbridge: 握手: %w", err)
		}
		if resp != nil {
			capture.SafetyBuffering = strings.EqualFold(strings.TrimSpace(resp.Header.Get(headerSafetyEnabled)), "true")
			capture.FasterModel = resp.Header.Get(headerSafetyFaster)
			if model := resp.Header.Get(headerOpenAIModel); model != "" {
				capture.Live.setServed(model)
			}
		}
		conn = &pooledConn{ws: ws}
	}

	// coder/websocket 默认单消息读上限 32KiB，长会话 response.completed 必超限
	// （1009 断流）：显式设置读上限。复用连接同样重设，配置热更立即生效。
	conn.ws.SetReadLimit(cfg.WSReadLimitBytes)

	discard := func() {
		conn.markDead()
		conn.ws.CloseNow()
	}
	if reused {
		// 复用连接上读取头部信号不可用；观测字段留给事件流补齐。
	}

	// 先按现有票建帧；新连接预热铸出的票会覆盖注入。
	ticket, _ := b.store.Replay(storeKey)
	if ticket != "" {
		capture.Ticket = ticket
	}
	frame, requestedModel, err := BuildCreateFrame(body, ticket)
	if err != nil {
		if !reused {
			discard()
		} else {
			pool.release(conn)
		}
		return nil, capture, err
	}
	capture.RequestedModel = requestedModel

	if !reused && cfg.WSPrewarmEnabled {
		prewarmCtx, cancel := context.WithTimeout(ctx, time.Duration(cfg.WSEventIdleTimeoutSeconds)*time.Second)
		prewarmTicket, err := b.prewarm(prewarmCtx, conn.ws, frame, storeKey)
		cancel()
		if err != nil {
			discard()
			return nil, capture, fmt.Errorf("wsbridge: 预热: %w", err)
		}
		if prewarmTicket != "" {
			capture.Ticket = prewarmTicket
			capture.PrewarmTicket = true
			// 用预热票重建业务帧（官方：首轮回放 warmup 票，agent_websocket.rs:639-642）。
			frame, requestedModel, err = BuildCreateFrame(body, prewarmTicket)
			if err != nil {
				discard()
				return nil, capture, err
			}
			capture.RequestedModel = requestedModel
		}
	}

	if ticket != "" || (capture.Ticket != "" && capture.PrewarmTicket) {
		b.counters.TicketsReplayed.Add(1)
	}

	if err := writeFrame(ctx, conn.ws, frame); err != nil {
		discard()
		// 帧写入失败无法确认上游是否已收到：禁止调用方回退 HTTP 重放。
		return nil, capture, fmt.Errorf("%w: %w", ErrRequestSent, err)
	}
	b.counters.WSBridgedRequests.Add(1)

	// 合成 SSE 响应：事件循环写入管道，宿主侧读 Body。
	pipeReader, pipeWriter := io.Pipe()
	header := http.Header{}
	header.Set("Content-Type", "text/event-stream")
	response := &http.Response{
		StatusCode:    http.StatusOK,
		Status:        "200 OK",
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        header,
		Body:          pipeReader,
		ContentLength: -1,
	}

	idleTimeout := time.Duration(cfg.WSEventIdleTimeoutSeconds) * time.Second
	go b.streamEvents(ctx, conn, pool, pipeWriter, storeKey, idleTimeout, &capture, reused)
	return response, capture, nil
}

// streamEvents 读上游事件：捕获票/观测字段 → 写 SSE；终态后归还连接。
// response.metadata 是 WS 控制事件，只捕获不下发（HTTP 语义没有该事件）。
// 降智观测在终态时统一执行（served model 来自事件流，晚于 Request 返回）。
func (b *Bridge) streamEvents(parent context.Context, conn *pooledConn, pool *connPool, writer *io.PipeWriter, storeKey string, idleTimeout time.Duration, capture *Capture, reused bool) {
	idleCtx, cancel := context.WithCancel(parent)
	defer cancel()
	timer := time.AfterFunc(idleTimeout, cancel)
	defer timer.Stop()

	healthy := true
	reachedTerminal := false
	defer func() {
		if reachedTerminal {
			b.observeDowngrade(capture)
		}
		capture.Live.finish()
		if healthy {
			pool.release(conn)
			writer.Close()
		} else {
			conn.markDead()
			conn.ws.CloseNow()
			writer.Close()
		}
	}()

	for {
		timer.Reset(idleTimeout)
		raw, envelope, err := readEvent(idleCtx, conn.ws)
		if err != nil {
			healthy = false
			if parent.Err() == nil && idleCtx.Err() != nil {
				// 事件空闲超时中断上游：writer.Close() 会把中断伪装成正常 EOF，
				// 宿主把半截响应当完整响应。必须携带中断原因收尾，宿主可见。
				writer.CloseWithError(fmt.Errorf("wsbridge: 事件空闲超时 %s，上游中断: %w", idleTimeout, err))
				return
			}
			if parent.Err() == nil && idleCtx.Err() == nil {
				b.counters.WSErrors.Add(1)
				writer.CloseWithError(fmt.Errorf("wsbridge: 读事件: %w", err))
				return
			}
			writer.Close()
			return
		}
		if envelope.Type == "" {
			continue
		}
		switch {
		case envelope.Type == eventResponseMeta:
			if value, ok := lookupHeader(envelope.Headers, clientMetadataTicket); ok {
				// 票只写 Live（并发安全）：Request 返回时按值拷贝 capture，
				// goroutine 直写 capture.Ticket/PrewarmTicket 与宿主读取构成
				// string 双字撕裂竞争（历史 cachedAt 同类）。
				capture.Live.setTicket(value)
				if storeKey != "" {
					// 无线程标识（storeKey 为空）的票不入库不计捕获（fail-closed）。
					b.counters.TicketsCaptured.Add(1)
					b.store.Capture(storeKey, value)
				}
			}
			continue // 控制事件不下发
		case envelope.Type == "response.created":
			if envelope.Response.Model != "" {
				capture.Live.setServed(envelope.Response.Model)
			}
		}
		if _, err := writer.Write(append(append([]byte("data: "), raw...), '\n', '\n')); err != nil {
			// 宿主已断开：继续排空上游到终态（与网关行为一致）。
			continue
		}
		if terminalEvent(envelope.Type) {
			reachedTerminal = true
			return
		}
	}
}

// observeDowngrade 在响应终态统一执行降智观测（requested vs served + safety 头）。
func (b *Bridge) observeDowngrade(capture *Capture) {
	_, servedModel, _ := capture.Live.Snapshot()
	if capture.SafetyBuffering {
		b.counters.SafetyBufferingResponses.Add(1)
	}
	if capture.RequestedModel != "" && servedModel != "" && capture.RequestedModel != servedModel {
		b.recorder.ObserveDowngrade(metrics.DowngradeSample{
			RequestedModel: capture.RequestedModel,
			ServedModel:    servedModel,
			FasterModel:    capture.FasterModel,
			At:             time.Now().UTC().Format(time.RFC3339),
		})
	}
}

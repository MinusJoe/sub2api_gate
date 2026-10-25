package wsbridge

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/MACOS-DO/sub4api-plugins/codex-bridge/internal/metrics"
	"github.com/MACOS-DO/sub4api-plugins/codex-bridge/internal/pluginconfig"
	"github.com/MACOS-DO/sub4api-plugins/codex-bridge/internal/turnstate"
)

const fakePrewarmTicket = "gAAAAAprewarm-ticket-780chars"

// fakeWSUpstream 模拟官方 Responses WS 上游：
// generate=false 帧 → 回 response.metadata（带票）+ completed；
// 业务帧 → 记录 client_metadata 票后回 created/delta/completed。同一连接可循环服务。
func fakeWSUpstream(t *testing.T) (*httptest.Server, func() []map[string]any) {
	t.Helper()
	var mu sync.Mutex
	var frames []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		ctx := r.Context()
		writeEvent := func(event map[string]any) bool {
			payload, err := json.Marshal(event)
			if err != nil {
				return false
			}
			writer, err := conn.Writer(ctx, websocket.MessageText)
			if err != nil {
				return false
			}
			if _, err := writer.Write(payload); err != nil {
				writer.Close()
				return false
			}
			return writer.Close() == nil
		}
		for {
			_, reader, err := conn.Reader(ctx)
			if err != nil {
				return
			}
			data, err := io.ReadAll(reader)
			if err != nil {
				return
			}
			var frame map[string]any
			if err := json.Unmarshal(data, &frame); err != nil {
				continue
			}
			mu.Lock()
			frames = append(frames, frame)
			mu.Unlock()
			generate, hasGenerate := frame["generate"]
			model, _ := frame["model"].(string)
			if hasGenerate && generate == false {
				// 预热：铸票（官方 response.metadata 事件通道）。
				if !writeEvent(map[string]any{
					"type":    "response.metadata",
					"headers": map[string]any{"x-codex-turn-state": fakePrewarmTicket},
				}) {
					return
				}
				if !writeEvent(map[string]any{"type": "response.completed", "response": map[string]any{"id": "warm-1"}}) {
					return
				}
				continue
			}
			// 业务响应：created（带 served model）→ delta → completed。
			if !writeEvent(map[string]any{"type": "response.created", "response": map[string]any{"id": "resp-1", "model": model}}) ||
				!writeEvent(map[string]any{"type": "response.output_text.delta", "delta": "hello"}) ||
				!writeEvent(map[string]any{"type": "response.completed", "response": map[string]any{"id": "resp-1"}}) {
				return
			}
		}
	}))
	t.Cleanup(server.Close)
	return server, func() []map[string]any {
		mu.Lock()
		defer mu.Unlock()
		out := make([]map[string]any, len(frames))
		copy(out, frames)
		return out
	}
}

func newTestBridge(t *testing.T) (*Bridge, *metrics.Counters, *turnstate.Store) {
	t.Helper()
	counters := metrics.New()
	store := turnstate.NewStore(time.Minute)
	recorder := metrics.NewRecorder(counters)
	return New(store, recorder, counters), counters, store
}

func bridgeSpec(serverURL string, thread string) Spec {
	header := http.Header{}
	header.Set("x-client-request-id", thread)
	header.Set("authorization", "Bearer test")
	header.Set("content-type", "application/json")
	return Spec{
		URL:       serverURL + "/backend-api/codex/responses",
		Header:    header,
		AccountID: 71,
		StoreKey:  turnstate.Key(71, thread),
	}
}

func readAllSSE(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return string(data)
}

func TestBridgePrewarmMintsTicketAndFrameReplays(t *testing.T) {
	bridge, counters, store := newTestBridge(t)
	server, recorded := fakeWSUpstream(t)
	cfg := pluginconfig.Default()
	cfg.WSBridgeEnabled = true
	ctx := context.Background()

	body := []byte(`{"model":"gpt-6-astra","instructions":"base","input":[],"stream":true}`)

	// 第一个请求：新连接 → 预热铸票 → 业务帧带票（官方首轮回放 warmup 票）。
	resp, capture, err := bridge.Request(ctx, cfg, bridgeSpec(server.URL, "thread-ws-1"), body)
	require.NoError(t, err)
	sse1 := readAllSSE(t, resp)

	assert.Equal(t, fakePrewarmTicket, capture.Ticket, "预热票应进入 capture")
	assert.True(t, capture.PrewarmTicket)
	assert.Equal(t, "gpt-6-astra", capture.RequestedModel)
	done, served, _ := capture.Live.Snapshot()
	assert.True(t, done, "响应体 EOF 后 Live 必须 done")
	assert.Equal(t, "gpt-6-astra", served, "response.created 的 model 应补齐 served model")
	assert.Contains(t, sse1, `"type":"response.created"`)
	assert.Contains(t, sse1, `"type":"response.output_text.delta"`)
	assert.Contains(t, sse1, `"type":"response.completed"`)
	assert.NotContains(t, sse1, "response.metadata", "控制事件不得下发到 SSE")
	assert.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))

	// 第二个请求（同线程）：应复用连接（无新预热），票从存储回带。
	resp2, capture2, err := bridge.Request(ctx, cfg, bridgeSpec(server.URL, "thread-ws-1"), body)
	require.NoError(t, err)
	_ = readAllSSE(t, resp2)
	assert.Equal(t, fakePrewarmTicket, capture2.Ticket)

	frames := recorded()
	require.Len(t, frames, 3, "1 预热 + 2 业务")
	prewarmFrame := frames[0]
	assert.Equal(t, "response.create", prewarmFrame["type"])
	assert.Equal(t, false, prewarmFrame["generate"])
	assert.NotContains(t, prewarmFrame, "input", "预热帧必须剥离 input（官方 Base 预热为空 input）")
	prewarmMeta, _ := prewarmFrame["client_metadata"].(map[string]any)
	assert.Empty(t, prewarmMeta["x-codex-turn-state"], "预热帧自身不带票")

	for _, businessFrame := range frames[1:3] {
		metadata, _ := businessFrame["client_metadata"].(map[string]any)
		require.NotNil(t, metadata, "业务帧必须带 client_metadata")
		assert.Equal(t, fakePrewarmTicket, metadata["x-codex-turn-state"], "帧级回带（官方 WS 契约通道）")
	}

	assert.Equal(t, uint64(2), counters.WSBridgedRequests.Load())
	assert.Equal(t, uint64(1), counters.WSPrewarmAttempts.Load(), "连接复用只预热一次")
	assert.Equal(t, uint64(1), counters.WSPrewarmTickets.Load())
	assert.Equal(t, uint64(1), counters.TicketsCaptured.Load())
	assert.Equal(t, uint64(2), counters.TicketsReplayed.Load())
	ticket, ok := store.Replay(turnstate.Key(71, "thread-ws-1"))
	require.True(t, ok)
	assert.Equal(t, fakePrewarmTicket, ticket)
}

// 无线程标识（StoreKey 为空）的桥接请求：预热铸票仅限同回合帧级注入（官方契约，
// agent_websocket.rs:639-642），不得入库被后续回合/他线程回带。修复前 StoreKey
// 退化为账号级键，跨线程共享票。
func TestBridgeHeaderlessRequestDoesNotStoreOrReplayTicket(t *testing.T) {
	bridge, counters, store := newTestBridge(t)
	server, recorded := fakeWSUpstream(t)
	cfg := pluginconfig.Default()
	cfg.WSBridgeEnabled = true
	ctx := context.Background()

	body := []byte(`{"model":"gpt-6-astra","instructions":"base","input":[],"stream":true}`)

	// 第一个无线程头请求：新连接预热铸票，同回合帧级注入。
	resp, capture, err := bridge.Request(ctx, cfg, bridgeSpec(server.URL, ""), body)
	require.NoError(t, err)
	_ = readAllSSE(t, resp)
	assert.Equal(t, fakePrewarmTicket, capture.Ticket, "预热票仍按官方契约同回合注入")
	assert.True(t, capture.PrewarmTicket)

	// 但预热票不入库：任何键上都查不到。
	_, ok := store.Replay(turnstate.Key(71, ""))
	assert.False(t, ok, "无线程标识的票不得入库")
	assert.Equal(t, 0, store.Len())

	// 第二个无线程头请求（连接复用、无新预热）：不得回带第一回合的票。
	resp2, capture2, err := bridge.Request(ctx, cfg, bridgeSpec(server.URL, ""), body)
	require.NoError(t, err)
	_ = readAllSSE(t, resp2)
	assert.Empty(t, capture2.Ticket, "连接复用时无线程请求不得回带他回合的票")
	assert.False(t, capture2.PrewarmTicket)

	frames := recorded()
	require.Len(t, frames, 3, "1 预热 + 2 业务")
	metadata, _ := frames[2]["client_metadata"].(map[string]any)
	_, hasTicket := metadata["x-codex-turn-state"]
	assert.False(t, hasTicket, "第二个无线程业务帧不得带任何票")

	assert.Equal(t, uint64(0), counters.TicketsCaptured.Load(), "无线程票不入库不计捕获")
	assert.Equal(t, uint64(1), counters.TicketsReplayed.Load(), "仅首回合的预热票同回合注入计一次回带")
}

func TestBridgeHandshakeFailureReturnsError(t *testing.T) {
	bridge, _, _ := newTestBridge(t)
	// 普通 HTTP 服务器不接受 WS 升级。
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{}"))
	}))
	defer plain.Close()

	cfg := pluginconfig.Default()
	cfg.WSBridgeEnabled = true
	_, _, err := bridge.Request(context.Background(), cfg, bridgeSpec(plain.URL, "thread-x"), []byte(`{"model":"m"}`))
	require.Error(t, err, "握手失败必须返回错误供上层回退 HTTP")
}

func TestBuildCreateFrame(t *testing.T) {
	frame, model, err := BuildCreateFrame([]byte(`{"model":"gpt-6-astra","background":true,"input":[1]}`), "tk")
	require.NoError(t, err)
	assert.Equal(t, "gpt-6-astra", model)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(frame, &payload))
	assert.Equal(t, "response.create", payload["type"])
	assert.Equal(t, true, payload["stream"])
	assert.Equal(t, false, payload["store"])
	assert.NotContains(t, payload, "background")
	metadata, _ := payload["client_metadata"].(map[string]any)
	assert.Equal(t, "tk", metadata["x-codex-turn-state"])

	_, _, err = BuildCreateFrame([]byte(`not-json`), "")
	require.Error(t, err)
}

func TestHandshakeHeaderFiltersAndSetsBeta(t *testing.T) {
	src := http.Header{}
	src.Set("Content-Type", "application/json")
	src.Set("Accept", "text/event-stream")
	src.Set("OpenAI-Beta", "responses=experimental")
	src.Set("Authorization", "Bearer x")
	src.Set("x-codex-turn-state", "host-injected-ticket")
	src.Set("session-id", "s1")
	src.Set("Host", "chatgpt.com")
	out := handshakeHeader(src)
	assert.Equal(t, openAIBetaWebsockets, out.Get("OpenAI-Beta"))
	assert.Empty(t, out.Get("Content-Type"))
	assert.Empty(t, out.Get("Accept"))
	assert.Empty(t, out.Get("Host"))
	assert.Equal(t, "Bearer x", out.Get("Authorization"))
	assert.Equal(t, "s1", out.Get("session-id"))
	// W3：官方 WS 握手从不带票（client.rs:1425 turn_state=None），宿主注入的
	// 逐跳票必须剥离，票只走帧级 client_metadata 回带。
	assert.Empty(t, out.Get("x-codex-turn-state"), "WS 握手不得携带票")
}

func TestToWSURL(t *testing.T) {
	cases := map[string]string{
		"https://chatgpt.com/backend-api/codex/responses": "wss://chatgpt.com/backend-api/codex/responses",
		"http://127.0.0.1:1/x/responses":                  "ws://127.0.0.1:1/x/responses",
		"wss://a/b":                                       "wss://a/b",
	}
	for in, want := range cases {
		got, err := toWSURL(in)
		require.NoError(t, err, in)
		assert.Equal(t, want, got, in)
	}
	_, err := toWSURL("ftp://x")
	require.Error(t, err)
}

// 票管理必须无条件处理：有票覆盖、无票剥离——下游携带的外来票（跨账号/跨线程
// 共享，协议上不允许）不得在无票时原样穿透到上游。
func TestBuildCreateFrameForeignTicketHandling(t *testing.T) {
	const foreign = `{"model":"m","client_metadata":{"x-codex-turn-state":"foreign-ticket","request_id":"r-1"}}`

	t.Run("有票时覆盖外来票", func(t *testing.T) {
		frame, _, err := BuildCreateFrame([]byte(foreign), "own-ticket")
		require.NoError(t, err)
		var payload map[string]any
		require.NoError(t, json.Unmarshal(frame, &payload))
		metadata := payload["client_metadata"].(map[string]any)
		assert.Equal(t, "own-ticket", metadata[clientMetadataTicket])
		assert.Equal(t, "r-1", metadata["request_id"], "非票键保留")
	})

	t.Run("无票时剥离外来票并保留其余元数据", func(t *testing.T) {
		frame, _, err := BuildCreateFrame([]byte(foreign), "")
		require.NoError(t, err)
		var payload map[string]any
		require.NoError(t, json.Unmarshal(frame, &payload))
		metadata := payload["client_metadata"].(map[string]any)
		assert.NotContains(t, metadata, clientMetadataTicket)
		assert.Equal(t, "r-1", metadata["request_id"])
	})

	t.Run("无票且仅剩外来票时整个 client_metadata 移除", func(t *testing.T) {
		frame, _, err := BuildCreateFrame([]byte(`{"model":"m","client_metadata":{"x-codex-turn-state":"foreign-ticket"}}`), "")
		require.NoError(t, err)
		var payload map[string]any
		require.NoError(t, json.Unmarshal(frame, &payload))
		assert.NotContains(t, payload, clientMetadataField)
	})

	t.Run("无票且无元数据时不得凭空生成 client_metadata", func(t *testing.T) {
		frame, _, err := BuildCreateFrame([]byte(`{"model":"m"}`), "")
		require.NoError(t, err)
		var payload map[string]any
		require.NoError(t, json.Unmarshal(frame, &payload))
		assert.NotContains(t, payload, clientMetadataField)
	})
}

func TestStripClientMetadataTicket(t *testing.T) {
	t.Run("剥离外来票并保留其余键", func(t *testing.T) {
		cleaned := StripClientMetadataTicket([]byte(`{"model":"m","client_metadata":{"x-codex-turn-state":"t1","request_id":"r-1"}}`))
		var payload map[string]any
		require.NoError(t, json.Unmarshal(cleaned, &payload))
		metadata := payload["client_metadata"].(map[string]any)
		assert.NotContains(t, metadata, clientMetadataTicket)
		assert.Equal(t, "r-1", metadata["request_id"])
	})
	t.Run("无票时原样返回", func(t *testing.T) {
		body := []byte(`{"model":"m","client_metadata":{"request_id":"r-1"}}`)
		assert.Equal(t, string(body), string(StripClientMetadataTicket(body)))
	})
	t.Run("非 JSON 体原样返回", func(t *testing.T) {
		body := []byte("not-json")
		assert.Equal(t, string(body), string(StripClientMetadataTicket(body)))
	})
}

// idleWSServer 接受 WS 升级后保持沉默（连接池行为测试用）。
func idleWSServer(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)
	return server
}

func dialTestWS(t *testing.T, serverURL string) *websocket.Conn {
	t.Helper()
	wsURL, err := toWSURL(serverURL)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, wsURL, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ws.CloseNow() })
	return ws
}

// W2：已关闭连接（markDead 等价 IsClosed）归还不入池，acquire 也不再取用，
// 防止陈旧连接写帧失败走 ErrRequestSent 硬失败。
func TestConnPoolClosedConnNeverReused(t *testing.T) {
	server := idleWSServer(t)

	t.Run("归还前已判死：release 关连接不入池", func(t *testing.T) {
		pool := &connPool{limit: 4}
		conn := &pooledConn{ws: dialTestWS(t, server.URL)}
		conn.markDead()
		_ = conn.ws.CloseNow()
		pool.release(conn)
		assert.Nil(t, pool.acquire(time.Minute))
		assert.Empty(t, pool.idle, "已关闭连接归还后不得留在池内")
	})

	t.Run("池内判死：acquire 跳过并弃用", func(t *testing.T) {
		pool := &connPool{limit: 4}
		conn := &pooledConn{ws: dialTestWS(t, server.URL)}
		pool.release(conn)
		conn.markDead()
		assert.Nil(t, pool.acquire(time.Minute), "已关闭连接必须被 acquire 跳过，不得交给调用方")
		assert.True(t, conn.isDead())
	})
}

// W2：lastUsed 超过空闲 TTL 的连接弃用（关连接不入池），未超龄连接正常复用。
func TestConnPoolAcquireEvictsIdleConn(t *testing.T) {
	server := idleWSServer(t)
	pool := &connPool{limit: 4}

	stale := &pooledConn{ws: dialTestWS(t, server.URL)}
	pool.release(stale)
	// 注：回拨量必须大于 TTL 且避免 Add 负向剥离单调钟的边界（>120s 才稳定触发弃用）。
	stale.lastUsed = time.Now().Add(-2*time.Minute - time.Second)
	assert.Nil(t, pool.acquire(120*time.Second), "超龄空闲连接必须弃用")
	assert.True(t, stale.isDead(), "弃用连接必须判死并关闭")

	fresh := &pooledConn{ws: dialTestWS(t, server.URL)}
	pool.release(fresh)
	assert.Same(t, fresh, pool.acquire(120*time.Second), "未超龄连接正常复用")
}

// W2：pool.limit 的读写统一在 p.mu——poolFor 热更 limit 后 release/acquire 可见。
func TestConnPoolSetLimitVisible(t *testing.T) {
	server := idleWSServer(t)
	pool := &connPool{limit: 1}
	first := &pooledConn{ws: dialTestWS(t, server.URL)}
	second := &pooledConn{ws: dialTestWS(t, server.URL)}
	pool.release(first)
	pool.release(second) // 池满：必须关闭不入池
	assert.True(t, second.isDead())

	pool.setLimit(4)
	third := &pooledConn{ws: dialTestWS(t, server.URL)}
	pool.release(third)
	assert.Same(t, third, pool.acquire(time.Minute), "热更 limit 后新连接可入池")
}

// W6：retired 池的 release 必须关连接不入池（在飞 streamEvents 归还旧池不再泄漏 FD）。
func TestRetiredPoolReleaseClosesConn(t *testing.T) {
	server := idleWSServer(t)
	pool := &connPool{limit: 4}
	conn := &pooledConn{ws: dialTestWS(t, server.URL)}
	pool.retire()
	assert.True(t, pool.retired.Load())
	pool.release(conn)
	assert.True(t, conn.isDead(), "退役池归还的连接必须关闭")
	assert.Nil(t, pool.acquire(time.Minute))
	assert.Empty(t, pool.idle)
}

// W6：CloseIdleConnections 换池后桥必须能重建连接继续服务。
func TestBridgeCloseIdleConnectionsThenRequest(t *testing.T) {
	bridge, _, _ := newTestBridge(t)
	server, _ := fakeWSUpstream(t)
	cfg := pluginconfig.Default()
	ctx := context.Background()
	body := []byte(`{"model":"m"}`)

	resp, _, err := bridge.Request(ctx, cfg, bridgeSpec(server.URL, "thread-cic"), body)
	require.NoError(t, err)
	_ = readAllSSE(t, resp)

	bridge.CloseIdleConnections()

	resp2, _, err := bridge.Request(ctx, cfg, bridgeSpec(server.URL, "thread-cic"), body)
	require.NoError(t, err, "换池后必须能重建连接继续服务")
	_ = readAllSSE(t, resp2)
}

// bigDeltaWSUpstream 对预热帧回 completed；对业务帧回 created → 超大 delta → completed。
func bigDeltaWSUpstream(t *testing.T, deltaLen int) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		ctx := r.Context()
		writeEvent := func(event map[string]any) bool {
			payload, err := json.Marshal(event)
			if err != nil {
				return false
			}
			writer, err := conn.Writer(ctx, websocket.MessageText)
			if err != nil {
				return false
			}
			if _, err := writer.Write(payload); err != nil {
				writer.Close()
				return false
			}
			return writer.Close() == nil
		}
		servedBig := false
		for {
			_, reader, err := conn.Reader(ctx)
			if err != nil {
				return
			}
			data, err := io.ReadAll(reader)
			if err != nil {
				return
			}
			var frame map[string]any
			if err := json.Unmarshal(data, &frame); err != nil {
				continue
			}
			if generate, ok := frame["generate"]; ok && generate == false {
				if !writeEvent(map[string]any{"type": "response.completed", "response": map[string]any{"id": "warm-1"}}) {
					return
				}
				continue
			}
			if servedBig {
				return
			}
			servedBig = true
			if !writeEvent(map[string]any{"type": "response.created", "response": map[string]any{"id": "resp-1"}}) ||
				!writeEvent(map[string]any{"type": "response.output_text.delta", "delta": strings.Repeat("x", deltaLen)}) ||
				!writeEvent(map[string]any{"type": "response.completed", "response": map[string]any{"id": "resp-1"}}) {
				return
			}
		}
	}))
	t.Cleanup(server.Close)
	return server
}

// W1：读上限配置生效——超过 coder/websocket 默认 32KiB 的单条消息必须完整透传；
// 低于消息大小的读上限必须使流以错误收尾（不得截断成正常 EOF）。
func TestBridgeWSReadLimit(t *testing.T) {
	const deltaLen = 40000

	t.Run("默认16MiB上限放行长消息", func(t *testing.T) {
		bridge, _, _ := newTestBridge(t)
		server := bigDeltaWSUpstream(t, deltaLen)
		cfg := pluginconfig.Default()
		require.EqualValues(t, 16<<20, cfg.WSReadLimitBytes)
		resp, _, err := bridge.Request(context.Background(), cfg, bridgeSpec(server.URL, "thread-readlimit"), []byte(`{"model":"m"}`))
		require.NoError(t, err)
		sse := readAllSSE(t, resp)
		assert.Contains(t, sse, `"type":"response.output_text.delta"`)
		assert.Contains(t, sse, strings.Repeat("x", deltaLen), "超 32KiB 的单条消息必须完整透传")
	})

	t.Run("低于消息大小的读上限使流报错", func(t *testing.T) {
		bridge, counters, _ := newTestBridge(t)
		server := bigDeltaWSUpstream(t, deltaLen)
		cfg := pluginconfig.Default()
		cfg.WSPrewarmEnabled = false
		cfg.WSReadLimitBytes = 1024
		resp, _, err := bridge.Request(context.Background(), cfg, bridgeSpec(server.URL, "thread-readlimit-low"), []byte(`{"model":"m"}`))
		require.NoError(t, err)
		defer resp.Body.Close()
		_, err = io.ReadAll(resp.Body)
		require.Error(t, err, "超读上限必须使流报错")
		assert.Contains(t, err.Error(), "读事件")
		assert.NotZero(t, counters.WSErrors.Load())
	})
}

// W4：流中 response.metadata 的票只写 Live（并发安全）；capture 按值返回后不得被
// goroutine 直写（string 双字撕裂），保持 Request 返回时刻的快照。
func TestBridgeMidStreamTicketGoesToLiveFields(t *testing.T) {
	bridge, counters, store := newTestBridge(t)
	const midStreamTicket = "gAAAAA mid-stream-ticket"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		ctx := r.Context()
		writeEvent := func(event map[string]any) bool {
			payload, err := json.Marshal(event)
			if err != nil {
				return false
			}
			writer, err := conn.Writer(ctx, websocket.MessageText)
			if err != nil {
				return false
			}
			if _, err := writer.Write(payload); err != nil {
				writer.Close()
				return false
			}
			return writer.Close() == nil
		}
		for {
			_, reader, err := conn.Reader(ctx)
			if err != nil {
				return
			}
			data, err := io.ReadAll(reader)
			if err != nil {
				return
			}
			var frame map[string]any
			if err := json.Unmarshal(data, &frame); err != nil {
				continue
			}
			if generate, ok := frame["generate"]; ok && generate == false {
				if !writeEvent(map[string]any{
					"type":    "response.metadata",
					"headers": map[string]any{"x-codex-turn-state": fakePrewarmTicket},
				}) || !writeEvent(map[string]any{"type": "response.completed", "response": map[string]any{"id": "warm-1"}}) {
					return
				}
				continue
			}
			// 业务流中补发 response.metadata（官方逐 turn 铸票通道）。
			if !writeEvent(map[string]any{
				"type":    "response.metadata",
				"headers": map[string]any{"x-codex-turn-state": midStreamTicket},
			}) || !writeEvent(map[string]any{"type": "response.created", "response": map[string]any{"id": "resp-1"}}) ||
				!writeEvent(map[string]any{"type": "response.completed", "response": map[string]any{"id": "resp-1"}}) {
				return
			}
		}
	}))
	t.Cleanup(server.Close)
	cfg := pluginconfig.Default()
	resp, capture, err := bridge.Request(context.Background(), cfg, bridgeSpec(server.URL, "thread-live"), []byte(`{"model":"m"}`))
	require.NoError(t, err)
	sse := readAllSSE(t, resp)

	assert.NotContains(t, sse, "response.metadata", "控制事件不得下发到 SSE")
	done, _, liveTicket := capture.Live.Snapshot()
	require.True(t, done)
	assert.Equal(t, midStreamTicket, liveTicket, "流中票必须进 Live")
	assert.Equal(t, fakePrewarmTicket, capture.Ticket, "capture 保持 Request 返回时刻的快照，不被流中直写")
	ticket, ok := store.Replay(turnstate.Key(71, "thread-live"))
	require.True(t, ok)
	assert.Equal(t, midStreamTicket, ticket, "流中票必须入存储")
	assert.Equal(t, uint64(2), counters.TicketsCaptured.Load(), "预热+流中各捕获一票")
}

// W5：预热帧剥离 previous_response_id（官方预热 continuation=None），业务帧保留链锚。
func TestBridgePrewarmDropsPreviousResponseID(t *testing.T) {
	bridge, _, _ := newTestBridge(t)
	server, recorded := fakeWSUpstream(t)
	cfg := pluginconfig.Default()
	body := []byte(`{"model":"m","previous_response_id":"resp-old","input":[]}`)
	_, _, err := bridge.Request(context.Background(), cfg, bridgeSpec(server.URL, "thread-prev"), body)
	require.NoError(t, err)

	frames := recorded()
	require.NotEmpty(t, frames)
	prewarmFrame := frames[0]
	assert.Equal(t, false, prewarmFrame["generate"])
	assert.NotContains(t, prewarmFrame, "previous_response_id", "预热帧不得带链锚")
	require.Eventually(t, func() bool { return len(recorded()) >= 2 }, 2*time.Second, 10*time.Millisecond)
	assert.Equal(t, "resp-old", recorded()[1]["previous_response_id"], "业务帧保留链锚")
}

// W7：事件空闲超时必须以错误收尾（CloseWithError 携带原因），不得伪装成正常 EOF。
func TestBridgeIdleTimeoutSurfacesInterruption(t *testing.T) {
	bridge, _, _ := newTestBridge(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		ctx := r.Context()
		for {
			_, reader, err := conn.Reader(ctx)
			if err != nil {
				return
			}
			if _, err := io.ReadAll(reader); err != nil {
				return
			}
			// 读到一帧（业务帧）后保持沉默：制造事件空闲超时。
			writer, err := conn.Writer(ctx, websocket.MessageText)
			if err != nil {
				return
			}
			payload, err := json.Marshal(map[string]any{"type": "response.created", "response": map[string]any{"id": "r1"}})
			if err != nil {
				return
			}
			if _, err := writer.Write(payload); err != nil {
				return
			}
			if err := writer.Close(); err != nil {
				return
			}
		}
	}))
	t.Cleanup(server.Close)
	cfg := pluginconfig.Default()
	cfg.WSPrewarmEnabled = false
	cfg.WSEventIdleTimeoutSeconds = 1
	resp, _, err := bridge.Request(context.Background(), cfg, bridgeSpec(server.URL, "thread-idle"), []byte(`{"model":"m"}`))
	require.NoError(t, err)
	defer resp.Body.Close()
	_, err = io.ReadAll(resp.Body)
	require.Error(t, err, "空闲超时必须以错误收尾，不得伪装成完整响应")
	assert.Contains(t, err.Error(), "空闲超时")
}

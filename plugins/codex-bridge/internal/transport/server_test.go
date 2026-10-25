package transport

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/MACOS-DO/sub4api-plugins/codex-bridge/internal/metrics"
	"github.com/MACOS-DO/sub4api-plugins/codex-bridge/internal/pluginconfig"
	"github.com/MACOS-DO/sub4api-plugins/codex-bridge/internal/turnstate"
	pluginv1 "github.com/MACOS-DO/sub4api/pkg/pluginapi/v1"
)

func newTestForwarder(t *testing.T) (*Forwarder, *metrics.Counters) {
	t.Helper()
	counters := metrics.New()
	store := turnstate.NewStore(time.Duration(pluginconfig.Default().TurnStateTTLSeconds) * time.Second)
	recorder := metrics.NewRecorder(counters)
	return NewForwarder(pluginconfig.Default(), store, recorder, counters), counters
}

type recordedRequest struct {
	header http.Header
	body   string
}

// upstreamFixture 返回一个记录请求并按脚本回复的测试上游。
func upstreamFixture(t *testing.T, respond func(index int, r recordedRequest) (int, http.Header, string)) (*httptest.Server, *[]recordedRequest) {
	t.Helper()
	var mu sync.Mutex
	recorded := make([]recordedRequest, 0)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		req.Body.Close()
		mu.Lock()
		recorded = append(recorded, recordedRequest{header: req.Header.Clone(), body: string(body)})
		index := len(recorded) - 1
		mu.Unlock()
		status, header, payload := respond(index, recordedRequest{header: req.Header, body: string(body)})
		for name, values := range header {
			for _, value := range values {
				w.Header().Add(name, value)
			}
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(payload))
	}))
	t.Cleanup(server.Close)
	return server, &recorded
}

func outbound(accountID int64, url string, header http.Header, body string) Outbound {
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	return Outbound{
		Method:        http.MethodPost,
		URL:           url,
		Header:        header,
		Body:          reader,
		ContentLength: int64(len(body)),
		AccountID:     accountID,
	}
}

func TestForwarderTicketCaptureAndReplay(t *testing.T) {
	const ticket = "gAAAABticket-value-780chars"
	forwarder, counters := newTestForwarder(t)
	server, recorded := upstreamFixture(t, func(index int, r recordedRequest) (int, http.Header, string) {
		header := http.Header{}
		if index == 0 {
			header.Set(headerTurnState, ticket)
			header.Set(headerOpenAIModel, "gpt-6-astra")
		}
		return http.StatusOK, header, `data: {"type":"response.completed"}` + "\n\n"
	})

	header := http.Header{}
	header.Set(headerClientRequestID, "thread-1")
	header.Set("Content-Type", "application/json")

	// 第一个请求：无票可回带，上游铸票。
	resp, _, _, err := forwarder.Do(context.Background(), outbound(11, server.URL, header.Clone(), `{"model":"gpt-6-astra"}`))
	require.NoError(t, err)
	resp.Body.Close()

	// 第二个请求（同账号同线程）：必须回带捕获的票。
	resp, _, _, err = forwarder.Do(context.Background(), outbound(11, server.URL, header.Clone(), `{"model":"gpt-6-astra","input":[2]}`))
	require.NoError(t, err)
	resp.Body.Close()

	requests := *recorded
	require.Len(t, requests, 2)
	assert.Empty(t, requests[0].header.Get(headerTurnState), "首请求不应带票")
	assert.Equal(t, ticket, requests[1].header.Get(headerTurnState), "同线程续请求应回带票")
	assert.Equal(t, uint64(1), counters.TicketsCaptured.Load())
	assert.Equal(t, uint64(1), counters.TicketsReplayed.Load())
}

func TestForwarderStripsGatewayInjectedTicket(t *testing.T) {
	forwarder, counters := newTestForwarder(t)
	server, recorded := upstreamFixture(t, func(int, recordedRequest) (int, http.Header, string) {
		return http.StatusOK, nil, "{}"
	})

	header := http.Header{}
	header.Set(headerClientRequestID, "thread-9")
	header.Set(headerTurnState, "shared-ticket-from-gateway-store") // 跨账号共享票，必须剥离
	resp, _, _, err := forwarder.Do(context.Background(), outbound(11, server.URL, header, `{"model":"gpt-6-astra"}`))
	require.NoError(t, err)
	resp.Body.Close()

	requests := *recorded
	require.Len(t, requests, 1)
	assert.Empty(t, requests[0].header.Get(headerTurnState), "宿主注入的共享票必须被剥离")
	assert.Equal(t, uint64(0), counters.TicketsReplayed.Load())
}

func TestForwarderTicketsAreAccountScoped(t *testing.T) {
	forwarder, _ := newTestForwarder(t)
	server, recorded := upstreamFixture(t, func(index int, _ recordedRequest) (int, http.Header, string) {
		header := http.Header{}
		if index == 0 {
			header.Set(headerTurnState, "ticket-for-account-11")
		}
		return http.StatusOK, header, "{}"
	})

	header := http.Header{}
	header.Set(headerClientRequestID, "thread-shared")
	resp, _, _, err := forwarder.Do(context.Background(), outbound(11, server.URL, header.Clone(), "{}"))
	require.NoError(t, err)
	resp.Body.Close()

	// 账号 12：同线程也不得拿到账号 11 的票。
	resp, _, _, err = forwarder.Do(context.Background(), outbound(12, server.URL, header.Clone(), "{}"))
	require.NoError(t, err)
	resp.Body.Close()

	requests := *recorded
	require.Len(t, requests, 2)
	assert.Empty(t, requests[1].header.Get(headerTurnState))
}

func TestForwarderDowngradeObservation(t *testing.T) {
	forwarder, counters := newTestForwarder(t)
	server, _ := upstreamFixture(t, func(_ int, _ recordedRequest) (int, http.Header, string) {
		header := http.Header{}
		header.Set(headerSafetyBuffering, "true")
		header.Set(headerSafetyBufferingModel, "gpt-6-luna")
		header.Set(headerOpenAIModel, "gpt-6-luna") // 请求 gpt-6-astra，实际服务 luna
		return http.StatusOK, header, "{}"
	})

	resp, capture, _, err := forwarder.Do(context.Background(), outbound(21, server.URL, http.Header{}, `{"model":"gpt-6-astra","instructions":"x"}`))
	require.NoError(t, err)
	resp.Body.Close()

	assert.True(t, capture.SafetyBuffering)
	assert.Equal(t, "gpt-6-luna", capture.ServedModel)
	assert.Equal(t, "gpt-6-luna", capture.FasterModel)
	assert.Equal(t, uint64(1), counters.SafetyBufferingResponses.Load())
	assert.Equal(t, uint64(1), counters.ModelDowngrades.Load())
	snapshot := string(forwarder.recorder.Snapshot())
	assert.Contains(t, snapshot, `"requested_model":"gpt-6-astra"`)
	assert.Contains(t, snapshot, `"served_model":"gpt-6-luna"`)
}

func TestForwarderCookieJarAllowlist(t *testing.T) {
	forwarder, _ := newTestForwarder(t)
	server, recorded := upstreamFixture(t, func(_ int, _ recordedRequest) (int, http.Header, string) {
		header := http.Header{}
		header.Add("Set-Cookie", "__cf_bm=abc; Path=/")
		header.Add("Set-Cookie", "session=secret-session-state; Path=/")
		return http.StatusOK, header, "{}"
	})

	for i := 0; i < 2; i++ {
		resp, _, _, err := forwarder.Do(context.Background(), outbound(31, server.URL, http.Header{}, "{}"))
		require.NoError(t, err)
		resp.Body.Close()
	}

	requests := *recorded
	require.Len(t, requests, 2)
	cookie2 := requests[1].header.Get("Cookie")
	assert.Contains(t, cookie2, "__cf_bm=abc", "基础设施 cookie 应回带")
	assert.NotContains(t, cookie2, "session=", "认证态 cookie 不得持久化")
}

func TestForwarderInvalidProxyFailsClosed(t *testing.T) {
	forwarder, _ := newTestForwarder(t)
	server, _ := upstreamFixture(t, func(int, recordedRequest) (int, http.Header, string) {
		return http.StatusOK, nil, "{}"
	})

	spec := outbound(41, server.URL, http.Header{}, "{}")
	spec.ProxyURL = "://not-a-proxy"
	_, _, requestSent, err := forwarder.Do(context.Background(), spec)
	require.Error(t, err)
	assert.False(t, requestSent, "无效代理应在触达上游前失败")
}

func TestForwarderRedirectPassthrough(t *testing.T) {
	forwarder, _ := newTestForwarder(t)
	server, _ := upstreamFixture(t, func(int, recordedRequest) (int, http.Header, string) {
		header := http.Header{}
		header.Set("Location", "https://example.invalid/moved")
		return http.StatusFound, header, ""
	})

	resp, _, _, err := forwarder.Do(context.Background(), outbound(51, server.URL, http.Header{}, "{}"))
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusFound, resp.StatusCode, "3xx 必须原样透传")
}

func TestSniffingReaderExtractsModel(t *testing.T) {
	var model string
	payload := `{"stream":true,"model":"gpt-6-astra","input":[]}`
	reader := &sniffingReader{r: strings.NewReader(payload), max: 256, modelOut: &model}
	out, err := io.ReadAll(reader)
	require.NoError(t, err)
	assert.Equal(t, payload, string(out), "嗅探不得改写字节流")
	assert.Equal(t, "gpt-6-astra", model)
}

func TestForwarderWSBridgeFallsBackToHTTP(t *testing.T) {
	forwarder, counters := newTestForwarder(t)
	cfg := pluginconfig.Default()
	cfg.WSBridgeEnabled = true
	require.NoError(t, forwarder.ApplyConfig(cfg))

	// 普通 HTTP 上游：WS 握手必然失败 → 必须回退 HTTP 透传。
	server, recorded := upstreamFixture(t, func(int, recordedRequest) (int, http.Header, string) {
		header := http.Header{}
		header.Set(headerTurnState, "gAAAAAhttp-ticket")
		return http.StatusOK, header, `data: {"type":"response.completed"}` + "\n\n"
	})

	resp, capture, _, err := forwarder.Do(context.Background(), outbound(61, server.URL+"/backend-api/codex/responses", http.Header{}, `{"model":"gpt-6-astra"}`))
	require.NoError(t, err)
	resp.Body.Close()

	requests := *recorded
	// 第一条是失败的 WS 升级握手（无 body、服务器未处理业务），第二条才是回退 POST。
	var posts []recordedRequest
	for _, r := range requests {
		if r.body != "" {
			posts = append(posts, r)
		}
	}
	require.Len(t, posts, 1, "回退后 HTTP 必须恰好发一次真实 POST")
	assert.Contains(t, posts[0].body, "gpt-6-astra")
	assert.Equal(t, uint64(1), counters.WSFallbackHTTP.Load())
	assert.Equal(t, uint64(0), counters.WSBridgedRequests.Load())
	assert.Equal(t, "gAAAAAhttp-ticket", capture.Ticket, "回退路径的 HTTP 票捕获照常工作")
}

func TestForwarderCookieJarAccountIsolation(t *testing.T) {
	forwarder, _ := newTestForwarder(t)
	server, recorded := upstreamFixture(t, func(int, recordedRequest) (int, http.Header, string) {
		header := http.Header{}
		header.Add("Set-Cookie", "__cf_bm=per-visit; Path=/")
		return http.StatusOK, header, "{}"
	})

	// 账号 81 首访：拿 cookie；同代理的账号 82 再访：不得带上账号 81 的 cookie。
	for _, accountID := range []int64{81, 82} {
		for i := 0; i < 2; i++ {
			resp, _, _, err := forwarder.Do(context.Background(), outbound(accountID, server.URL, http.Header{}, "{}"))
			require.NoError(t, err)
			resp.Body.Close()
		}
	}

	requests := *recorded
	require.Len(t, requests, 4)
	// 账号 81 第 2 次请求：带自己的 cookie（jar 生效）。
	assert.Contains(t, requests[1].header.Get("Cookie"), "__cf_bm=per-visit", "同账号应回带自己的 cookie")
	// 账号 82 第 1 次请求（同代理同上游）：必须干净（jar 按账号隔离）。
	assert.NotContains(t, requests[2].header.Get("Cookie"), "__cf_bm", "同代理的其他账号不得继承 cookie")
}

func TestThreadKeyFallbacks(t *testing.T) {
	header := func(pairs ...string) http.Header {
		h := http.Header{}
		for i := 0; i+1 < len(pairs); i += 2 {
			h.Set(pairs[i], pairs[i+1])
		}
		return h
	}
	tests := []struct {
		name   string
		header http.Header
		want   string
	}{
		{"官方线程头优先", header("x-client-request-id", "thread-a", "session-id", "hyphen-s", "session_id", "underscore-s"), "thread-a"},
		{"连字符 session-id 次之", header("session-id", "hyphen-s", "session_id", "underscore-s"), "hyphen-s"},
		{"宿主下划线 session_id 第三回退", header("session_id", "underscore-s"), "underscore-s"},
		{"连字符与下划线互不命中", header("session-id", "hyphen-s"), "hyphen-s"},
		// 修复前注释为「皆缺失退化为账号级」：账号级键会把 A 会话的 turn-state
		// 注入 B 会话请求。修复后无线程标识返回空键，票簿记整体 fail-closed。
		{"皆缺失无线程标识（fail-closed）", http.Header{}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, threadKey(tt.header))
		})
	}
}

// 宿主出站写的是下划线 session_id（canonical 键 Session_Id）；修复前 threadKey
// 只认连字符形态，键必然 miss，票退化为账号级跨线程共享。
func TestForwarderTicketScopedByUnderscoreSessionID(t *testing.T) {
	forwarder, _ := newTestForwarder(t)
	server, recorded := upstreamFixture(t, func(_ int, r recordedRequest) (int, http.Header, string) {
		header := http.Header{}
		header.Set(headerTurnState, "ticket-for-"+r.header.Get(headerSessionIDUnderscore))
		return http.StatusOK, header, "{}"
	})

	threadA := http.Header{}
	threadA.Set(headerSessionIDUnderscore, "thread-A")
	threadB := http.Header{}
	threadB.Set(headerSessionIDUnderscore, "thread-B")

	// 线程 A 铸票 → 线程 B 不得继承 → 线程 A 续请求回带自己的票。
	for _, h := range []http.Header{threadA, threadB, threadA} {
		resp, _, _, err := forwarder.Do(context.Background(), outbound(11, server.URL, h, "{}"))
		require.NoError(t, err)
		resp.Body.Close()
	}

	requests := *recorded
	require.Len(t, requests, 3)
	// 票值编码了上游看到的线程标识（ticket-for-<session_id>），据此可断言键的
	// 归属：请求 2（线程 A 续）必须回带线程 A 自己的票，请求 1（线程 B）不得
	// 继承线程 A 的票。修复前 threadKey 对下划线键 miss，三请求共享账号级键，
	// 请求 1 会错误回带 ticket-for-thread-A。
	assert.Empty(t, requests[0].header.Get(headerTurnState), "首请求无票可回带")
	assert.Empty(t, requests[1].header.Get(headerTurnState), "不同线程不得拿到他线程的票")
	assert.Equal(t, "ticket-for-thread-A", requests[2].header.Get(headerTurnState), "同线程续请求应回带票")
}

// 无线程头客户端（OpenAI 兼容 API 调用方典型形态，不带 x-client-request-id）：
// 修复前 Key 退化为纯账号键，第一个无线程头会话捕获的票会被后续所有无线程头
// 会话回带——账号级跨线程共享，官方协议不允许。修复后 fail-closed：无线程头
// 不捕获、不回带；带线程头的请求不受影响。
func TestForwarderHeaderlessClientNeverReplaysTicket(t *testing.T) {
	forwarder, counters := newTestForwarder(t)
	server, recorded := upstreamFixture(t, func(index int, r recordedRequest) (int, http.Header, string) {
		header := http.Header{}
		switch {
		case index == 0:
			// 第一个无线程头会话：上游铸票（修复后不捕获——票无线程归属）。
			header.Set(headerTurnState, "gAAAAAticket-headerless-session-A")
		case index == 2:
			// 带线程头的首个请求照常铸票；其续请求（index 3）不铸，只能回带。
			header.Set(headerTurnState, "gAAAAAticket-for-thread-1")
		}
		return http.StatusOK, header, "{}"
	})

	// 第一个无线程头会话：上游铸票，修复后不捕获。
	resp, _, _, err := forwarder.Do(context.Background(), outbound(11, server.URL, http.Header{}, "{}"))
	require.NoError(t, err)
	resp.Body.Close()
	// 第二个无线程头会话（同账号）：不得回带第一个会话的票。
	resp, _, _, err = forwarder.Do(context.Background(), outbound(11, server.URL, http.Header{}, "{}"))
	require.NoError(t, err)
	resp.Body.Close()
	// 线程头请求仍正常捕获与回带。
	threaded := http.Header{}
	threaded.Set(headerClientRequestID, "thread-1")
	resp, _, _, err = forwarder.Do(context.Background(), outbound(11, server.URL, threaded.Clone(), "{}"))
	require.NoError(t, err)
	resp.Body.Close()
	resp, _, _, err = forwarder.Do(context.Background(), outbound(11, server.URL, threaded.Clone(), "{}"))
	require.NoError(t, err)
	resp.Body.Close()

	requests := *recorded
	require.Len(t, requests, 4)
	assert.Empty(t, requests[0].header.Get(headerTurnState), "首请求无票可回带")
	assert.Empty(t, requests[1].header.Get(headerTurnState), "无线程头会话不得回带其他会话的票")
	assert.Equal(t, "gAAAAAticket-for-thread-1", requests[3].header.Get(headerTurnState), "带线程头的续请求回带不受影响")
	assert.Equal(t, uint64(1), counters.TicketsCaptured.Load(), "无线程头的票不捕获，只计带线程头的一次")
}

// WS 桥候选路径 body 已缓冲：client_metadata 里的外来票（官方 HTTP 通道回带走
// 请求头，body 内票属异常形状）必须在桥接与 HTTP 回退重放前剥离。
func TestForwarderWSFallbackStripsBodyTicket(t *testing.T) {
	forwarder, _ := newTestForwarder(t)
	cfg := pluginconfig.Default() // manage_turn_state=true
	cfg.WSBridgeEnabled = true
	require.NoError(t, forwarder.ApplyConfig(cfg))

	// 普通 HTTP 上游：WS 握手必然失败 → 回退 HTTP 重放缓冲体。
	server, recorded := upstreamFixture(t, func(int, recordedRequest) (int, http.Header, string) {
		return http.StatusOK, nil, `data: {"type":"response.completed"}` + "\n\n"
	})

	body := `{"model":"gpt-6-astra","client_metadata":{"x-codex-turn-state":"foreign-shared-ticket"}}`
	resp, _, _, err := forwarder.Do(context.Background(), outbound(91, server.URL+"/backend-api/codex/responses", http.Header{}, body))
	require.NoError(t, err)
	resp.Body.Close()

	var posts []recordedRequest
	for _, r := range *recorded {
		if r.body != "" {
			posts = append(posts, r)
		}
	}
	require.Len(t, posts, 1, "回退后 HTTP 必须恰好发一次真实 POST")
	assert.NotContains(t, posts[0].body, "foreign-shared-ticket", "body 外来票必须剥离")
	assert.NotContains(t, posts[0].body, "client_metadata", "仅剩外来票时整个键移除")
	assert.Contains(t, posts[0].body, "gpt-6-astra", "其余字段保持原样")
}

// fakeForwardStream 是 Forward 的最小 gRPC 服务端流替身：只实现 Forward 用到的
// Context/Send/Recv，其余方法由嵌入接口承接（不会被调用）。
type fakeForwardStream struct {
	pluginv1.TransportPlugin_ForwardServer
	ctx    context.Context
	frames chan *pluginv1.ForwardRequest
	closed chan struct{}

	mu   sync.Mutex
	sent []*pluginv1.ForwardResponse
}

func newFakeForwardStream() *fakeForwardStream {
	return &fakeForwardStream{
		ctx:    context.Background(),
		frames: make(chan *pluginv1.ForwardRequest, 8),
		closed: make(chan struct{}),
	}
}

func (f *fakeForwardStream) Context() context.Context { return f.ctx }

func (f *fakeForwardStream) Send(resp *pluginv1.ForwardResponse) error {
	f.mu.Lock()
	f.sent = append(f.sent, resp)
	f.mu.Unlock()
	return nil
}

func (f *fakeForwardStream) Recv() (*pluginv1.ForwardRequest, error) {
	select {
	case frame := <-f.frames:
		return frame, nil
	case <-f.closed:
		return nil, io.EOF
	}
}

func (f *fakeForwardStream) lastResponse() *pluginv1.ForwardResponse {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.sent) == 0 {
		return nil
	}
	return f.sent[len(f.sent)-1]
}

// Do 在传输接管 body 前失败（无效代理）时必须关闭 pipeReader：
// 否则 drain goroutine 永久阻塞在 pipe Write 上（goroutine 泄漏）。
func TestForwardDoFailureReleasesDrainGoroutine(t *testing.T) {
	server, err := NewServer("codex-bridge", "test", pluginconfig.Default(), metrics.New())
	require.NoError(t, err)

	stream := newFakeForwardStream()
	stream.frames <- &pluginv1.ForwardRequest{Frame: &pluginv1.ForwardRequest_Start{Start: &pluginv1.ForwardRequestStart{
		// 非 /responses 路径：不走 WS 桥，Do 在 clientFor 代理校验处失败。
		Method:   http.MethodPost,
		Url:      "https://chatgpt.com/backend-api/codex/other",
		Headers:  map[string]*pluginv1.HeaderValues{"Content-Type": {Values: []string{"application/json"}}},
		ProxyUrl: "://not-a-proxy",
		HasBody:  true,
	}}}
	stream.frames <- &pluginv1.ForwardRequest{Frame: &pluginv1.ForwardRequest_BodyChunk{BodyChunk: []byte(`{"x":1}`)}}

	require.NoError(t, server.Forward(stream))
	close(stream.closed)

	errFrame := stream.lastResponse().GetError()
	require.NotNil(t, errFrame, "Do 失败必须回传 error 帧")
	assert.Equal(t, "upstream", errFrame.Code)
	assert.False(t, errFrame.RequestSent, "代理校验失败发生在触达上游前")

	// 修复前：drain goroutine 卡死在 pipe Write，永不退出。
	// 不用 NumGoroutine 全局计数断言：testify Eventually 的条件在独立
	// goroutine 里求值（计数恒含自身 +1），只能靠无关 goroutine 的退出
	// 碰巧抵消，天然 flaky。改为直接等待本条 Forward 的 drain goroutine
	// 生命周期结束（drainWG），断言精确且确定。
	drainExited := make(chan struct{})
	go func() {
		server.drainWG.Wait()
		close(drainExited)
	}()
	select {
	case <-drainExited:
	case <-time.After(5 * time.Second):
		t.Fatal("drain goroutine 必须随 pipeReader 关闭而退出")
	}
}

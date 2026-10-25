// 进程级集成测试：通过 go-plugin 握手拉起真实插件二进制，
// 走完整 TransportPlugin 协议（GetInfo/ValidateConfig/ApplyConfig/Health/Forward），
// 上游用本地 httptest 验证契约对齐行为（铸票→回带、共享票剥离、流式透传）。
package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	hcplugin "github.com/hashicorp/go-plugin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pluginv1 "github.com/MACOS-DO/sub4api/pkg/pluginapi/v1"
)

func buildPluginBinary(t *testing.T) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "codex-bridge-test.exe")
	cmd := exec.Command("go", "build", "-o", out, "./cmd/codex-bridge")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	combined, err := cmd.CombinedOutput()
	require.NoError(t, err, "构建插件二进制: %s", string(combined))
	return out
}

func dispense(t *testing.T, binary string) *pluginv1.TransportClient {
	t.Helper()
	client := hcplugin.NewClient(&hcplugin.ClientConfig{
		HandshakeConfig:  pluginv1.HandshakeConfig,
		Plugins:          pluginv1.ClientPluginMap(),
		Cmd:              exec.Command(binary),
		AllowedProtocols: []hcplugin.Protocol{hcplugin.ProtocolGRPC},
		StartTimeout:     30 * time.Second,
	})
	t.Cleanup(func() { client.Kill() })
	protocol, err := client.Client()
	require.NoError(t, err)
	raw, err := protocol.Dispense(pluginv1.TransportPluginName)
	require.NoError(t, err)
	transport, ok := raw.(*pluginv1.TransportClient)
	require.True(t, ok, "Dispense 返回类型不符: %T", raw)
	return transport
}

func runForward(t *testing.T, ctx context.Context, client *pluginv1.TransportClient, url, thread, body string) (*pluginv1.ForwardResponseStart, []byte) {
	t.Helper()
	stream, err := client.Forward(ctx)
	require.NoError(t, err)
	require.NoError(t, stream.Send(&pluginv1.ForwardRequest{Frame: &pluginv1.ForwardRequest_Start{Start: &pluginv1.ForwardRequestStart{
		RequestId: "it-" + thread, Method: http.MethodPost, Url: url,
		Headers: map[string]*pluginv1.HeaderValues{
			"x-client-request-id": {Values: []string{thread}},
			"content-type":        {Values: []string{"application/json"}},
		},
		AccountId: 99, AccountConcurrency: 1,
		Platform: "openai", AccountType: "oauth",
		ContentLength: int64(len(body)), HasBody: body != "",
	}}}))
	if body != "" {
		require.NoError(t, stream.Send(&pluginv1.ForwardRequest{Frame: &pluginv1.ForwardRequest_BodyChunk{BodyChunk: []byte(body)}}))
	}
	require.NoError(t, stream.Send(&pluginv1.ForwardRequest{Frame: &pluginv1.ForwardRequest_BodyEnd{BodyEnd: true}}))
	require.NoError(t, stream.CloseSend())

	var start *pluginv1.ForwardResponseStart
	var payload bytes.Buffer
	for {
		frame, err := stream.Recv()
		require.NoError(t, err)
		switch typed := frame.Frame.(type) {
		case *pluginv1.ForwardResponse_Start:
			start = typed.Start
		case *pluginv1.ForwardResponse_BodyChunk:
			payload.Write(typed.BodyChunk)
		case *pluginv1.ForwardResponse_End:
			return start, payload.Bytes()
		case *pluginv1.ForwardResponse_Error:
			t.Fatalf("收到 error 帧: code=%s message=%s request_sent=%v", typed.Error.Code, typed.Error.Message, typed.Error.RequestSent)
		}
	}
}

func TestPluginProcessEndToEnd(t *testing.T) {
	binary := buildPluginBinary(t)
	client := dispense(t, binary)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	info, err := client.GetInfo(ctx, &pluginv1.GetInfoRequest{})
	require.NoError(t, err)
	assert.Equal(t, "sub4api.codex-bridge", info.PluginId)
	assert.Equal(t, []string{"openai.oauth.outbound_transport.v1"}, info.Capabilities)
	assert.EqualValues(t, pluginv1.ProtocolVersion, info.ProtocolVersion)
	assert.EqualValues(t, pluginv1.TransportAPIVersion, info.TransportApiVersion)

	validate, err := client.ValidateConfig(ctx, &pluginv1.ValidateConfigRequest{ConfigJson: []byte(`{"bogus":1}`)})
	require.NoError(t, err)
	assert.False(t, validate.Valid)

	applied, err := client.ApplyConfig(ctx, &pluginv1.ApplyConfigRequest{ConfigJson: []byte(`{}`)})
	require.NoError(t, err)
	require.True(t, applied.Applied, applied.Message)

	health, err := client.Health(ctx, &pluginv1.HealthRequest{})
	require.NoError(t, err)
	require.True(t, health.Healthy)
	var status map[string]any
	require.NoError(t, json.Unmarshal([]byte(health.StatusJson), &status))
	assert.Contains(t, status, "requests_total")

	// 上游夹具：首请求铸票 + 降智头；记录全部请求头。
	const ticket = "gAAAAAintegration-ticket"
	var mu sync.Mutex
	var seen []http.Header
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		mu.Lock()
		seen = append(seen, req.Header.Clone())
		index := len(seen) - 1
		mu.Unlock()
		if index == 0 {
			w.Header().Set("x-codex-turn-state", ticket)
			w.Header().Set("x-codex-safety-buffering-enabled", "true")
			w.Header().Set("x-codex-safety-buffering-faster-model", "gpt-6-luna")
			w.Header().Set("openai-model", "gpt-6-luna")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: {\"type\":\"response.created\"}\n\ndata: {\"type\":\"response.completed\"}\n\n"))
	}))
	defer upstream.Close()

	body := `{"model":"gpt-6-astra","stream":true}`
	start1, payload1 := runForward(t, ctx, client, upstream.URL, "thread-it-1", body)
	require.NotNil(t, start1)
	assert.Equal(t, int32(http.StatusOK), start1.StatusCode)
	assert.Equal(t, ticket, headerValue(start1.Headers, "X-Codex-Turn-State"), "上游铸的票必须原样透传给宿主")
	assert.Contains(t, string(payload1), "response.completed")

	start2, _ := runForward(t, ctx, client, upstream.URL, "thread-it-1", body+`,"input":[2]}`)
	require.NotNil(t, start2)
	assert.Equal(t, int32(http.StatusOK), start2.StatusCode)

	mu.Lock()
	requests := append([]http.Header(nil), seen...)
	mu.Unlock()
	require.Len(t, requests, 2)
	assert.Empty(t, requests[0].Get("X-Codex-Turn-State"), "首请求不应带票")
	assert.Equal(t, ticket, requests[1].Get("X-Codex-Turn-State"), "同线程续请求应回带票")

	health2, err := client.Health(ctx, &pluginv1.HealthRequest{})
	require.NoError(t, err)
	var status2 struct {
		TicketsCaptured          uint64 `json:"tickets_captured"`
		TicketsReplayed          uint64 `json:"tickets_replayed"`
		SafetyBufferingResponses uint64 `json:"safety_buffering_responses"`
		ModelDowngrades          uint64 `json:"model_downgrades"`
	}
	require.NoError(t, json.Unmarshal([]byte(health2.StatusJson), &status2))
	assert.Equal(t, uint64(1), status2.TicketsCaptured)
	assert.Equal(t, uint64(1), status2.TicketsReplayed)
	assert.Equal(t, uint64(1), status2.SafetyBufferingResponses)
	assert.Equal(t, uint64(1), status2.ModelDowngrades, "请求 astra 实际服务 luna 应记为降智")
}

func headerValue(headers map[string]*pluginv1.HeaderValues, name string) string {
	if values := headers[name]; values != nil && len(values.Values) > 0 {
		return values.Values[0]
	}
	return ""
}

// fakeWSUpstream 集成版假上游：预热铸票 + 业务事件回放，记录全部帧。
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
			data, _ := io.ReadAll(reader)
			var frame map[string]any
			if json.Unmarshal(data, &frame) != nil {
				continue
			}
			mu.Lock()
			frames = append(frames, frame)
			mu.Unlock()
			generate, hasGenerate := frame["generate"]
			model, _ := frame["model"].(string)
			if hasGenerate && generate == false {
				if !writeEvent(map[string]any{
					"type":    "response.metadata",
					"headers": map[string]any{"x-codex-turn-state": "gAAAAAit-prewarm"},
				}) {
					return
				}
				writeEvent(map[string]any{"type": "response.completed", "response": map[string]any{"id": "warm-1"}})
				continue
			}
			if !writeEvent(map[string]any{"type": "response.created", "response": map[string]any{"id": "r", "model": model}}) ||
				!writeEvent(map[string]any{"type": "response.output_text.delta", "delta": "hi"}) ||
				!writeEvent(map[string]any{"type": "response.completed", "response": map[string]any{"id": "r"}}) {
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

// TestPluginProcessWSBridge 验证 WS 桥全链路（真实插件进程 + 假 WS 上游）：
// 配置热应用开启 → 预热铸票 → 业务帧 client_metadata 回带 → SSE 回到宿主侧。
func TestPluginProcessWSBridge(t *testing.T) {
	binary := buildPluginBinary(t)
	client := dispense(t, binary)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	upstream, recorded := fakeWSUpstream(t)

	applied, err := client.ApplyConfig(ctx, &pluginv1.ApplyConfigRequest{ConfigJson: []byte(`{"ws_bridge_enabled": true}`)})
	require.NoError(t, err)
	require.True(t, applied.Applied, applied.Message)

	body := `{"model":"gpt-6-astra","stream":true,"input":[]}`
	start1, payload1 := runForward(t, ctx, client, upstream.URL+"/backend-api/codex/responses", "thread-ws-it", body)
	require.NotNil(t, start1)
	assert.Equal(t, int32(http.StatusOK), start1.StatusCode)
	assert.Equal(t, "text/event-stream", headerValue(start1.Headers, "Content-Type"))
	assert.Contains(t, string(payload1), `"type":"response.created"`)
	assert.Contains(t, string(payload1), `"type":"response.completed"`)
	assert.NotContains(t, string(payload1), "response.metadata")

	_, payload2 := runForward(t, ctx, client, upstream.URL+"/backend-api/codex/responses", "thread-ws-it", body)
	assert.Contains(t, string(payload2), `"type":"response.completed"`)

	frames := recorded()
	require.Len(t, frames, 3, "1 预热 + 2 业务（连接复用不再预热）")
	prewarmFrame := frames[0]
	assert.Equal(t, false, prewarmFrame["generate"])
	for _, businessFrame := range frames[1:] {
		metadata, _ := businessFrame["client_metadata"].(map[string]any)
		require.NotNil(t, metadata)
		assert.Equal(t, "gAAAAAit-prewarm", metadata["x-codex-turn-state"], "帧级回带必须生效")
	}

	health, err := client.Health(ctx, &pluginv1.HealthRequest{})
	require.NoError(t, err)
	assert.Contains(t, health.StatusJson, `"ws_bridged_requests":2`)
	assert.Contains(t, health.StatusJson, `"ws_prewarm_tickets":1`)
}

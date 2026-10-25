// Package pluginconfig 定义 codex-bridge 的配置结构、默认值与严格校验。
// JSON 字段统一 snake_case；解析拒绝未知字段；空对象规范化为完整默认配置。
package pluginconfig

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

// Config 是插件的全部可调项。所有开关默认取「官方契约对齐」形态。
type Config struct {
	// ManageTurnState 开启后由插件接管 x-codex-turn-state：
	// 剥离宿主/下游注入的值，从上游响应头捕获票，并按 (账号, 会话线程) 回带。
	ManageTurnState bool `json:"manage_turn_state"`
	// TurnStateTTLSeconds 是票的回带窗口；窗口过后不再回带（近似官方 per-turn 边界）。
	TurnStateTTLSeconds int `json:"turn_state_ttl_seconds"`
	// CookieJarEnabled 开启 per-account Cookie jar（Cloudflare/OpenAI 基础设施 cookie 白名单）。
	CookieJarEnabled bool `json:"cookie_jar_enabled"`
	// MaxIdleConnsPerAccount 是每个 (账号, 代理) 连接池的每主机空闲连接上限。
	MaxIdleConnsPerAccount int `json:"max_idle_conns_per_account"`
	// IdleConnTimeoutSeconds 是池内空闲连接回收时间。
	IdleConnTimeoutSeconds int `json:"idle_conn_timeout_seconds"`
	// DialTimeoutSeconds 是上游拨号超时（含 TLS 握手）。
	DialTimeoutSeconds int `json:"dial_timeout_seconds"`
	// ResponseHeaderTimeoutSeconds 是等待上游响应头的超时（流式 body 不受此限制）。
	ResponseHeaderTimeoutSeconds int `json:"response_header_timeout_seconds"`
	// ObserveDowngrade 开启降智观测：记录 safety-buffering 响应头与
	// 请求模型 vs 实际服务模型（openai-model）的不一致计数。
	ObserveDowngrade bool `json:"observe_downgrade"`
	// ModelSniffBytes 是从请求体首段提取 "model" 字段的扫描窗口；0 关闭请求模型嗅探。
	ModelSniffBytes int `json:"model_sniff_bytes"`
	// WSBridgeEnabled 开启上游 WS 桥：POST /responses 请求经官方 WS 通道
	// （wss + responses_websockets=2026-02-06）出站，对宿主仍呈现 HTTP+SSE。
	// 默认关闭；开启前建议先灰度 HTTP 契约对齐模式。
	WSBridgeEnabled bool `json:"ws_bridge_enabled"`
	// WSPrewarmEnabled 开启后每条新 WS 连接先发 generate=false 预热帧
	// （官方线程启动同款：连接建立即铸票，无生成 token），票注入首个业务帧。
	WSPrewarmEnabled bool `json:"ws_prewarm_enabled"`
	// WSHandshakeTimeoutSeconds 是 WS 拨号+握手超时。
	WSHandshakeTimeoutSeconds int `json:"ws_handshake_timeout_seconds"`
	// WSEventIdleTimeoutSeconds 是流内相邻事件的最大间隔，超时中止。
	WSEventIdleTimeoutSeconds int `json:"ws_event_idle_timeout_seconds"`
	// WSMaxPoolSize 是每个 (账号, 代理) 的空闲 WS 连接上限。
	WSMaxPoolSize int `json:"ws_max_pool_size"`
	// WSReadLimitBytes 是单条上游 WS 消息的读上限。coder/websocket 默认仅
	// 32KiB，长会话 response.completed 必超限（1009 断流），故显式可配。
	WSReadLimitBytes int64 `json:"ws_read_limit_bytes"`
	// WSIdleTTLSeconds 是池内空闲 WS 连接的最大复用年龄：acquire 时超龄连接
	// 弃用（关连接不入池），防陈旧连接写帧失败走硬失败。
	WSIdleTTLSeconds int `json:"ws_idle_ttl_seconds"`
}

// Default 返回默认配置。
func Default() Config {
	return Config{
		ManageTurnState:              true,
		TurnStateTTLSeconds:          300,
		CookieJarEnabled:             true,
		MaxIdleConnsPerAccount:       8,
		IdleConnTimeoutSeconds:       120,
		DialTimeoutSeconds:           15,
		ResponseHeaderTimeoutSeconds: 600,
		ObserveDowngrade:             true,
		ModelSniffBytes:              4096,
		WSBridgeEnabled:              false,
		WSPrewarmEnabled:             true,
		WSHandshakeTimeoutSeconds:    15,
		WSEventIdleTimeoutSeconds:    300,
		WSMaxPoolSize:                4,
		WSReadLimitBytes:             16 << 20,
		WSIdleTTLSeconds:             120,
	}
}

// Validate 检查数值范围并返回规范化后的配置。
func (c Config) Validate() (Config, error) {
	def := Default()
	if c.TurnStateTTLSeconds == 0 {
		c.TurnStateTTLSeconds = def.TurnStateTTLSeconds
	}
	if c.MaxIdleConnsPerAccount == 0 {
		c.MaxIdleConnsPerAccount = def.MaxIdleConnsPerAccount
	}
	if c.IdleConnTimeoutSeconds == 0 {
		c.IdleConnTimeoutSeconds = def.IdleConnTimeoutSeconds
	}
	if c.DialTimeoutSeconds == 0 {
		c.DialTimeoutSeconds = def.DialTimeoutSeconds
	}
	if c.ResponseHeaderTimeoutSeconds == 0 {
		c.ResponseHeaderTimeoutSeconds = def.ResponseHeaderTimeoutSeconds
	}
	if c.ModelSniffBytes == 0 {
		c.ModelSniffBytes = def.ModelSniffBytes
	}
	if c.WSHandshakeTimeoutSeconds == 0 {
		c.WSHandshakeTimeoutSeconds = def.WSHandshakeTimeoutSeconds
	}
	if c.WSEventIdleTimeoutSeconds == 0 {
		c.WSEventIdleTimeoutSeconds = def.WSEventIdleTimeoutSeconds
	}
	if c.WSMaxPoolSize == 0 {
		c.WSMaxPoolSize = def.WSMaxPoolSize
	}
	if c.WSReadLimitBytes == 0 {
		c.WSReadLimitBytes = def.WSReadLimitBytes
	}
	if c.WSIdleTTLSeconds == 0 {
		c.WSIdleTTLSeconds = def.WSIdleTTLSeconds
	}
	if c.TurnStateTTLSeconds < 30 || c.TurnStateTTLSeconds > 3600 {
		return c, fmt.Errorf("turn_state_ttl_seconds 超出范围 [30, 3600]: %d", c.TurnStateTTLSeconds)
	}
	if c.MaxIdleConnsPerAccount < 1 || c.MaxIdleConnsPerAccount > 64 {
		return c, fmt.Errorf("max_idle_conns_per_account 超出范围 [1, 64]: %d", c.MaxIdleConnsPerAccount)
	}
	if c.IdleConnTimeoutSeconds < 5 || c.IdleConnTimeoutSeconds > 600 {
		return c, fmt.Errorf("idle_conn_timeout_seconds 超出范围 [5, 600]: %d", c.IdleConnTimeoutSeconds)
	}
	if c.DialTimeoutSeconds < 3 || c.DialTimeoutSeconds > 120 {
		return c, fmt.Errorf("dial_timeout_seconds 超出范围 [3, 120]: %d", c.DialTimeoutSeconds)
	}
	if c.ResponseHeaderTimeoutSeconds < 30 || c.ResponseHeaderTimeoutSeconds > 3600 {
		return c, fmt.Errorf("response_header_timeout_seconds 超出范围 [30, 3600]: %d", c.ResponseHeaderTimeoutSeconds)
	}
	if c.ModelSniffBytes < 0 || c.ModelSniffBytes > 65536 {
		return c, fmt.Errorf("model_sniff_bytes 超出范围 [0, 65536]: %d", c.ModelSniffBytes)
	}
	if c.WSHandshakeTimeoutSeconds < 3 || c.WSHandshakeTimeoutSeconds > 60 {
		return c, fmt.Errorf("ws_handshake_timeout_seconds 超出范围 [3, 60]: %d", c.WSHandshakeTimeoutSeconds)
	}
	if c.WSEventIdleTimeoutSeconds < 30 || c.WSEventIdleTimeoutSeconds > 3600 {
		return c, fmt.Errorf("ws_event_idle_timeout_seconds 超出范围 [30, 3600]: %d", c.WSEventIdleTimeoutSeconds)
	}
	if c.WSMaxPoolSize < 1 || c.WSMaxPoolSize > 16 {
		return c, fmt.Errorf("ws_max_pool_size 超出范围 [1, 16]: %d", c.WSMaxPoolSize)
	}
	if c.WSReadLimitBytes < 64<<10 || c.WSReadLimitBytes > 256<<20 {
		return c, fmt.Errorf("ws_read_limit_bytes 超出范围 [64KiB, 256MiB]: %d", c.WSReadLimitBytes)
	}
	if c.WSIdleTTLSeconds < 5 || c.WSIdleTTLSeconds > 3600 {
		return c, fmt.Errorf("ws_idle_ttl_seconds 超出范围 [5, 3600]: %d", c.WSIdleTTLSeconds)
	}
	return c, nil
}

// Parse 严格解析配置 JSON：拒绝未知字段与多余对象；空输入规范化为默认配置。
func Parse(raw []byte) (Config, error) {
	cfg := Default()
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return cfg.Validate()
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("解析插件配置: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err == nil {
		return Config{}, errors.New("插件配置只能包含一个 JSON 对象")
	}
	return cfg.Validate()
}

// Marshal 输出规范化 JSON（配置回显与保存使用）。
func Marshal(c Config) []byte {
	out, err := json.Marshal(c)
	if err != nil {
		// Config 只含基础类型，Marshal 不可能失败。
		return []byte("{}")
	}
	return out
}

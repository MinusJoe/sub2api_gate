// Package metrics 维护插件的被动观测计数器。
// 只聚合无敏感信息的计数与模型名字符串，供 Health.status_json 暴露。
package metrics

import (
	"encoding/json"
	"sync"
	"sync/atomic"
)

// Counters 是进程生命周期的累计计数。所有字段只增不减。
type Counters struct {
	// RequestsTotal 是收到的 Forward 请求数。
	RequestsTotal atomic.Uint64
	// ResponsesTotal 是成功转发并开始回传的上游响应数。
	ResponsesTotal atomic.Uint64
	// ErrorsTotal 是转发失败（上游错误或协议错误）数。
	ErrorsTotal atomic.Uint64
	// TicketsCaptured 是从上游响应头捕获到的 x-codex-turn-state 数。
	TicketsCaptured atomic.Uint64
	// TicketsReplayed 是向出站请求回带的票数。
	TicketsReplayed atomic.Uint64
	// SafetyBufferingResponses 是携带 x-codex-safety-buffering-enabled: true 的响应数。
	SafetyBufferingResponses atomic.Uint64
	// ModelDowngrades 是能同时观测到请求模型与实际服务模型且二者不一致的响应数。
	ModelDowngrades atomic.Uint64
	// WSBridgedRequests 是经上游 WS 桥转发的请求数。
	WSBridgedRequests atomic.Uint64
	// WSPrewarmAttempts 是发出的 generate=false 预热帧数。
	WSPrewarmAttempts atomic.Uint64
	// WSPrewarmTickets 是预热帧铸出的票数（response.metadata 事件捕获）。
	WSPrewarmTickets atomic.Uint64
	// WSFallbackHTTP 是 WS 桥失败后回退 HTTP 透传的次数。
	WSFallbackHTTP atomic.Uint64
	// WSErrors 是 WS 桥中流错误数（已开始回传后失败）。
	WSErrors atomic.Uint64
	// InFlight 是当前正在处理的 Forward 数。
	InFlight atomic.Int64
}

// New 返回零值计数器。
func New() *Counters { return &Counters{} }

// DowngradeSample 记录最近一次降智观测快照（mutex 保护的小结构）。
type DowngradeSample struct {
	RequestedModel string `json:"requested_model"`
	ServedModel    string `json:"served_model"`
	FasterModel    string `json:"faster_model,omitempty"`
	At             string `json:"at"`
}

// Recorder 聚合计数与最近样本。
type Recorder struct {
	counters *Counters
	mu       sync.Mutex
	last     DowngradeSample
	lastSet  bool
}

// NewRecorder 创建记录器。
func NewRecorder(counters *Counters) *Recorder { return &Recorder{counters: counters} }

// ObserveDowngrade 记录一次降智观测。
func (r *Recorder) ObserveDowngrade(sample DowngradeSample) {
	r.counters.ModelDowngrades.Add(1)
	r.mu.Lock()
	r.last = sample
	r.lastSet = true
	r.mu.Unlock()
}

// Snapshot 返回可序列化的状态（供 Health.status_json，纯被动读取）。
func (r *Recorder) Snapshot() json.RawMessage {
	c := r.counters
	r.mu.Lock()
	last := r.last
	hasLast := r.lastSet
	r.mu.Unlock()
	payload := struct {
		RequestsTotal            uint64           `json:"requests_total"`
		ResponsesTotal           uint64           `json:"responses_total"`
		ErrorsTotal              uint64           `json:"errors_total"`
		InFlight                 int64            `json:"in_flight"`
		TicketsCaptured          uint64           `json:"tickets_captured"`
		TicketsReplayed          uint64           `json:"tickets_replayed"`
		SafetyBufferingResponses uint64           `json:"safety_buffering_responses"`
		ModelDowngrades          uint64           `json:"model_downgrades"`
		WSBridgedRequests        uint64           `json:"ws_bridged_requests"`
		WSPrewarmAttempts        uint64           `json:"ws_prewarm_attempts"`
		WSPrewarmTickets         uint64           `json:"ws_prewarm_tickets"`
		WSFallbackHTTP           uint64           `json:"ws_fallback_http"`
		WSErrors                 uint64           `json:"ws_errors"`
		LastDowngrade            *DowngradeSample `json:"last_downgrade,omitempty"`
	}{
		RequestsTotal:            c.RequestsTotal.Load(),
		ResponsesTotal:           c.ResponsesTotal.Load(),
		ErrorsTotal:              c.ErrorsTotal.Load(),
		InFlight:                 c.InFlight.Load(),
		TicketsCaptured:          c.TicketsCaptured.Load(),
		TicketsReplayed:          c.TicketsReplayed.Load(),
		SafetyBufferingResponses: c.SafetyBufferingResponses.Load(),
		ModelDowngrades:          c.ModelDowngrades.Load(),
		WSBridgedRequests:        c.WSBridgedRequests.Load(),
		WSPrewarmAttempts:        c.WSPrewarmAttempts.Load(),
		WSPrewarmTickets:         c.WSPrewarmTickets.Load(),
		WSFallbackHTTP:           c.WSFallbackHTTP.Load(),
		WSErrors:                 c.WSErrors.Load(),
	}
	if hasLast {
		sample := last
		payload.LastDowngrade = &sample
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return json.RawMessage(`{"error":"snapshot"}`)
	}
	return raw
}

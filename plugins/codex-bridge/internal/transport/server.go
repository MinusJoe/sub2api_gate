package transport

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MACOS-DO/sub4api-plugins/codex-bridge/internal/metrics"
	"github.com/MACOS-DO/sub4api-plugins/codex-bridge/internal/pluginconfig"
	"github.com/MACOS-DO/sub4api-plugins/codex-bridge/internal/turnstate"
	pluginv1 "github.com/MACOS-DO/sub4api/pkg/pluginapi/v1"
)

// Server 实现 TransportPlugin 服务（openai.oauth.outbound_transport.v1）。
type Server struct {
	pluginv1.UnimplementedTransportPluginServer

	pluginID  string
	version   string
	cfg       atomic.Pointer[pluginconfig.Config]
	forwarder *Forwarder
	recorder  *metrics.Recorder
	counters  *metrics.Counters

	// drainWG 追踪在途的 drainRequestBody goroutine，供测试等待其退出。
	drainWG sync.WaitGroup
}

// NewServer 创建服务实现。
func NewServer(pluginID, version string, cfg pluginconfig.Config, counters *metrics.Counters) (*Server, error) {
	normalized, err := cfg.Validate()
	if err != nil {
		return nil, fmt.Errorf("初始配置无效: %w", err)
	}
	if counters == nil {
		counters = metrics.New()
	}
	store := turnstate.NewStore(time.Duration(normalized.TurnStateTTLSeconds) * time.Second)
	recorder := metrics.NewRecorder(counters)
	s := &Server{
		pluginID:  pluginID,
		version:   version,
		forwarder: NewForwarder(normalized, store, recorder, counters),
		recorder:  recorder,
		counters:  counters,
	}
	s.cfg.Store(&normalized)
	return s, nil
}

// GetInfo 返回与 manifest.source.json 一致的身份声明。
func (s *Server) GetInfo(context.Context, *pluginv1.GetInfoRequest) (*pluginv1.GetInfoResponse, error) {
	return &pluginv1.GetInfoResponse{
		PluginId:            s.pluginID,
		PluginVersion:       s.version,
		ProtocolVersion:     pluginv1.ProtocolVersion,
		TransportApiVersion: pluginv1.TransportAPIVersion,
		Capabilities:        []string{capabilityOpenAIOAuthOutbound},
	}, nil
}

// Health 返回被动状态快照（不触网、无副作用）。
func (s *Server) Health(context.Context, *pluginv1.HealthRequest) (*pluginv1.HealthResponse, error) {
	return &pluginv1.HealthResponse{
		Healthy:    true,
		Message:    "codex-bridge ready",
		StatusJson: string(s.recorder.Snapshot()),
	}, nil
}

// ValidateConfig 严格校验并返回规范化配置。
func (s *Server) ValidateConfig(_ context.Context, req *pluginv1.ValidateConfigRequest) (*pluginv1.ValidateConfigResponse, error) {
	cfg, err := pluginconfig.Parse(req.ConfigJson)
	if err != nil {
		return &pluginv1.ValidateConfigResponse{Valid: false, Message: err.Error()}, nil
	}
	return &pluginv1.ValidateConfigResponse{Valid: true, Message: "ok", NormalizedConfigJson: pluginconfig.Marshal(cfg)}, nil
}

// ApplyConfig 校验成功后原子切换配置并重建连接池。
func (s *Server) ApplyConfig(_ context.Context, req *pluginv1.ApplyConfigRequest) (*pluginv1.ApplyConfigResponse, error) {
	cfg, err := pluginconfig.Parse(req.ConfigJson)
	if err != nil {
		return &pluginv1.ApplyConfigResponse{Applied: false, Message: err.Error()}, nil
	}
	if err := s.forwarder.ApplyConfig(cfg); err != nil {
		return &pluginv1.ApplyConfigResponse{Applied: false, Message: err.Error()}, nil
	}
	s.cfg.Store(&cfg)
	return &pluginv1.ApplyConfigResponse{Applied: true, Message: "ok"}, nil
}

// TestConfig 做本地快速诊断（纯被动：不触网、不应用配置）。
func (s *Server) TestConfig(_ context.Context, _ *pluginv1.TestConfigRequest) (*pluginv1.TestConfigResponse, error) {
	cfg := *s.cfg.Load()
	return &pluginv1.TestConfigResponse{
		Success:    true,
		Message:    fmt.Sprintf("config ok (manage_turn_state=%v, ttl=%ds, cookie_jar=%v, observe_downgrade=%v, tickets=%d)", cfg.ManageTurnState, cfg.TurnStateTTLSeconds, cfg.CookieJarEnabled, cfg.ObserveDowngrade, s.forwarder.StoreLen()),
		LatencyMs:  0,
		StatusJson: string(s.recorder.Snapshot()),
	}, nil
}

const forwardBodyChunkBuffer = 32 * 1024

// Forward 按协议转发：接收 start/body_chunk/body_end 帧，发出上游请求，
// 再按 start/body_chunk/end 顺序回传响应流。
func (s *Server) Forward(stream pluginv1.TransportPlugin_ForwardServer) error {
	ctx := stream.Context()
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	start := first.GetStart()
	if start == nil {
		return s.sendForwardError(stream, "protocol", "首帧必须是 start", false)
	}
	s.counters.RequestsTotal.Add(1)
	s.counters.InFlight.Add(1)
	defer s.counters.InFlight.Add(-1)

	var body io.Reader
	var pipeReader *io.PipeReader
	if start.HasBody {
		var pipeWriter *io.PipeWriter
		pipeReader, pipeWriter = io.Pipe()
		body = pipeReader
		s.drainWG.Add(1)
		go s.drainRequestBody(stream, pipeWriter)
	}

	spec := Outbound{
		Method:        start.Method,
		URL:           start.Url,
		Host:          start.Host,
		Header:        headersFromProto(start.Headers),
		Body:          body,
		ContentLength: start.ContentLength,
		AccountID:     start.AccountId,
		ProxyURL:      start.ProxyUrl,
	}

	began := time.Now()
	resp, _, requestSent, err := s.forwarder.Do(ctx, spec)
	if err != nil {
		if pipeReader != nil {
			// Do 失败时 http.Transport 不会再接管/读完 body：不关管道会让阻塞在
			// pipe Write 上的 drain goroutine 永久泄漏。CloseWithError 立即解除
			// 其阻塞；成功路径不走到这里，body 归 http.Transport 关闭。
			pipeReader.CloseWithError(err)
		}
		s.counters.ErrorsTotal.Add(1)
		return s.sendForwardError(stream, "upstream", sanitizeForwardError(err), requestSent)
	}
	defer resp.Body.Close()

	responseStart := &pluginv1.ForwardResponseStart{
		StatusCode:    int32(resp.StatusCode),
		Status:        resp.Status,
		Protocol:      resp.Proto,
		ProtocolMajor: int32(resp.ProtoMajor),
		ProtocolMinor: int32(resp.ProtoMinor),
		Headers:       headersToProto(resp.Header),
		ContentLength: resp.ContentLength,
	}
	if err := stream.Send(&pluginv1.ForwardResponse{Frame: &pluginv1.ForwardResponse_Start{Start: responseStart}}); err != nil {
		return err
	}

	var received int64
	buf := make([]byte, forwardBodyChunkBuffer)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			received += int64(n)
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			if err := stream.Send(&pluginv1.ForwardResponse{Frame: &pluginv1.ForwardResponse_BodyChunk{BodyChunk: chunk}}); err != nil {
				return err
			}
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				s.counters.ErrorsTotal.Add(1)
				return s.sendForwardError(stream, "upstream_body", sanitizeForwardError(readErr), true)
			}
			break
		}
	}

	return stream.Send(&pluginv1.ForwardResponse{Frame: &pluginv1.ForwardResponse_End{
		End: &pluginv1.ForwardResponseEnd{
			BytesReceived: received,
			DurationMs:    time.Since(began).Milliseconds(),
		},
	}})
}

// drainRequestBody 把宿主的 body 帧写入请求管道；流结束或出错时关闭管道。
func (s *Server) drainRequestBody(stream pluginv1.TransportPlugin_ForwardServer, writer *io.PipeWriter) {
	defer s.drainWG.Done()
	defer writer.Close()
	for {
		frame, err := stream.Recv()
		if err != nil {
			// io.EOF = 宿主半关闭（正常）；其他错误携带进请求体以中止上游读取。
			if !errors.Is(err, io.EOF) {
				writer.CloseWithError(err)
			}
			return
		}
		switch typed := frame.Frame.(type) {
		case *pluginv1.ForwardRequest_BodyChunk:
			if len(typed.BodyChunk) > 0 {
				if _, err := writer.Write(typed.BodyChunk); err != nil {
					return
				}
			}
		case *pluginv1.ForwardRequest_BodyEnd:
			return
		case *pluginv1.ForwardRequest_Start:
			// 协议违规：忽略重复 start（管道语义下按 body 处理会有歧义，直接终止）。
			writer.CloseWithError(errors.New("codex-bridge: 收到重复 start 帧"))
			return
		}
	}
}

func (s *Server) sendForwardError(stream pluginv1.TransportPlugin_ForwardServer, code, message string, requestSent bool) error {
	return stream.Send(&pluginv1.ForwardResponse{Frame: &pluginv1.ForwardResponse_Error{
		Error: &pluginv1.ForwardResponseError{Code: code, Message: message, RequestSent: requestSent},
	}})
}

// sanitizeForwardError 防止错误消息携带 URL 查询串、代理凭据等敏感信息。
func sanitizeForwardError(err error) string {
	message := err.Error()
	if idx := bytes.IndexByte([]byte(message), '?'); idx >= 0 {
		message = message[:idx] + "?…"
	}
	const maxErrLen = 300
	if len(message) > maxErrLen {
		message = message[:maxErrLen] + "…"
	}
	return message
}

func headersFromProto(source map[string]*pluginv1.HeaderValues) http.Header {
	header := http.Header{}
	for name, values := range source {
		if values == nil {
			continue
		}
		for _, value := range values.Values {
			header.Add(name, value)
		}
	}
	return header
}

func headersToProto(source http.Header) map[string]*pluginv1.HeaderValues {
	out := make(map[string]*pluginv1.HeaderValues, len(source))
	for name, values := range source {
		copied := make([]string, len(values))
		copy(copied, values)
		out[name] = &pluginv1.HeaderValues{Values: copied}
	}
	return out
}

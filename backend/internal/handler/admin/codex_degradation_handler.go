package admin

import (
	"github.com/MACOS-DO/sub4api/internal/pkg/response"
	"github.com/MACOS-DO/sub4api/internal/service"
	"github.com/gin-gonic/gin"
)

// GetCodexDegradationMetrics 暴露真实流量的降智信号聚合（只读）。
//
// 信号来自上游响应头 OpenAI-Model / x-codex-safety-buffering-* 与
// response.metadata safety_buffering 事件；默认 observe-only，不改变调度。
func (h *AccountHandler) GetCodexDegradationMetrics(c *gin.Context) {
	if h == nil || h.codexTicketGateway == nil {
		codexTicketError(c, service.ErrCodexTicketUnavailable)
		return
	}
	response.Success(c, h.codexTicketGateway.CodexDegradationMetrics())
}

// ResetCodexDegradationMetrics 清空进程内降智聚合（排障用）。
func (h *AccountHandler) ResetCodexDegradationMetrics(c *gin.Context) {
	if h == nil || h.codexTicketGateway == nil {
		codexTicketError(c, service.ErrCodexTicketUnavailable)
		return
	}
	h.codexTicketGateway.ResetCodexDegradationMetrics()
	response.Success(c, gin.H{"reset": true})
}

package service

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
)

// ErrCodexClientRestriction 与 /v1/responses 主路径使用同一个错误前缀：
// handler 依靠它判断"403 已经写出 / 或 WS 需要在关闭帧里说明原因"。
// 使用包级变量而不是每次 errors.New，便于调用方用 errors.Is 判断。
var ErrCodexClientRestriction = errors.New("codex_cli_only restriction: only codex official clients are allowed")

// codexExtraPathDenied 是扩展入口的共享判定：只做"是否拒绝 + 结果"，不写任何响应。
// 默认关闭（gateway.openai_codex_gate_extra_paths=false）时直接放行，
// 账号未开启 codex_cli_only 时也放行，保证"不配置 = 不变"。
func (s *OpenAIGatewayService) codexExtraPathDenied(c *gin.Context, account *Account, body []byte) (CodexClientRestrictionDetectionResult, bool) {
	if s == nil || s.cfg == nil || c == nil || c.Request == nil || account == nil {
		return CodexClientRestrictionDetectionResult{}, false
	}
	if !s.cfg.Gateway.OpenAICodexGateExtraPaths {
		return CodexClientRestrictionDetectionResult{}, false
	}
	if !account.IsCodexCLIOnlyEnabled() {
		return CodexClientRestrictionDetectionResult{}, false
	}
	result := s.detectCodexClientRestriction(c, account, body)
	logCodexCLIOnlyDetection(c.Request.Context(), c, account, getAPIKeyIDFromContext(c), result, body)
	return result, result.Enabled && !result.Matched
}

// rejectNonOfficialCodexClient 用于 HTTP 入口（images / embeddings / input_tokens / messages）：
// 判定拒绝时写出 403 JSON 并返回哨兵错误。
func (s *OpenAIGatewayService) rejectNonOfficialCodexClient(c *gin.Context, account *Account, body []byte) error {
	result, denied := s.codexExtraPathDenied(c, account, body)
	if !denied {
		return nil
	}
	MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
	c.JSON(http.StatusForbidden, gin.H{
		"error": gin.H{
			"type":    "forbidden_error",
			"message": CodexClientRestrictionMessage(result),
		},
	})
	return ErrCodexClientRestriction
}

// rejectNonOfficialCodexClientWS 用于 WS 直连入口：连接在账号调度之前就已经 Upgrade，
// 此时再写 HTTP 响应会把 HTTP 字节注入 WebSocket 流。这里只返回哨兵错误，
// 由 handler 用关闭帧（policy violation）告知客户端。
func (s *OpenAIGatewayService) rejectNonOfficialCodexClientWS(c *gin.Context, account *Account, body []byte) error {
	if _, denied := s.codexExtraPathDenied(c, account, body); denied {
		return ErrCodexClientRestriction
	}
	return nil
}

// IsCodexClientRestrictionError ?????????? codex_cli_only ?????
// ??????????????(?????????????),
// ???????????????,???????? EWMA ???
func IsCodexClientRestrictionError(err error) bool {
	return err != nil && errors.Is(err, ErrCodexClientRestriction)
}

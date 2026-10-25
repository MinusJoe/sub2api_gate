package service

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/MACOS-DO/sub4api/internal/pkg/logger"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// 业务响应头回收票（方案 A）：上游按「每 turn 首个非增量请求」自然铸票，
// 网关在自己的业务响应上直接接住即可，无需独立打票探针。
//
// 安全约束：
//   - 只接受上游真实下发的 state（Fernet 形态），绝不在本地构造/伪造；
//   - 只按 (账号, 模型) 存储，不跨账号共享；
//   - 写入限频（默认 60s/键），避免并发用户互相覆盖 generation；
//   - 仅 harvest 模式需要票，relay/off 模式只透传/剥离，不做回收存储。
const codexTicketRecycleMinInterval = 60 * time.Second

func (s *OpenAIGatewayService) captureOpenAICodexTicketFromBusinessResponse(c *gin.Context, account *Account, upstream http.Header) {
	if s == nil || c == nil || account == nil || account.ID <= 0 || upstream == nil {
		return
	}
	reqCtx := context.Background()
	if c.Request != nil {
		reqCtx = c.Request.Context()
	}
	if !s.openAICodexTicketEnabledContext(reqCtx) {
		return
	}
	// relay/off 不注入票，回收结果也无消费方；只保留给 harvest 模式，避免无意义写库。
	if s.openAICodexTurnStateMode() != openAICodexTurnStateModeHarvest {
		return
	}
	state := strings.TrimSpace(extractOpenAICodexTurnState(upstream))
	if !openAICodexTicketStateShapeValid(state, 0) {
		return
	}
	model := strings.TrimSpace(c.GetString(OpsUpstreamModelKey))
	if model == "" {
		// 没有可靠出站模型时不写库：错误模型键会制造跨模型票错配。
		return
	}
	model = normalizeOpenAICodexTicketModel(model)
	if !s.openAICodexTicketGatedModel(model) {
		return
	}
	now := time.Now().UTC()
	key := openAICodexTicketKey(account.ID, model)
	if previous, ok := s.openaiCodexTicketRecycleAt.Load(key); ok {
		if last, ok := previous.(time.Time); ok && now.Sub(last) < codexTicketRecycleMinInterval {
			return
		}
	}
	s.openaiCodexTicketRecycleAt.Store(key, now)
	ttl := s.openAICodexTicketTTL()
	ticket := &openAICodexTicket{
		AccountID:          account.ID,
		GenerationID:       uuid.NewString(),
		VerificationMethod: "business_response_v1",
		Model:              model,
		State:              state,
		Length:             len(state),
		CapturedAt:         now,
		ExpiresAt:          now.Add(ttl),
		Cookie:             strings.TrimSpace(extractOpenAICodexResponseCookies(upstream)),
		HTTPStatus:         http.StatusOK,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.storeOpenAICodexTicket(ctx, account, ticket); err != nil {
		logger.L().Warn("codex ticket recycle persist failed",
			zap.Int64("account_id", account.ID),
			zap.String("model", model),
			zap.Error(err),
		)
		return
	}
	logger.L().Debug("codex ticket recycled from business response",
		zap.Int64("account_id", account.ID),
		zap.String("model", model),
		zap.Int("length", len(state)),
	)
}

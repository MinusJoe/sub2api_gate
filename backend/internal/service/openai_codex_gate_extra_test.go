package service

import (
	"errors"
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/MACOS-DO/sub4api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// gateExtraStubDetector 让测试直接控制判定结果，避免依赖真实的头/UA 规则。
type gateExtraStubDetector struct {
	result CodexClientRestrictionDetectionResult
}

func (d gateExtraStubDetector) Detect(*gin.Context, *Account, CodexRestrictionPolicy, []byte) CodexClientRestrictionDetectionResult {
	return d.result
}

// newGateExtraService 构造一个只带配置与桩探测器的最小服务。
func newGateExtraService(t *testing.T, gateEnabled bool, result CodexClientRestrictionDetectionResult) (*OpenAIGatewayService, *gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{}
	cfg.Gateway.OpenAICodexGateExtraPaths = gateEnabled
	svc := &OpenAIGatewayService{cfg: cfg, codexDetector: gateExtraStubDetector{result: result}}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/images/generations", nil)
	return svc, c, w
}

func codexGateAccount(enabled bool) *Account {
	extra := map[string]any{}
	if enabled {
		extra["codex_cli_only"] = true
	}
	return &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: extra}
}

// 默认关闭：即使账号开启了 codex_cli_only、探测器判否，扩展入口也不得拦截。
func TestRejectNonOfficialCodexClientExtraPathsDisabled(t *testing.T) {
	t.Cleanup(func() { SetCodexCLIOnlyDefault(false) })
	SetCodexCLIOnlyDefault(false)

	svc, c, w := newGateExtraService(t, false, CodexClientRestrictionDetectionResult{Enabled: true, Matched: false})
	err := svc.rejectNonOfficialCodexClient(c, codexGateAccount(true), nil)

	require.NoError(t, err)
	require.Equal(t, 0, w.Body.Len(), "未开启扩展门时不得写出响应")
}

// 账号未开启 codex_cli_only（且全局默认关闭）时放行。
func TestRejectNonOfficialCodexClientAccountGateOff(t *testing.T) {
	t.Cleanup(func() { SetCodexCLIOnlyDefault(false) })
	SetCodexCLIOnlyDefault(false)

	svc, c, w := newGateExtraService(t, true, CodexClientRestrictionDetectionResult{Enabled: true, Matched: false})
	err := svc.rejectNonOfficialCodexClient(c, codexGateAccount(false), nil)

	require.NoError(t, err)
	require.Equal(t, 0, w.Body.Len())
}

// 探测器判为官方客户端时放行。
func TestRejectNonOfficialCodexClientMatched(t *testing.T) {
	t.Cleanup(func() { SetCodexCLIOnlyDefault(false) })
	SetCodexCLIOnlyDefault(false)

	svc, c, w := newGateExtraService(t, true, CodexClientRestrictionDetectionResult{Enabled: true, Matched: true})
	err := svc.rejectNonOfficialCodexClient(c, codexGateAccount(true), nil)

	require.NoError(t, err)
	require.Equal(t, 0, w.Body.Len())
}

// 判否时必须写出 403，并返回可识别的哨兵错误（handler 靠它避免追加 SSE）。
func TestRejectNonOfficialCodexClientDenied(t *testing.T) {
	t.Cleanup(func() { SetCodexCLIOnlyDefault(false) })
	SetCodexCLIOnlyDefault(false)

	svc, c, w := newGateExtraService(t, true, CodexClientRestrictionDetectionResult{
		Enabled: true,
		Matched: false,
		Reason:  CodexClientRestrictionReasonNotMatchedUA,
	})
	err := svc.rejectNonOfficialCodexClient(c, codexGateAccount(true), nil)

	require.Error(t, err)
	require.True(t, errors.Is(err, ErrCodexClientRestriction), "必须是哨兵错误，便于上层识别")
	require.Equal(t, 403, w.Code)
	require.Contains(t, w.Body.String(), CodexOfficialClientsOnlyMessage)
}

// detector 返回 Enabled=false（账号实际未开启）时不应拦截，避免与账号状态分叉。
func TestRejectNonOfficialCodexClientDetectorDisabled(t *testing.T) {
	t.Cleanup(func() { SetCodexCLIOnlyDefault(false) })
	SetCodexCLIOnlyDefault(false)

	svc, c, w := newGateExtraService(t, true, CodexClientRestrictionDetectionResult{Enabled: false})
	err := svc.rejectNonOfficialCodexClient(c, codexGateAccount(true), nil)

	require.NoError(t, err)
	require.Equal(t, 0, w.Body.Len())
}

// WS 入口在 Upgrade 之后才判定：不能写 HTTP 响应（会把 HTTP 字节注入 WS 流），
// 只能返回哨兵错误，由 handler 用 policy-violation 关闭帧告知客户端。
func TestRejectNonOfficialCodexClientWSDoesNotWriteHTTP(t *testing.T) {
	t.Cleanup(func() { SetCodexCLIOnlyDefault(false) })
	SetCodexCLIOnlyDefault(false)

	svc, c, w := newGateExtraService(t, true, CodexClientRestrictionDetectionResult{
		Enabled: true,
		Matched: false,
		Reason:  CodexClientRestrictionReasonNotMatchedUA,
	})
	err := svc.rejectNonOfficialCodexClientWS(c, codexGateAccount(true), nil)

	require.True(t, errors.Is(err, ErrCodexClientRestriction))
	require.Equal(t, 0, w.Body.Len(), "WS 路径不得写 HTTP 响应体")
	require.Equal(t, 200, w.Code, "WS 路径不得改写状态码")
}

// WS 路径同样要遵守默认关闭：未开启扩展门时放行。
func TestRejectNonOfficialCodexClientWSDisabled(t *testing.T) {
	t.Cleanup(func() { SetCodexCLIOnlyDefault(false) })
	SetCodexCLIOnlyDefault(false)

	svc, c, _ := newGateExtraService(t, false, CodexClientRestrictionDetectionResult{Enabled: true, Matched: false})
	require.NoError(t, svc.rejectNonOfficialCodexClientWS(c, codexGateAccount(true), nil))
}

// 哨兵错误必须可识别（含包裹），其它错误不得被误判成策略拒绝。
func TestIsCodexClientRestrictionError(t *testing.T) {
	require.True(t, IsCodexClientRestrictionError(ErrCodexClientRestriction))
	require.True(t, IsCodexClientRestrictionError(fmt.Errorf("wrapped: %w", ErrCodexClientRestriction)))
	require.False(t, IsCodexClientRestrictionError(nil))
	require.False(t, IsCodexClientRestrictionError(errors.New("upstream 500")))
}

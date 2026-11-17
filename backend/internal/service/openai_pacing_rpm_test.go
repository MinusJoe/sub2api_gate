package service

import (
	"testing"
	"time"

	"github.com/MACOS-DO/sub4api/internal/config"
	"github.com/stretchr/testify/require"
)

// 默认关闭：未配置 RPM 时任何账号都允许，且不需要清理状态。
func TestOpenAIAccountRPMLimiterDisabled(t *testing.T) {
	limiter := newOpenAIAccountRPMLimiter(0)
	for i := 0; i < 100; i++ {
		require.True(t, limiter.allow(1))
		limiter.consume(1)
	}
	require.True(t, limiter.allow(1))
}

// 超过上限后 allow 返回 false；窗口滑过后恢复。
func TestOpenAIAccountRPMLimiterWindow(t *testing.T) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	limiter := newOpenAIAccountRPMLimiter(2)
	limiter.now = func() time.Time { return now }

	require.True(t, limiter.allow(7))
	limiter.consume(7)
	require.True(t, limiter.allow(7))
	limiter.consume(7)
	require.False(t, limiter.allow(7), "达到上限后必须跳过")

	// 另一个账号不受影响（按账号隔离）。
	require.True(t, limiter.allow(8))

	// 窗口滑过 61s 后恢复。
	now = now.Add(61 * time.Second)
	require.True(t, limiter.allow(7))
}

// 只检查不计数：调度探测不能把额度吃掉。
func TestOpenAIAccountRPMAllowDoesNotConsume(t *testing.T) {
	limiter := newOpenAIAccountRPMLimiter(1)
	for i := 0; i < 10; i++ {
		require.True(t, limiter.allow(9), "allow 不应消耗额度")
	}
	limiter.consume(9)
	require.False(t, limiter.allow(9))
}

// 发布到进程级后可通过 OpenAIPacingRPM 读回；负数按关闭处理。
func TestSetOpenAIPacingRPM(t *testing.T) {
	t.Cleanup(func() { SetOpenAIPacingRPM(0) })
	SetOpenAIPacingRPM(5)
	require.Equal(t, 5, OpenAIPacingRPM())
	SetOpenAIPacingRPM(-3)
	require.Equal(t, 0, OpenAIPacingRPM())
}

// 装配层发布：配置里的值必须真的生效；nil 配置不清空已发布值。
func TestApplyCodexOutboundConfigPublishesRPM(t *testing.T) {
	t.Cleanup(func() { SetOpenAIPacingRPM(0) })
	cfg := &config.Config{}
	cfg.Gateway.OpenAIPacingRPM = 11
	applyCodexOutboundConfig(cfg)
	require.Equal(t, 11, OpenAIPacingRPM())

	applyCodexOutboundConfig(nil)
	require.Equal(t, 11, OpenAIPacingRPM(), "nil config 不得清空已发布值")
}

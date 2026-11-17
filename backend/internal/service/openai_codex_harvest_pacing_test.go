package service

import (
	"testing"
	"time"

	"github.com/MACOS-DO/sub4api/internal/config"
	"github.com/stretchr/testify/require"
)

func harvestPacingCfg() config.OpenAICodexTicketConfig {
	return config.OpenAICodexTicketConfig{
		HarvestProbeIntervalSeconds: 60,
		HarvestProbeJitterRatio:     0.3,
		HarvestNightMultiplier:      3,
		HarvestNightStartHour:       23,
		HarvestNightEndHour:         7,
	}
}

// 抖动必须在 ±ratio 之间，且下界不能低于 1s。
func TestHarvestScanBaseDelayJitterBounds(t *testing.T) {
	t.Cleanup(func() { SetDefaultOpenAIRequestTimezone("") })
	SetDefaultOpenAIRequestTimezone("UTC")
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	cfg := harvestPacingCfg()
	cfg.HarvestNightMultiplier = 1 // 白天不放大，只看抖动

	require.Equal(t, 42*time.Second, harvestScanBaseDelay(cfg, now, 0))
	require.Equal(t, 60*time.Second, harvestScanBaseDelay(cfg, now, 0.5))
	require.Equal(t, 78*time.Second, harvestScanBaseDelay(cfg, now, 1))

	cfg.HarvestProbeIntervalSeconds = 1
	cfg.HarvestProbeJitterRatio = 1
	require.Equal(t, time.Second, harvestScanBaseDelay(cfg, now, 0), "下界不能低于 1s")
}

// 夜间窗口按进程默认时区判断，并支持跨零点。
func TestInHarvestNightWindow(t *testing.T) {
	t.Cleanup(func() { SetDefaultOpenAIRequestTimezone("") })
	SetDefaultOpenAIRequestTimezone("Asia/Tokyo")

	at := func(hour int) time.Time {
		return time.Date(2026, 11, 18, hour, 30, 0, 0, time.FixedZone("UTC", 0))
	}
	// 23→7 窗口：UTC 15:30 = JST 00:30 属于夜间；UTC 03:30 = JST 12:30 不属于。
	require.True(t, inHarvestNightWindow(at(15), 23, 7))
	require.False(t, inHarvestNightWindow(at(3), 23, 7))
	require.True(t, inHarvestNightWindow(at(14), 23, 7), "JST 23:30")
	require.False(t, inHarvestNightWindow(at(13), 23, 7), "JST 22:30")
	// 非跨零点窗口与非法窗口。
	require.True(t, inHarvestNightWindow(at(18), 0, 8), "JST 03:30 ??? 0?8 ??")
	require.False(t, inHarvestNightWindow(at(3), 5, 5))
}

// 夜间倍数只作用于基础间隔；不配置时逐值等于原行为。
func TestHarvestScanBaseDelayNightMultiplier(t *testing.T) {
	t.Cleanup(func() { SetDefaultOpenAIRequestTimezone("") })
	SetDefaultOpenAIRequestTimezone("UTC")
	night := time.Date(2026, 11, 18, 16, 0, 0, 0, time.UTC) // JST 01:00
	day := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)

	cfg := harvestPacingCfg()
	cfg.HarvestProbeJitterRatio = 0
	require.Equal(t, 180*time.Second, harvestScanBaseDelay(cfg, night, 0.5))
	require.Equal(t, 60*time.Second, harvestScanBaseDelay(cfg, day, 0.5))

	// 默认（multiplier=1, jitter=0）不改变原节奏。
	plain := config.OpenAICodexTicketConfig{HarvestProbeIntervalSeconds: 6}
	require.Equal(t, 6*time.Second, harvestScanBaseDelay(plain, day, 0.5))
	require.Equal(t, 6*time.Second, harvestScanBaseDelay(plain, night, 0.5))
}

package service

import (
	"math/rand/v2"
	"time"

	"github.com/MACOS-DO/sub4api/internal/config"
)

// harvestScanBaseDelay 计算打票扫描的基础间隔。
//
// 动机：默认 6s 的固定节奏是高度机械的信号——每账号每 6 秒一次、误差为零。
// 这里做两件事，都不改变"要不要打票"的判断，只改变"什么时候打"：
//   - 抖动：在 [1-ratio, 1+ratio] 之间随机，打散固定周期；
//   - 夜间放缓：落在夜间窗口时乘以 multiplier（默认 1 = 关闭）。
//
// 返回值只作为基础间隔；调用方仍会与"计划重试时间"取较小值，
// 因此 20-40s 的精确重试不会被抖动拉长。
func harvestScanBaseDelay(cfg config.OpenAICodexTicketConfig, now time.Time, u float64) time.Duration {
	base := time.Duration(cfg.HarvestProbeIntervalSeconds) * time.Second
	if base <= 0 {
		base = 6 * time.Second
	}
	if cfg.HarvestNightMultiplier > 1 && inHarvestNightWindow(now, cfg.HarvestNightStartHour, cfg.HarvestNightEndHour) {
		base = time.Duration(float64(base) * cfg.HarvestNightMultiplier)
	}
	ratio := cfg.HarvestProbeJitterRatio
	if ratio < 0 {
		ratio = 0
	}
	if ratio > 1 {
		ratio = 1
	}
	if u < 0 {
		u = 0
	}
	if u > 1 {
		u = 1
	}
	if ratio > 0 {
		base = time.Duration(float64(base) * (1 + ratio*(2*u-1)))
	}
	if base < time.Second {
		base = time.Second
	}
	return base
}

// inHarvestNightWindow 判断 now 是否落在夜间窗口内（支持跨零点，如 23→7）。
// 时区取进程默认请求时区（与出口区域一致），取不到时退回本机时区。
func inHarvestNightWindow(now time.Time, startHour, endHour int) bool {
	if startHour < 0 || startHour > 23 || endHour < 0 || endHour > 23 || startHour == endHour {
		return false
	}
	loc := time.Local
	if tz := DefaultOpenAIRequestTimezoneResolved(); tz != "" {
		if parsed, err := time.LoadLocation(tz); err == nil {
			loc = parsed
		}
	}
	hour := now.In(loc).Hour()
	if startHour < endHour {
		return hour >= startHour && hour < endHour
	}
	return hour >= startHour || hour < endHour
}

// harvestScanBaseDelayNow 是生产调用：用真实随机数取抖动。
func harvestScanBaseDelayNow(cfg config.OpenAICodexTicketConfig, now time.Time) time.Duration {
	return harvestScanBaseDelay(cfg, now, rand.Float64())
}

package service

import (
	"net/http"
	"strings"
	"sync/atomic"
)

// openAIEgressRegion 是进程级出口区域预设（gateway.openai_egress_region），
// 只保存 lowercase/trim 后的值；空串表示未配置，此时不产生任何预设信号。
// 热路径只读，故用 atomic.Value 存字符串快照。
var openAIEgressRegion = func() *atomic.Value {
	v := &atomic.Value{}
	v.Store("")
	return v
}()

// SetOpenAIEgressRegion 发布出口区域预设。取值由配置层校验为 ""|"jp"|"sg"；
// 这里只做 lowercase / trim 归一，非法值不会命中任何预设（等价于关闭）。
func SetOpenAIEgressRegion(region string) {
	openAIEgressRegion.Store(strings.ToLower(strings.TrimSpace(region)))
}

// openAIEgressRegionAcceptLanguage 按出口区域返回与之一致的 accept-language 预设：
// 出口在日本就声明日语偏好，在狮城就声明英语偏好。区域未配置或未知时返回空串
// （调用方据此完全不改写请求头，保持默认关闭语义）。
func openAIEgressRegionAcceptLanguage() string {
	region, _ := openAIEgressRegion.Load().(string)
	switch region {
	case "jp":
		return "ja-JP,ja;q=0.9,en-US;q=0.6,en;q=0.5"
	case "sg":
		return "en-US,en;q=0.9"
	}
	return ""
}

// openAIAcceptLanguageOverride 返回出站 accept-language 的归一值。
// 优先级：gateway.openai_accept_language（管理员显式值） > 出口区域预设 > 空串（关闭）。
// 显式值始终最优先：区域预设只是「一键对齐」的便捷路径，不能覆盖逐项调好的配置。
// 空串语义与改动前一致：normalizeOpenAIAcceptLanguage 完全不触碰请求头。
func (s *OpenAIGatewayService) openAIAcceptLanguageOverride() string {
	if s != nil && s.cfg != nil {
		if explicit := strings.TrimSpace(s.cfg.Gateway.OpenAIAcceptLanguage); explicit != "" {
			return explicit
		}
	}
	return openAIEgressRegionAcceptLanguage()
}

// normalizeOpenAIAcceptLanguage 按配置把出站 accept-language 改写为统一值。
//
// 与请求体时区归一同源的策略：只在客户端已经声明过该头时改写，头缺失时不新增，
// 避免给上游凭空多造一个可识别信号。override 为空（默认配置）时完全不触碰请求头，
// 因此该能力默认关闭，不影响现网行为。
func normalizeOpenAIAcceptLanguage(h http.Header, override string) {
	if h == nil {
		return
	}
	override = strings.TrimSpace(override)
	if override == "" {
		return
	}
	if len(h.Values("accept-language")) == 0 {
		return
	}
	h.Set("accept-language", override)
}

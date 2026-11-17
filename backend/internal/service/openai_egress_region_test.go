package service

import (
	"testing"

	"github.com/MACOS-DO/sub4api/internal/config"
	"github.com/stretchr/testify/require"
)

// 出口区域预设：jp / sg 各自给出与出口一致的 accept-language；未配置或未知区域返回空串，
// 因此「不配置 = 不改写请求头」的默认语义不变。
func TestOpenAIEgressRegionAcceptLanguage(t *testing.T) {
	t.Cleanup(func() { SetOpenAIEgressRegion("") })

	for _, test := range []struct {
		name   string
		region string
		want   string
	}{
		{name: "japan", region: "jp", want: "ja-JP,ja;q=0.9,en-US;q=0.6,en;q=0.5"},
		{name: "japan uppercase", region: "JP", want: "ja-JP,ja;q=0.9,en-US;q=0.6,en;q=0.5"},
		{name: "japan with spaces", region: " jp ", want: "ja-JP,ja;q=0.9,en-US;q=0.6,en;q=0.5"},
		{name: "singapore", region: "sg", want: "en-US,en;q=0.9"},
		{name: "unset", region: "", want: ""},
		{name: "unknown region", region: "us", want: ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			SetOpenAIEgressRegion(test.region)
			require.Equal(t, test.want, openAIEgressRegionAcceptLanguage())
		})
	}
}

// 显式 gateway.openai_accept_language 永远优先于区域预设：区域预设只是「一键对齐」的便捷路径，
// 不能覆盖逐项调好的配置；显式值为空时才落到区域预设，两者都为空则完全不改写。
func TestOpenAIAcceptLanguageOverridePrefersExplicitConfig(t *testing.T) {
	t.Cleanup(func() { SetOpenAIEgressRegion("") })

	explicit := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{OpenAIAcceptLanguage: "de-DE,de;q=0.9"}}}
	inherited := &OpenAIGatewayService{cfg: &config.Config{}}

	SetOpenAIEgressRegion("jp")
	require.Equal(t, "de-DE,de;q=0.9", explicit.openAIAcceptLanguageOverride(), "显式配置必须胜过区域预设")
	require.Equal(t, "ja-JP,ja;q=0.9,en-US;q=0.6,en;q=0.5", inherited.openAIAcceptLanguageOverride(), "未配显式值时继承区域预设")

	SetOpenAIEgressRegion("")
	require.Empty(t, inherited.openAIAcceptLanguageOverride(), "两者都为空时不得改写请求头")

	var nilService *OpenAIGatewayService
	require.Empty(t, nilService.openAIAcceptLanguageOverride())
}

// 进程级默认时区：无账号级 extra 的 OpenAI 账号跟随它；账号 extra 显式值仍然最优先；
// 非法值（不在白名单或无法解析）一律回落到内置默认 Asia/Singapore。
func TestDefaultOpenAIRequestTimezoneResolution(t *testing.T) {
	t.Cleanup(func() { SetDefaultOpenAIRequestTimezone(DefaultOpenAIRequestTimezone) })

	SetDefaultOpenAIRequestTimezone("Asia/Tokyo")
	require.Equal(t, "Asia/Tokyo", DefaultOpenAIRequestTimezoneResolved())
	require.Equal(t, "Asia/Tokyo", (&Account{Platform: PlatformOpenAI}).OpenAIRequestTimezone(), "无 extra 的账号必须跟随进程级默认")
	require.Equal(t, "Asia/Tokyo", (&Account{Platform: PlatformOpenAI, Extra: map[string]any{}}).OpenAIRequestTimezone())
	require.Equal(t, "America/New_York", (&Account{Platform: PlatformOpenAI, Extra: map[string]any{
		openAIRequestTimezoneExtraKey: "America/New_York",
	}}).OpenAIRequestTimezone(), "账号 extra 显式配置最优先")

	SetDefaultOpenAIRequestTimezone("Invalid/Timezone")
	require.Equal(t, DefaultOpenAIRequestTimezone, DefaultOpenAIRequestTimezoneResolved(), "非法时区必须回落内置默认")
	require.Equal(t, DefaultOpenAIRequestTimezone, (&Account{Platform: PlatformOpenAI}).OpenAIRequestTimezone())

	SetDefaultOpenAIRequestTimezone("")
	require.Equal(t, DefaultOpenAIRequestTimezone, DefaultOpenAIRequestTimezoneResolved(), "空值等价于关闭")
	require.Equal(t, DefaultOpenAIRequestTimezone, (&Account{Platform: PlatformOpenAI}).OpenAIRequestTimezone())
}

// 区域预设在未显式配置时区时给出与区域成对的默认时区；显式时区始终优先。
func TestOpenAIDefaultTimezoneForConfig(t *testing.T) {
	for _, test := range []struct {
		name     string
		explicit string
		region   string
		want     string
	}{
		{name: "nothing configured", want: ""},
		{name: "japan region", region: "jp", want: "Asia/Tokyo"},
		{name: "singapore region", region: "SG", want: "Asia/Singapore"},
		{name: "region with spaces", region: " jp ", want: "Asia/Tokyo"},
		{name: "unknown region", region: "us", want: ""},
		{name: "explicit beats region", explicit: "Europe/Berlin", region: "jp", want: "Europe/Berlin"},
		{name: "explicit alone", explicit: "Europe/Berlin", want: "Europe/Berlin"},
	} {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, openAIDefaultTimezoneForConfig(test.explicit, test.region))
		})
	}
}

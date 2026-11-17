package service

import (
	"net/http"
	"testing"

	"github.com/MACOS-DO/sub4api/internal/config"
	"github.com/MACOS-DO/sub4api/internal/pkg/openai"
	"github.com/stretchr/testify/require"
)

// codexFloorTestCanonicalUA 是低于 astra 下限的规范 UA：0.146.0 已满足上游最低门槛
// （codexUpstreamMinVersion=0.144.0），但低于 astra 要求的 0.153.4，
// 正好用来验证版本下限是否真的夹逼了出站身份。
const codexFloorTestCanonicalUA = "codex_cli_rs/0.146.0 (Ubuntu 22.4.0; x86_64) xterm-256color"

// 版本全局下限必须把「UA 版本段 + version 头」一起抬到下限，且 originator 保持配套：
// 只在 version 头动手会拼出自相矛盾的身份（UA 说旧版本、version 说新版本）。
// 不得给本用例加 t.Parallel()：它改写进程级规范 UA 解析器与版本下限。
func TestCodexClientVersionFloorClampsOutboundIdentity(t *testing.T) {
	SetCodexCanonicalUserAgentResolver(func() string { return codexFloorTestCanonicalUA })
	t.Cleanup(func() {
		SetCodexCanonicalUserAgentResolver(nil)
		SetCodexClientVersionFloor("")
	})

	for _, test := range []struct {
		name string
		// floor 是管理员填进 gateway.codex_min_client_version 的原始值。
		floor string
		// wantFloor 是发布后 CodexClientVersionFloor() 的读数：非法值或低于上游门槛的值按关闭处理。
		wantFloor string
		want      string
		wantUA    string
	}{
		{
			name: "floor above canonical raises version", floor: "0.153.4", wantFloor: "0.153.4",
			want: "0.153.4", wantUA: "codex_cli_rs/0.153.4 (Ubuntu 22.4.0; x86_64) xterm-256color",
		},
		{
			name: "floor between upstream minimum and canonical is inert", floor: "0.145.0", wantFloor: "0.145.0",
			want: "0.146.0", wantUA: codexFloorTestCanonicalUA,
		},
		{
			name: "floor below canonical and below upstream minimum is disabled", floor: "0.140.0", wantFloor: "",
			want: "0.146.0", wantUA: codexFloorTestCanonicalUA,
		},
		{
			name: "unparsable floor is disabled", floor: "abc", wantFloor: "",
			want: "0.146.0", wantUA: codexFloorTestCanonicalUA,
		},
		{
			name: "short floor is disabled", floor: "0.1", wantFloor: "",
			want: "0.146.0", wantUA: codexFloorTestCanonicalUA,
		},
		{
			name: "unset floor keeps existing behaviour", floor: "", wantFloor: "",
			want: "0.146.0", wantUA: codexFloorTestCanonicalUA,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			SetCodexClientVersionFloor(test.floor)
			require.Equal(t, test.wantFloor, CodexClientVersionFloor())

			identity := resolveCodexOutboundIdentity("")
			require.Equal(t, test.want, identity.version, "version 头必须夹逼到下限")
			require.Equal(t, test.want, openai.CodexUserAgentVersion(identity.userAgent), "UA 版本段必须与 version 头同源")
			require.Equal(t, test.wantUA, identity.userAgent, "UA 除版本段外必须保持规范身份")
			require.Equal(t, openai.CodexCLIOriginator, identity.originator)
			require.Equal(t, "codex_cli_rs", identity.originator)
		})
	}
}

// 启动告警的判定：只有「集合里确有需要 astra 版本的模型」且「生效版本低于下限」才成立。
// 空集合（未配置打票模型）永远不告警，保证默认配置下不新增任何启动噪声。
func TestAstraModelsBelowVersionFloor(t *testing.T) {
	for _, test := range []struct {
		name    string
		models  []string
		version string
		want    bool
	}{
		{name: "no configured models", version: "0.146.0", want: false},
		{name: "nil model list", models: nil, version: "0.146.0", want: false},
		{name: "models need no astra floor", models: []string{"gpt-5.5-pro", "gpt-5.6-sol"}, version: "0.146.0", want: false},
		{name: "astra below floor", models: []string{"gpt-6-astra"}, version: "0.146.0", want: true},
		{name: "astra exactly at floor", models: []string{"gpt-6-astra"}, version: openAICodexAstraMinVersion, want: false},
		{name: "astra above floor", models: []string{"gpt-6-astra"}, version: "0.155.0", want: false},
		{name: "one astra model is enough", models: []string{"gpt-5.5-pro", "gpt-6-astra"}, version: "0.146.0", want: true},
		{name: "unknown effective version counts as below", models: []string{"gpt-6-astra"}, version: "", want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, astraModelsBelowVersionFloor(test.models, test.version))
		})
	}
}

// 显式配置优先；未配置时由打票模型推导：astra 模型要求更高的客户端版本，
// 否则打票会以陈旧客户端身份被上游拒绝，而业务请求仍停在旧版本，形成新的不一致。
func TestResolveCodexClientVersionFloor(t *testing.T) {
	cases := []struct {
		name       string
		configured string
		enabled    bool
		models     []string
		want       string
	}{
		{name: "explicit wins", enabled: true, configured: "0.160.0", models: []string{"gpt-6-astra"}, want: "0.160.0"},
		{name: "astra model derives floor", enabled: true, configured: "", models: []string{"gpt-6-astra", "gpt-5.6-sol"}, want: openAICodexAstraMinVersion},
		{name: "no astra stays off", enabled: true, configured: "", models: []string{"gpt-5.6-sol"}, want: ""},
		{name: "nil models stays off", enabled: true, configured: "", models: nil, want: ""},
		{name: "ticket feature disabled ignores astra models", enabled: false, configured: "", models: []string{"gpt-6-astra"}, want: ""},
		{name: "prerelease explicit falls back to astra", enabled: true, configured: "0.153.4-beta.1", models: []string{"gpt-6-astra"}, want: openAICodexAstraMinVersion},
		{name: "low explicit falls back to astra", enabled: true, configured: "0.140.0", models: []string{"gpt-6-astra"}, want: openAICodexAstraMinVersion},
		{name: "invalid explicit without astra stays off", enabled: true, configured: "abc", models: []string{"gpt-5.6-sol"}, want: ""},
		{name: "invalid explicit with astra falls back", enabled: true, configured: "abc", models: []string{"gpt-6-astra"}, want: openAICodexAstraMinVersion},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, resolveCodexClientVersionFloor(tc.configured, tc.enabled, tc.models))
		})
	}
}

// 预发布版本号的比较器会截断后缀，不能作为下限：必须判为无效并保持关闭。
func TestCodexClientVersionFloorRejectsPrerelease(t *testing.T) {
	t.Cleanup(func() { SetCodexClientVersionFloor("") })
	SetCodexClientVersionFloor("0.153.4-beta.1")
	require.Equal(t, "", CodexClientVersionFloor())
	SetCodexClientVersionFloor("0.153.4")
	require.Equal(t, "0.153.4", CodexClientVersionFloor())
}

// 装配层接线测试：未显式配置下限时，astra 打票模型必须让进程级下限变为 0.153.4，
// 同时日本区域把默认时区推到 Asia/Tokyo。只测纯函数无法发现"装配时忘了发布"。
func TestApplyCodexOutboundConfigPublishesDerivedFloorAndRegion(t *testing.T) {
	t.Cleanup(func() {
		SetCodexClientVersionFloor("")
		SetOpenAIEgressRegion("")
		SetDefaultOpenAIRequestTimezone("")
	})
	cfg := &config.Config{}
	cfg.Gateway.OpenAICodexTicket.Enabled = true
	cfg.Gateway.OpenAICodexTicket.Models = []string{"gpt-6-astra"}
	cfg.Gateway.OpenAIEgressRegion = "jp"

	applyCodexOutboundConfig(cfg)

	require.Equal(t, openAICodexAstraMinVersion, CodexClientVersionFloor())
	require.Equal(t, "Asia/Tokyo", DefaultOpenAIRequestTimezoneResolved())
	require.Equal(t, "ja-JP,ja;q=0.9,en-US;q=0.6,en;q=0.5", openAIEgressRegionAcceptLanguage())

	applyCodexOutboundConfig(nil)
	require.Equal(t, openAICodexAstraMinVersion, CodexClientVersionFloor(), "nil config must not reset published snapshots")
}

// 关闭强制统一身份时，账号级 floor 必须仍然生效：客户端自带 0.150.0 高于上游门槛
// 但低于 astra 下限，不能原样出站。
func TestPairCodexIdentityHeadersAppliesVersionFloor(t *testing.T) {
	const canonicalUA = "codex_cli_rs/0.150.0 (Ubuntu 22.4.0; x86_64) xterm-256color"
	SetCodexCanonicalUserAgentResolver(func() string { return canonicalUA })
	SetCodexClientVersionFloor(openAICodexAstraMinVersion)
	t.Cleanup(func() {
		SetCodexCanonicalUserAgentResolver(nil)
		SetCodexClientVersionFloor("")
		SetCodexIdentityEnforcementEnabled(true)
	})

	h := make(http.Header)
	h.Set("user-agent", canonicalUA)
	h.Set("originator", "codex_cli_rs")
	h.Set("version", "0.150.0")
	SetCodexIdentityEnforcementEnabled(false)

	pairCodexIdentityHeaders(h)

	require.Equal(t, openAICodexAstraMinVersion, h.Get("version"))
	require.Equal(t, openAICodexAstraMinVersion, openai.CodexUserAgentVersion(h.Get("user-agent")))
	require.Equal(t, "codex_cli_rs", h.Get("originator"))
}

// floor 未配置时，关闭强制统一的兜底必须保持历史形态：不改写客户端 UA，
// 也不给缺失 version 头的请求凭空补一个版本。
func TestPairCodexIdentityHeadersWithoutFloorKeepsClientShape(t *testing.T) {
	const clientUA = "codex_cli_rs/0.160.0 (Debian 12; x86_64) myterm-256color"
	SetCodexCanonicalUserAgentResolver(func() string { return "codex_cli_rs/0.146.0 (Ubuntu 22.4.0; x86_64) xterm-256color" })
	SetCodexClientVersionFloor("")
	t.Cleanup(func() {
		SetCodexCanonicalUserAgentResolver(nil)
		SetCodexClientVersionFloor("")
		SetCodexIdentityEnforcementEnabled(true)
	})

	// 场景一：客户端没有 version 头 —— 不得新增，也不得改 UA。
	h := make(http.Header)
	h.Set("user-agent", clientUA)
	h.Set("originator", "codex_cli_rs")
	pairCodexIdentityHeaders(h)
	require.Empty(t, h.Get("version"))
	require.Equal(t, clientUA, h.Get("user-agent"))

	// 场景二：version 低于上游门槛 —— 只改 version 头，UA 保持客户端原值。
	lowUA := "codex_cli_rs/0.140.0 (Debian 12; x86_64) myterm-256color"
	h2 := make(http.Header)
	h2.Set("user-agent", lowUA)
	h2.Set("originator", "codex_cli_rs")
	h2.Set("version", "0.140.0")
	pairCodexIdentityHeaders(h2)
	require.Equal(t, "0.146.0", h2.Get("version"))
	require.Equal(t, lowUA, h2.Get("user-agent"))
}

// floor 已配置但请求没有 version 头时，按 UA 版本段判定是否夹逼。
func TestPairCodexIdentityHeadersFloorUsesUserAgentVersion(t *testing.T) {
	const clientUA = "codex_cli_rs/0.150.0 (Debian 12; x86_64) myterm-256color"
	SetCodexCanonicalUserAgentResolver(func() string { return "codex_cli_rs/0.146.0 (Ubuntu 22.4.0; x86_64) xterm-256color" })
	SetCodexClientVersionFloor(openAICodexAstraMinVersion)
	t.Cleanup(func() {
		SetCodexCanonicalUserAgentResolver(nil)
		SetCodexClientVersionFloor("")
		SetCodexIdentityEnforcementEnabled(true)
	})

	h := make(http.Header)
	h.Set("user-agent", clientUA)
	h.Set("originator", "codex_cli_rs")
	pairCodexIdentityHeaders(h)

	require.Equal(t, openAICodexAstraMinVersion, h.Get("version"))
	require.Equal(t, openAICodexAstraMinVersion, openai.CodexUserAgentVersion(h.Get("user-agent")))
}

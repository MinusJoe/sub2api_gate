package service

import (
	"net/http"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/MACOS-DO/sub4api/internal/pkg/logger"
	"github.com/MACOS-DO/sub4api/internal/pkg/openai"
	"github.com/google/uuid"
)

// codexUpstreamMinVersion 上游 /backend-api/codex 接受的最低 version 头：
// 若请求携带 version 且低于该值，上游直接 404（issue #3901，2026-07 实测）。
const codexUpstreamMinVersion = "0.144.0"

// codexClientVersionMaxLen 官方版本号均为短 ASCII 串，远低于此上限。
const codexClientVersionMaxLen = 64

// codexClientVersionPattern 允许 0.146.0 与 0.147.0-alpha.4 两类官方形态。
var codexClientVersionPattern = regexp.MustCompile(`^[0-9]+(\.[0-9]+){1,3}(-[0-9A-Za-z.]+)?$`)

// NormalizeCodexClientVersion 校验并归一化 Codex 客户端版本号，非法值返回空串。
// 该值会被拼进出站 User-Agent 与 version 头，必须拒绝任意字节，避免管理员误填或
// 自动同步拿到异常值时把不可控内容透给上游。
func NormalizeCodexClientVersion(version string) string {
	version = strings.TrimSpace(version)
	if version == "" || len(version) > codexClientVersionMaxLen || !codexClientVersionPattern.MatchString(version) {
		return ""
	}
	return version
}

// codexClientVersionFloor 是进程级的 Codex 客户端版本下限（gateway.codex_min_client_version）。
// 线上在跑的 gpt-6*/astra 要求更高的客户端版本，而生效版本只有一个来源（面板设置 / 自动同步）；
// 该下限保证无论版本号来源如何漂移，出站声明的版本都不会低于下限。
// 空串表示关闭，这是零值语义：未配置时出站身份与改动前完全一致。
// 热路径只读，故用 atomic.Value 存字符串快照，不引入锁。
var codexClientVersionFloor = func() *atomic.Value {
	v := &atomic.Value{}
	v.Store("")
	return v
}()

// SetCodexClientVersionFloor 发布进程级 Codex 客户端版本下限。
// 归一失败、或归一值低于上游最低门槛（codexUpstreamMinVersion）的输入一律按关闭处理：
// 低于门槛的下限只会把版本抬到一个仍然 404 的值，等于把「配置填错」伪装成「已修复」。
func SetCodexClientVersionFloor(floor string) {
	raw := strings.TrimSpace(floor)
	if raw == "" {
		codexClientVersionFloor.Store("")
		return
	}
	normalized := NormalizeCodexClientVersion(raw)
	// Prerelease tags are not comparable: the numeric parser drops the suffix, so a
	// floor like 0.153.4-beta.1 would clamp the identity yet keep warning "below the
	// astra floor" forever. Require a plain numeric version instead.
	if normalized == "" || strings.Contains(normalized, "-") || CompareVersions(normalized, codexUpstreamMinVersion) < 0 {
		logger.LegacyPrintf("openai_codex_identity",
			"[warn] gateway.codex_min_client_version %q ignored: require a numeric version >= %s",
			raw, codexUpstreamMinVersion)
		codexClientVersionFloor.Store("")
		return
	}
	codexClientVersionFloor.Store(normalized)
}

// CodexClientVersionFloor 返回当前生效的进程级版本下限，空串表示未设置（关闭）。
// 导出以便测试与诊断观察装配结果。
func CodexClientVersionFloor() string {
	floor, _ := codexClientVersionFloor.Load().(string)
	return floor
}

// codexUserAgentSuffix 是 UA 的 OS/架构/终端段（例如 " (Ubuntu 26.4.0; x86_64) xterm-256color"）。
// 默认取编译期常量；运维可用配置覆盖，以匹配真实客户端的发行版版本。
// 只允许改这一段：客户端名与版本段仍由同一处派生，避免身份三元组自相矛盾。
var codexUserAgentSuffix = func() *atomic.Value {
	v := &atomic.Value{}
	v.Store(codexCLIUserAgentSuffix)
	return v
}()

// SetCodexUserAgentSuffix 发布 UA 的平台/终端后缀。空串恢复默认。
// 拒绝控制字符与明显畸形的值（必须含 "(...; ...)" 与终端段），输错不影响现有身份。
func SetCodexUserAgentSuffix(suffix string) {
	s := strings.TrimSpace(suffix)
	if s == "" {
		codexUserAgentSuffix.Store(codexCLIUserAgentSuffix)
		return
	}
	if len(s) > 160 || strings.ContainsAny(s, "\r\n\x00") || !strings.Contains(s, "(") || !strings.Contains(s, ";") {
		logger.LegacyPrintf("openai_codex_identity", "[warn] ignore invalid gateway.codex_user_agent_suffix %q", suffix)
		return
	}
	if !strings.HasPrefix(s, " ") {
		s = " " + s
	}
	codexUserAgentSuffix.Store(s)
}

func currentCodexUserAgentSuffix() string {
	if v, ok := codexUserAgentSuffix.Load().(string); ok && strings.TrimSpace(v) != "" {
		return v
	}
	return codexCLIUserAgentSuffix
}

// buildCodexCLIUserAgent 按版本号拼出规范 Codex CLI User-Agent。
// UA 形态只在 codexCLIUserAgentSuffix 一处定义，避免多处拼装漂移。
func buildCodexCLIUserAgent(version string) string {
	if version = NormalizeCodexClientVersion(version); version == "" {
		return codexCLIUserAgent
	}
	name := CodexIdentityModeName()
	ua := name + "/" + version + currentCodexUserAgentSuffix()
	if name == "codex_exec" {
		// 实测官方 exec 模式 UA 末尾还会追加一段 (codex_exec; <version>)。
		ua += " (" + name + "; " + version + ")"
	}
	return ua
}

// codexIdentityEnforcement 控制 enforceCodexIdentityHeaders 是否强制统一出站身份，
// 由 gateway.disable_codex_identity_enforcement 在服务构造时取反发布。
// 默认开启：上游会按客户端身份进行风控分层，命中风险身份的请求会拿到
// HTTP 200 + 流内 server_is_overloaded，本次请求即失败；强制统一出口可确保没有
// 请求带着第三方或陈旧身份出站。关闭后退回「仅按最终 UA 配对 originator」的收口语义。
var codexIdentityEnforcement = func() *atomic.Bool {
	v := &atomic.Bool{}
	v.Store(true)
	return v
}()

// SetCodexIdentityEnforcementEnabled 发布 Codex 出站身份强制统一开关。
// enforceCodexIdentityHeaders 是所有出站路径共用的纯函数收口点，无法在热路径注入配置，
// 故由持有配置的服务在构造时发布进程级快照。
func SetCodexIdentityEnforcementEnabled(enabled bool) {
	codexIdentityEnforcement.Store(enabled)
}

// codexCanonicalUserAgentResolver 返回当前生效的规范 Codex User-Agent（后台设置 / 自动同步版本号）。
// 由 SettingService 在装配时注入；解析器内部自带 TTL 缓存，热路径不触库。
type codexCanonicalUserAgentResolver func() string

var (
	codexCanonicalUAMu       sync.RWMutex
	codexCanonicalUAResolver codexCanonicalUserAgentResolver
)

// SetCodexCanonicalUserAgentResolver 注入规范 User-Agent 解析器。
// 未注入或解析结果非法时回退到编译期常量 codexCLIUserAgent。
func SetCodexCanonicalUserAgentResolver(resolver func() string) {
	codexCanonicalUAMu.Lock()
	defer codexCanonicalUAMu.Unlock()
	codexCanonicalUAResolver = resolver
}

// CodexCanonicalUserAgent 返回当前生效的规范 Codex User-Agent。
// 取值走与推理相同的解析链：面板 UA 指纹 + 面板/自动同步版本号 + 编译期兜底。
// 供无账号句柄的出站路径（OAuth 换 Token / 刷新）使用。
func CodexCanonicalUserAgent() string {
	return resolveCodexOutboundIdentity("").userAgent
}

// CodexCanonicalAuthIdentity 返回凭据面（auth.openai.com：换 Token / 刷新 / whoami）
// 出站请求的身份对：规范 User-Agent 与配套 originator，与推理解析链同源。
// 凭据面不发 version 头——真实 Codex 客户端在该面只携带 originator 与 User-Agent
// （codex-rs login/default_client.rs 的 default_headers()），version 门槛
// （issue #3901）只存在于 /backend-api/codex 推理面。
func CodexCanonicalAuthIdentity() (userAgent, originator string) {
	identity := resolveCodexOutboundIdentity("")
	return identity.userAgent, identity.originator
}

// ApplyCodexCanonicalAuthIdentity 为凭据面出站请求写入身份对（不含 version）。
func ApplyCodexCanonicalAuthIdentity(h http.Header) {
	if h == nil {
		return
	}
	userAgent, originator := CodexCanonicalAuthIdentity()
	h.Set("user-agent", userAgent)
	h.Set("originator", originator)
}

// CodexCanonicalClientVersion 返回当前生效的 Codex 客户端版本号。
func CodexCanonicalClientVersion() string {
	return resolveCodexOutboundIdentity("").version
}

// codexCanonicalUserAgent 返回出站规范 User-Agent。
func codexCanonicalUserAgent() string {
	codexCanonicalUAMu.RLock()
	resolver := codexCanonicalUAResolver
	codexCanonicalUAMu.RUnlock()
	if resolver != nil {
		if ua := resolver(); strings.TrimSpace(ua) != "" {
			return ua
		}
	}
	return buildCodexCLIUserAgent(codexCLIVersion)
}

// codexOutboundIdentity 出站身份三元组，三者必须同源自洽：
// originator 与 User-Agent 首段配套（否则上游 404，issue #3901），
// version 等于 User-Agent 的版本段且不低于上游门槛。
type codexOutboundIdentity struct {
	userAgent  string
	originator string
	version    string
}

// resolveCodexOutboundIdentity 由候选 User-Agent 推导自洽的出站身份。
// candidateUA 为空时使用规范 User-Agent；推导不出官方身份时整体回退为规范 CLI 身份。
//
// 候选 UA（面板 / 账号级的管理员显式配置）只贡献客户端名与 OS / 架构 / 终端指纹，
// 其自带的版本段一律用当前生效版本重建：一条填写于某个历史版本的 UA 否则会把出站身份
// 永久钉死在陈旧版本上，绕过版本自动同步，落回上游身份风控更严格的一侧。
// 需要固定版本请填「Codex 客户端版本号」并关闭自动同步。
func resolveCodexOutboundIdentity(candidateUA string) codexOutboundIdentity {
	canonical := codexCanonicalUserAgent()
	if _, _, ok := openai.PairCodexClientIdentity(canonical); !ok {
		canonical = buildCodexCLIUserAgent(codexCLIVersion)
	}
	ua := candidateUA
	if strings.TrimSpace(ua) == "" {
		ua = canonical
	}
	originator, pairedUA, ok := openai.PairCodexClientIdentity(ua)
	if !ok {
		if originator, pairedUA, ok = openai.PairCodexClientIdentity(canonical); !ok {
			originator, pairedUA = CodexIdentityModeName(), buildCodexCLIUserAgent(codexCLIVersion)
		}
	}
	// 旧配置或账号级 UA 可能仍保存 codex-tui。该身份是 sub2api 默认出口曾经
	// 使用的风险指纹，必须与 UA 首段一起归一为 CLI，不能只替换 originator。
	originator, pairedUA, _ = openai.NormalizeCodexClientIdentityToCLI(originator, pairedUA)
	// 生效版本只有一个来源：规范身份（面板版本号 → 自动同步值 → 内置常量，见
	// SettingService.GetOpenAICodexClientVersion）。UA 与 version 头由此同源派生。
	version := codexClientVersionFromUA(canonical)
	// 版本下限夹逼（gateway.codex_min_client_version）：版本号来源单一，但只要它低于下限，
	// astra 请求就会被上游按陈旧客户端拒绝。夹逼必须发生在重建 UA 之前，
	// 这样 UA 版本段、version 头仍然由同一个 version 派生（同源，见上方注释）。
	if floor := CodexClientVersionFloor(); floor != "" && CompareVersions(version, floor) < 0 {
		version = floor
	}
	if rebuilt := openai.SetCodexUserAgentVersion(pairedUA, version); rebuilt != "" {
		pairedUA = rebuilt
	}
	return codexOutboundIdentity{userAgent: pairedUA, originator: originator, version: version}
}

// codexClientVersionFromUA 取 UA 的版本段作为生效版本；
// 非法或低于上游门槛（低于则上游 404，issue #3901）时回退编译期常量。
func codexClientVersionFromUA(ua string) string {
	version := NormalizeCodexClientVersion(openai.CodexUserAgentVersion(ua))
	if version == "" || CompareVersions(version, codexUpstreamMinVersion) < 0 {
		return codexCLIVersion
	}
	return version
}

// ensureCodexIdentityHeaders 补齐 OAuth（ChatGPT 内部接口）出站请求所需的 Codex 身份头。
// 已有 User-Agent 与 version 保持不变，交给紧随其后的 enforceCodexIdentityHeaders 收口。
func ensureCodexIdentityHeaders(h http.Header) {
	if h == nil {
		return
	}
	identity := resolveCodexOutboundIdentity("")
	if strings.TrimSpace(h.Get("user-agent")) == "" {
		h.Set("user-agent", identity.userAgent)
	}
	if strings.TrimSpace(h.Get("originator")) == "" {
		h.Set("originator", identity.originator)
	}
	if strings.TrimSpace(h.Get("version")) == "" {
		h.Set("version", identity.version)
	}
	h.Set("OpenAI-Beta", "responses=experimental")
}

// applyOpenAICodexProbeHeaders 为合成探测请求补齐 Codex 身份和引擎指纹。
func applyOpenAICodexProbeHeaders(h http.Header) {
	if h == nil {
		return
	}
	ensureCodexIdentityHeaders(h)
	h.Set("X-Codex-Window-ID", uuid.NewString())
}

// enforceCodexIdentityHeaders 收口 OAuth（ChatGPT 内部接口）出站请求的客户端身份头。
// 见 enforceCodexIdentityHeadersWithUA；无账号级自定义 User-Agent 时使用本函数。
func enforceCodexIdentityHeaders(h http.Header) {
	enforceCodexIdentityHeadersWithUA(h, "")
}

// enforceCodexIdentityHeadersWithUA 强制统一 OAuth 出站身份：User-Agent / originator / version
// 一律改写为网关的规范身份，客户端自报身份不参与构造。上游会按客户端身份进行风控分层，
// 命中风险身份的请求会拿到 HTTP 200 + 流内 server_is_overloaded；统一出口可确保没有请求带着
// 第三方或陈旧身份出站，也天然满足 originator 与 UA 首段配套的上游校验（issue #3901）。
//
// overrideUA 是账号级自定义 User-Agent：管理员的显式配置仍然生效，但只贡献客户端名与
// OS / 架构 / 终端指纹——版本段与 originator 都由规范身份重建，不允许出现自相矛盾或陈旧的身份。
//
// 强制统一被 gateway.disable_codex_identity_enforcement 关闭时，退回「按最终 User-Agent 配对
// originator + version 门槛校正」的收口语义，供上游策略变动时回滚。
//
// 仅对携带 originator 的请求生效：compat 桥接等非 ChatGPT 内部接口路径会显式删除 originator，
// 不应被补回。需要从缺失身份头恢复的调用方应先调用 ensureCodexIdentityHeaders。
// 必须在所有 User-Agent 改写之后调用。
func enforceCodexIdentityHeadersWithUA(h http.Header, overrideUA string) {
	if h == nil || h.Get("originator") == "" {
		return
	}
	if !codexIdentityEnforcement.Load() {
		pairCodexIdentityHeaders(h)
		return
	}
	identity := resolveCodexOutboundIdentity(overrideUA)
	h.Set("user-agent", identity.userAgent)
	h.Set("originator", identity.originator)
	h.Set("version", identity.version)
}

// pairCodexIdentityHeaders 是关闭强制统一后的兜底收口：保留客户端真实身份，
// 仅保证 originator 与最终 User-Agent 首段配套、version 不低于上游门槛（issue #3901）。
func pairCodexIdentityHeaders(h http.Header) {
	originator, pairedUA, ok := openai.PairCodexClientIdentity(h.Get("user-agent"))
	if !ok {
		identity := resolveCodexOutboundIdentity("")
		originator, pairedUA = identity.originator, identity.userAgent
		h.Set("version", identity.version)
	}
	h.Set("user-agent", pairedUA)
	h.Set("originator", originator)
	if v := strings.TrimSpace(h.Get("version")); v != "" && CompareVersions(v, codexUpstreamMinVersion) < 0 {
		h.Set("version", resolveCodexOutboundIdentity("").version)
	}
	// The process-wide floor applies here too: deployments that keep identity enforcement
	// disabled must not send astra traffic below the configured minimum. When no floor is
	// configured this block is a no-op, so the relaxed mode keeps its historical shape
	// (client User-Agent is never rewritten and a missing version header stays missing).
	floor := CodexClientVersionFloor()
	if floor == "" {
		return
	}
	version := strings.TrimSpace(h.Get("version"))
	if version == "" {
		version = openai.CodexUserAgentVersion(pairedUA)
	}
	if version == "" || CompareVersions(version, floor) >= 0 {
		return
	}
	if rebuilt := openai.SetCodexUserAgentVersion(pairedUA, floor); rebuilt != "" {
		h.Set("user-agent", rebuilt)
	}
	h.Set("version", floor)
}

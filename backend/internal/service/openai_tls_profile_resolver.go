package service

import (
	"sync"
	"sync/atomic"

	"github.com/MACOS-DO/sub4api/internal/pkg/logger"

	"github.com/MACOS-DO/sub4api/internal/pkg/tlsfingerprint"
)

// openAITLSProfileResolverFn 把「账号 → OpenAI 出站 TLS Profile」的解析能力
// 以进程级函数形式发布。
type openAITLSProfileResolverFn func(account *Account) *tlsfingerprint.Profile

var (
	openAITLSProfileResolverMu sync.RWMutex
	openAITLSProfileResolver   openAITLSProfileResolverFn
)

// SetOpenAITLSProfileResolver 发布 OpenAI 出站使用的 TLS Profile 解析器。
// 传 nil 表示撤销注册，出站路径随之回落到 s.httpUpstream.Do（默认关闭状态）。
//
// 为什么用进程级 resolver，而不是给 OpenAIGatewayService 增加一个
// *TLSFingerprintProfileService 字段并由 wire 注入：
//   - OpenAIGatewayService 由 ProvideOpenAIGatewayService 构造，参数列表在
//     wire.go 中声明、在 cmd/server/wire_gen.go 中生成；加参数就必须改生成文件，
//     而生成文件不允许手改，源定义改动又会牵连整个装配契约（本次任务显式禁止）。
//   - 出站收口 doOpenAIUpstream 只拿到 *Account，装配期没有任何注入点。
//   - 与既有 SetCodexIdentityEnforcementEnabled / SetCodexCanonicalUserAgentResolver
//     同一模式：由持有配置的服务在构造时发布进程级快照，热路径只读。
//
// 注册点：NewTLSFingerprintProfileService（wire_gen.go 中该服务先于
// OpenAIGatewayService 构造，注册先于任何出站调用）。
func SetOpenAITLSProfileResolver(resolver func(account *Account) *tlsfingerprint.Profile) {
	openAITLSProfileResolverMu.Lock()
	defer openAITLSProfileResolverMu.Unlock()
	if resolver == nil {
		openAITLSProfileResolver = nil
		return
	}
	openAITLSProfileResolver = openAITLSProfileResolverFn(resolver)
}

// openAITLSProfileFor 返回该账号在 OpenAI 出站路径上应使用的 TLS Profile。
//
// nil 安全：未注册解析器、account 为 nil，或解析结果表示「不启用指纹」时返回 nil，
// 调用方据此维持原有的 s.httpUpstream.Do 行为。解析器本身对未启用开关的账号返回 nil，
// 因此默认（未配置 enable_tls_fingerprint）路径与引入本开关前完全一致。
func openAITLSProfileFor(account *Account) *tlsfingerprint.Profile {
	openAITLSProfileResolverMu.RLock()
	resolver := openAITLSProfileResolver
	openAITLSProfileResolverMu.RUnlock()
	if resolver == nil || account == nil {
		return nil
	}
	profile := resolver(account)
	noteOpenAITLSFingerprintApplied(account, profile)
	return profile
}

// openAITLSFingerprintApplied ??"???????"???,??????????
var (
	openAITLSFingerprintAppliedAccounts atomic.Int64
	openAITLSFingerprintAppliedSeen     sync.Map
)

// OpenAITLSFingerprintAppliedAccountCount ???????????????(???????)?
// ??:?????/?????"???????????"?
func OpenAITLSFingerprintAppliedAccountCount() int64 {
	return openAITLSFingerprintAppliedAccounts.Load()
}

// noteOpenAITLSFingerprintApplied ???????????,??????
// account ?????(profile == nil)??????,????????
func noteOpenAITLSFingerprintApplied(account *Account, profile *tlsfingerprint.Profile) {
	if account == nil || profile == nil {
		return
	}
	if _, loaded := openAITLSFingerprintAppliedSeen.LoadOrStore(account.ID, struct{}{}); loaded {
		return
	}
	openAITLSFingerprintAppliedAccounts.Add(1)
	logger.LegacyPrintf("service.openai_tlsfp",
		"[info] openai egress TLS fingerprint applied account=%d profile=%s", account.ID, tlsfingerprint.ProfileKeyForDebug(profile))
}

// openAITLSFingerprintPluginWarned 保证同一账号只告警一次，避免刷屏。
var openAITLSFingerprintPluginWarned sync.Map

// warnOpenAITLSFingerprintBypassedByPlugin 在插件接管 OpenAI OAuth 出站时提示指纹未生效。
//
// 插件接管意味着请求不经过 httpUpstream，enable_tls_fingerprint 在该路径上没有作用；
// 若不显式告警，运维会误以为开关已生效（这是评审指出的 M1）。
func warnOpenAITLSFingerprintBypassedByPlugin(account *Account) {
	if account == nil || !account.IsTLSFingerprintEnabled() {
		return
	}
	if _, loaded := openAITLSFingerprintPluginWarned.LoadOrStore(account.ID, struct{}{}); loaded {
		return
	}
	logger.LegacyPrintf("service.openai_tlsfp",
		"[warn] account %d has enable_tls_fingerprint=true but the OpenAI OAuth plugin handles egress; TLS fingerprint is NOT applied on this path",
		account.ID)
}

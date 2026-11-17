package service

import (
	"context"
	"strings"
	"time"

	"github.com/MACOS-DO/sub4api/internal/config"
	"github.com/MACOS-DO/sub4api/internal/pkg/logger"
)

// Construct the harvester only after history and proxy settings are available.
func ProvideOpenAIGatewayService(
	accountRepo AccountRepository,
	usageLogRepo UsageLogRepository,
	usageBillingRepo UsageBillingRepository,
	userRepo UserRepository,
	userSubRepo UserSubscriptionRepository,
	userGroupRateRepo UserGroupRateRepository,
	cache GatewayCache,
	cfg *config.Config,
	schedulerSnapshot *SchedulerSnapshotService,
	concurrencyService *ConcurrencyService,
	billingService *BillingService,
	rateLimitService *RateLimitService,
	billingCacheService *BillingCacheService,
	httpUpstream HTTPUpstream,
	deferredService *DeferredService,
	openAITokenProvider *OpenAITokenProvider,
	grokTokenProvider *GrokTokenProvider,
	resolver *ModelPricingResolver,
	channelService *ChannelService,
	balanceNotifyService *BalanceNotifyService,
	settingService *SettingService,
	userPlatformQuotaRepo UserPlatformQuotaRepository,
	history CodexTicketAttemptRepository,
) *OpenAIGatewayService {
	s := NewOpenAIGatewayService(accountRepo, usageLogRepo, usageBillingRepo, userRepo,
		userSubRepo, userGroupRateRepo, cache, cfg, schedulerSnapshot, concurrencyService,
		billingService, rateLimitService, billingCacheService, httpUpstream, deferredService,
		openAITokenProvider, grokTokenProvider, resolver, channelService, balanceNotifyService,
		settingService, userPlatformQuotaRepo)
	s.SetCodexTicketHistory(history)
	// 出站身份与出口区域：这些值由热路径上的纯函数读取（那一层拿不到配置），
	// 故在装配时发布为进程级快照。三者在默认空配置下都不改变任何出站行为，
	// 「不配置 = 不变」由各自的空串/内置默认兜底保证。
	if cfg != nil {
		// 版本下限先发布：下面的一致性告警要按发布后的生效版本判定。
		// 未配置下限时由打票模型推导：astra 模型要求更高的客户端版本，打票若停在旧版本会被
		// 上游当作陈旧客户端拒绝；自动夹逼保证打票与业务在全链路只声明一个版本。
		applyCodexOutboundConfig(cfg)
	}
	if settingService != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = settingService.LoadModelTraceBank(ctx)
		cancel()
	}
	s.StartOpenAICodexTicketHarvester()
	go s.LogOpenAIEgressSharing(context.Background())
	return s
}

func (s *OpenAIGatewayService) SetCodexTicketHistory(history CodexTicketAttemptRepository) {
	s.openaiCodexTicketHistory = history
	s.openaiCodexTicketLifecycle, _ = history.(CodexTicketLifecycleRepository)
}

// openAIDefaultTimezoneForConfig 解析进程级默认时区：显式配置优先；未配置时由出口区域
// 给出各自默认（jp→Asia/Tokyo，sg→Asia/Singapore），与 accept-language 预设成对；
// 都没有则返回空串，由 SetDefaultOpenAIRequestTimezone 回落到内置默认 Asia/Singapore。
func openAIDefaultTimezoneForConfig(explicit, region string) string {
	if tz := strings.TrimSpace(explicit); tz != "" {
		return tz
	}
	switch strings.ToLower(strings.TrimSpace(region)) {
	case "jp":
		return "Asia/Tokyo"
	case "sg":
		return "Asia/Singapore"
	}
	return ""
}

// astraModelsBelowVersionFloor 判定给定模型集合里是否存在「需要 astra 版本下限、
// 而生效版本仍低于下限」的模型。version 为空表示无法确定生效版本，按低于下限处理。
// 纯函数，便于对启动告警的判定条件做单元测试。
func astraModelsBelowVersionFloor(models []string, version string) bool {
	current := strings.TrimSpace(version)
	for _, model := range models {
		if !needsOpenAICodexAstraVersion(model) {
			continue
		}
		if current == "" || CompareVersions(current, openAICodexAstraMinVersion) < 0 {
			return true
		}
	}
	return false
}

// warnIfCodexAstraModelsNeedHigherVersion 在启动时检查打票模型与生效客户端版本是否自洽：
// 需要 astra 版本的模型配上更低的生效版本，会在上游按陈旧客户端被拒或降载，
// 因此打一条 Warn 指向两个可修的地方（gateway.codex_min_client_version / 前端 Codex 客户端版本）。
// 只告警，不修改任何出站身份。
func warnIfCodexAstraModelsNeedHigherVersion(models []string) {
	current := CodexCanonicalClientVersion()
	if !astraModelsBelowVersionFloor(models, current) {
		return
	}
	logger.LegacyPrintf("openai_codex_ticket",
		"[warn] models %v need Codex client version >= %s but the effective version is %q; set gateway.codex_min_client_version or raise the Codex client version setting in the panel",
		models, openAICodexAstraMinVersion, current)
}

// resolveCodexClientVersionFloor returns the process-wide client version floor.
// An explicit configuration wins; otherwise the floor is derived from the configured
// ticket models so astra traffic never runs below its minimum. Empty means "off".
func resolveCodexClientVersionFloor(configured string, ticketEnabled bool, models []string) string {
	if floor := strings.TrimSpace(configured); floor != "" {
		// Only accept a floor that the setter would keep; otherwise fall back to the
		// derived floor so a typo cannot silently disable the astra requirement.
		normalized := NormalizeCodexClientVersion(floor)
		if normalized != "" && !strings.Contains(normalized, "-") && CompareVersions(normalized, codexUpstreamMinVersion) >= 0 {
			return normalized
		}
		logger.LegacyPrintf("openai_codex_ticket",
			"[warn] gateway.codex_min_client_version %q is not a usable numeric version; falling back to the derived floor",
			floor)
	}
	// 只有打票功能真正开启时，才需要为了打票抬高全局版本下限。
	// 关闭打票时 models 只是遗留配置，不应影响任何出站身份。
	if ticketEnabled && ticketModelsNeedAstra(models) {
		return openAICodexAstraMinVersion
	}
	return ""
}

// ticketModelsNeedAstra reports whether any configured ticket model requires the astra floor.
func ticketModelsNeedAstra(models []string) bool {
	for _, model := range models {
		if needsOpenAICodexAstraVersion(model) {
			return true
		}
	}
	return false
}

// applyCodexOutboundConfig publishes the process-wide outbound identity snapshots
// (client version floor, egress region, default request timezone) plus the two
// behavior gates (codex_cli_only default, OpenAI per-account scheduling
// concurrency cap), and runs the consistency warning. It is separate from the
// provider so the wiring can be tested without constructing the whole gateway
// service.
//
// The snapshots are read by hot-path pure functions and are published only at
// construction time: changing these settings requires a restart.
func applyCodexOutboundConfig(cfg *config.Config) {
	if cfg == nil {
		return
	}
	// The floor goes first: the warning below evaluates the post-publish version.
	// Without an explicit floor it is derived from the ticket models so astra traffic
	// never runs below its minimum.
	SetCodexClientVersionFloor(resolveCodexClientVersionFloor(cfg.Gateway.CodexMinClientVersion, cfg.Gateway.OpenAICodexTicket.Enabled, cfg.Gateway.OpenAICodexTicket.Models))
	SetOpenAIEgressRegion(cfg.Gateway.OpenAIEgressRegion)
	SetDefaultOpenAIRequestTimezone(openAIDefaultTimezoneForConfig(cfg.Gateway.OpenAIDefaultTimezone, cfg.Gateway.OpenAIEgressRegion))
	// 两个行为门的进程级快照：默认 false / 0 时与改动前完全一致（不配置 = 不变）。
	SetCodexCLIOnlyDefault(cfg.Gateway.OpenAICodexRequireOfficialClient)
	SetCodexUserAgentSuffix(cfg.Gateway.CodexUserAgentSuffix)
	SetCodexIdentityMode(cfg.Gateway.CodexIdentityMode)
	SetOpenAITLSFingerprintDefault(cfg.Gateway.OpenAITLSFingerprintDefault)
	SetOpenAIPacingMaxConcurrency(cfg.Gateway.OpenAIPacingMaxAccountConcurrency)
	SetOpenAIPacingRPM(cfg.Gateway.OpenAIPacingRPM)
	if cfg.Gateway.OpenAICodexTicket.Enabled {
		warnIfCodexAstraModelsNeedHigherVersion(cfg.Gateway.OpenAICodexTicket.Models)
	}
}

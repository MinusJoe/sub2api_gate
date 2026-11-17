package service

import (
	"context"
	"fmt"
	"sort"

	"github.com/MACOS-DO/sub4api/internal/pkg/logger"
	"go.uber.org/zap"
)

// egressSharingGroup 表示一组共用同一出口的账号。
// Key 形如 "proxy:12" 或 "direct"（未配置代理 = 共用网关自身出口 IP）。
type egressSharingGroup struct {
	Key        string
	AccountIDs []int64
}

// analyzeEgressSharing 按出口对账号分组。上游判定"中转"时，一个出口承载多个互不相关的
// 账号是最强的信号之一；这里只做可见性，不改变任何调度行为。
func analyzeEgressSharing(accounts []Account) []egressSharingGroup {
	byKey := make(map[string][]int64)
	for i := range accounts {
		a := &accounts[i]
		key := "direct"
		if a.ProxyID != nil && *a.ProxyID > 0 {
			key = fmt.Sprintf("proxy:%d", *a.ProxyID)
		}
		byKey[key] = append(byKey[key], a.ID)
	}
	groups := make([]egressSharingGroup, 0, len(byKey))
	for key, ids := range byKey {
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		groups = append(groups, egressSharingGroup{Key: key, AccountIDs: ids})
	}
	sort.Slice(groups, func(i, j int) bool {
		if len(groups[i].AccountIDs) != len(groups[j].AccountIDs) {
			return len(groups[i].AccountIDs) > len(groups[j].AccountIDs)
		}
		return groups[i].Key < groups[j].Key
	})
	return groups
}

// LogOpenAIEgressSharing 在启动时输出"出口复用"报告。
// 只记录日志，不阻断、不改调度：真正修复需要更多独立出口（住宅/移动 IP），代码无法代劳。
func (s *OpenAIGatewayService) LogOpenAIEgressSharing(ctx context.Context) {
	if s == nil || s.accountRepo == nil {
		return
	}
	accounts, err := s.accountRepo.ListByPlatform(ctx, PlatformOpenAI)
	if err != nil {
		logger.L().Warn("openai egress sharing scan failed", zap.Error(err))
		return
	}
	groups := analyzeEgressSharing(accounts)
	shared := 0
	for _, g := range groups {
		if len(g.AccountIDs) <= 1 {
			continue
		}
		shared++
		label := g.Key
		if label == "direct" {
			label = "direct（未配代理，共用网关出口 IP）"
		}
		logger.L().Warn("openai egress shared by multiple accounts",
			zap.String("egress", label),
			zap.Int("accounts", len(g.AccountIDs)),
			zap.Int64s("account_ids", g.AccountIDs))
	}
	if shared == 0 {
		logger.L().Info("openai egress sharing scan: every account has its own egress",
			zap.Int("accounts", len(accounts)))
	}
}

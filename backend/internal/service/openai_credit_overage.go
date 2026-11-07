package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math/big"
	"strings"
	"time"
)

// OpenAI 账号的 5h / 7d 用量窗口用尽后，网关默认把它从调度里剔除
// （见 shouldAutoPauseOpenAIAccountByQuota 与 openai_quota_auto_reset.go）。
// 但 /wham/usage 里还有一份可消费余额 credits（has_credits / unlimited / balance），
// 部分账号在窗口用满后上游仍会按余额计费继续服务。此前这份余额只用于展示，
// 调度判定完全不看它，于是出现"能查到积分/余额，但永远用不出去"。
//
// OpenAICreditOverageExtraKey 是该能力的账号级开关：打开且余额快照仍然新鲜、显示
// 有余额时，额度型自动暂停一律放行，请求照常发往上游，是否扣余额由上游决定
// （上游拒绝会返回 429，网关照常冷却并切号）。
//
// 免费的重置卡优先级始终高于余额：命中用卡阈值时仍会异步通知后台用卡，只是
// 不再因此把账号从调度中剔除。
const OpenAICreditOverageExtraKey = "openai_credit_overage"

// openAICreditOverageSnapshotTTL 是余额快照的可信时长：快照由额度查询写入
// account.Extra[openaiQuotaCreditsKey]，超过该时长即视为不可信，回到默认暂停行为
// （fail-closed：宁可不放行，也不要凭过期余额把账号一直放出去）。
const openAICreditOverageSnapshotTTL = 2 * time.Hour

// openAICreditOverageState 是一次余额放行判定的结果。Reason 只用于日志与测试断言。
type openAICreditOverageState struct {
	Allowed bool
	Reason  string
}

const (
	openAICreditOverageReasonDisabled         = "disabled"
	openAICreditOverageReasonSnapshotMissing  = "snapshot_missing"
	openAICreditOverageReasonSnapshotStale    = "snapshot_stale"
	openAICreditOverageReasonNoCredits        = "no_credits"
	openAICreditOverageReasonBalanceExhausted = "balance_exhausted"
	openAICreditOverageReasonUnlimited        = "unlimited"
	openAICreditOverageReasonBalance          = "balance"
)

// resolveOpenAICreditOverage 判定该账号当前能否用余额承担超量请求。
// 只有账号显式开启开关、且余额快照新鲜、显示仍有可用余额时才返回 Allowed。
func resolveOpenAICreditOverage(account *Account, now time.Time) openAICreditOverageState {
	state := openAICreditOverageState{Reason: openAICreditOverageReasonDisabled}
	if account == nil || !account.IsOpenAI() || len(account.Extra) == 0 {
		return state
	}
	if !resolveAccountExtraBool(account.Extra, OpenAICreditOverageExtraKey) {
		return state
	}
	snapshot, ok := openAICreditSnapshotFromExtra(account.Extra)
	if !ok {
		state.Reason = openAICreditOverageReasonSnapshotMissing
		return state
	}
	if snapshot.FetchedAt <= 0 {
		state.Reason = openAICreditOverageReasonSnapshotStale
		return state
	}
	fetchedAt := time.Unix(snapshot.FetchedAt, 0)
	age := now.Sub(fetchedAt)
	if age < 0 || age >= openAICreditOverageSnapshotTTL {
		state.Reason = openAICreditOverageReasonSnapshotStale
		return state
	}
	if snapshot.Credits.Unlimited {
		state.Allowed = true
		state.Reason = openAICreditOverageReasonUnlimited
		return state
	}
	if !snapshot.Credits.HasCredits {
		state.Reason = openAICreditOverageReasonNoCredits
		return state
	}
	// 余额是不可解析的字符串时不做本地裁决（上游才是计费权威）；能解析且 <= 0 才算耗尽。
	if snapshot.Credits.Balance != nil {
		if balance, ok := parseOpenAICreditBalance(*snapshot.Credits.Balance); ok && balance <= 0 {
			state.Reason = openAICreditOverageReasonBalanceExhausted
			return state
		}
	}
	state.Allowed = true
	state.Reason = openAICreditOverageReasonBalance
	return state
}

// MergeOpenAICreditsSnapshotExtra keeps the server-managed balance from the
// current row, never a display snapshot echoed by an account edit.
func MergeOpenAICreditsSnapshotExtra(extra, current map[string]any) map[string]any {
	result := maps.Clone(extra)
	delete(result, openaiQuotaCreditsKey)
	delete(result, OpenAICreditsRevisionExtraKey)
	if revision, ok := current[OpenAICreditsRevisionExtraKey]; ok {
		if result == nil {
			result = make(map[string]any)
		}
		result[OpenAICreditsRevisionExtraKey] = revision
	}
	if value, ok := current[openaiQuotaCreditsKey]; ok {
		if result == nil {
			result = make(map[string]any)
		}
		result[openaiQuotaCreditsKey] = value
	}
	return result
}

const OpenAICreditsRevisionExtraKey = "codex_credits_revision"

var ErrOpenAICreditsObservationConflict = errors.New("openai credits observation is no longer current")

// OpenAICreditsObservation is captured before the upstream read, never decoded
// from a management request. Repositories must compare it under database locks.
type OpenAICreditsObservation struct {
	Target *Account
	Source *Account
}

type OpenAICreditsSnapshotRepository interface {
	UpdateOpenAICreditsSnapshot(context.Context, OpenAICreditsObservation, map[string]any) error
}

func OpenAICreditsRevision(extra map[string]any) (string, error) {
	raw, exists := extra[OpenAICreditsRevisionExtraKey]
	if !exists {
		return "0", nil
	}
	value, ok := raw.(string)
	if !ok || value == "" || (len(value) > 1 && value[0] == '0') {
		return "", ErrOpenAICreditsObservationConflict
	}
	for _, c := range value {
		if c < '0' || c > '9' {
			return "", ErrOpenAICreditsObservationConflict
		}
	}
	return value, nil
}

func NextOpenAICreditsRevision(extra map[string]any) (string, error) {
	revision, err := OpenAICreditsRevision(extra)
	if err != nil {
		return "", err
	}
	n, _ := new(big.Int).SetString(revision, 10)
	return n.Add(n, big.NewInt(1)).String(), nil
}

// Stable user/workspace identifiers survive ordinary OAuth token rotation.
// Without a stable user identity, credential changes conservatively invalidate.
func OpenAICreditsIdentity(account *Account) string {
	if account == nil {
		return ""
	}
	workspace := strings.TrimSpace(account.GetCredential("chatgpt_account_id"))
	if workspace == "" {
		workspace = strings.TrimSpace(account.GetCredential("organization_id"))
	}
	user := strings.TrimSpace(account.GetCredential("chatgpt_user_id"))
	identity := map[string]any{"platform": account.Platform, "type": account.Type, "workspace": workspace, "parent": account.ParentAccountID, "dimension": account.QuotaDimensionOrDefault(), "auth_mode": strings.ToLower(strings.TrimSpace(account.GetCredential("auth_mode")))}
	if account.IsOpenAIAgentIdentity() {
		identity["agent_private_key"] = account.GetCredential("agent_private_key")
		identity["agent_runtime_id"] = account.GetCredential("agent_runtime_id")
	} else if workspace != "" && user != "" {
		identity["user"] = user
	} else {
		identity["credentials"] = account.Credentials
	}
	encoded, _ := json.Marshal(identity)
	return fmt.Sprintf("%x", sha256.Sum256(encoded))
}

func cloneOpenAICreditsAccount(account *Account) *Account {
	if account == nil {
		return nil
	}
	copy := *account
	copy.Credentials = maps.Clone(account.Credentials)
	copy.Extra = maps.Clone(account.Extra)
	if account.ParentAccountID != nil {
		id := *account.ParentAccountID
		copy.ParentAccountID = &id
	}
	return &copy
}

func persistOpenAICreditsObservation(ctx context.Context, repo AccountRepository, accountID int64, usage *OpenAIQuotaUsage, updates map[string]any) error {
	if usage == nil || usage.creditsObservation == nil || usage.creditsObservation.Target == nil || usage.creditsObservation.Source == nil || usage.creditsObservation.Target.ID != accountID {
		return ErrOpenAICreditsObservationConflict
	}
	writer, ok := repo.(OpenAICreditsSnapshotRepository)
	if !ok {
		return fmt.Errorf("openai credits repository does not support protected writes")
	}
	updates = maps.Clone(updates)
	if updates == nil {
		updates = map[string]any{}
	}
	delete(updates, OpenAICreditsRevisionExtraKey)
	snapshot := openAICreditsSnapshot{Credits: usage.Credits, FetchedAt: usage.FetchedAt}
	if source := usage.creditsObservation.Source; usage.creditsObservation.Target.ID != source.ID {
		snapshot.SourceAccountID = source.ID
		snapshot.SourceIdentity = OpenAICreditsIdentity(source)
		snapshot.SourceRevision, _ = OpenAICreditsRevision(source.Extra)
	}
	updates[openaiQuotaCreditsKey] = snapshot
	return writer.UpdateOpenAICreditsSnapshot(ctx, *usage.creditsObservation, updates)
}

func openAICreditOverageRefreshEnabled(account *Account) bool {
	return isOpenAIAutoResetCreditAccount(account) && resolveAccountExtraBool(account.Extra, OpenAICreditOverageExtraKey)
}

// Refresh unknown and exhausted balances on the same cadence as usable balances.
// credits:null is still a successful observation with its own fetched_at.
func openAICreditSnapshotNeedsRefresh(extra map[string]any, now time.Time) bool {
	var fetchedAt int64
	switch snapshot := extra[openaiQuotaCreditsKey].(type) {
	case openAICreditsSnapshot:
		fetchedAt = snapshot.FetchedAt
	case map[string]any:
		value, _ := resolveAccountExtraNumber(snapshot, "fetched_at")
		fetchedAt = int64(value)
	default:
		encoded, err := json.Marshal(snapshot)
		if err == nil {
			var decoded openAICreditsSnapshot
			if json.Unmarshal(encoded, &decoded) == nil {
				fetchedAt = decoded.FetchedAt
			}
		}
	}
	age := now.Sub(time.Unix(fetchedAt, 0))
	return fetchedAt <= 0 || age < 0 || age >= openAIAutoResetSnapshotTTL
}

// Balance-only refreshes never enter the reset-credit consumption state machine.
func (s *OpenAIQuotaAutoResetService) refreshOverageCredits(ctx context.Context, account *Account) error {
	now := time.Now()
	if !openAICreditSnapshotNeedsRefresh(account.Extra, now) {
		return nil
	}
	if value, loaded := s.creditRefreshRetryAt.Load(account.ID); loaded {
		if retryAt, ok := value.(time.Time); ok && now.Before(retryAt) {
			return nil
		}
	}
	s.creditRefreshRetryAt.Store(account.ID, now.Add(openAIAutoResetQueryFailureRetryAfter))
	usage, err := s.quota.QueryUsage(ctx, account.ID)
	if err != nil {
		return err
	}
	if usage == nil {
		return fmt.Errorf("openai credit refresh returned empty usage")
	}
	if err := persistOpenAICreditsObservation(ctx, s.accountRepo, account.ID, usage, nil); err != nil {
		return err
	}
	s.creditRefreshRetryAt.Delete(account.ID)
	return nil
}

func parseOpenAICreditBalance(raw string) (float64, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0, false
	}
	var parsed float64
	if err := json.Unmarshal([]byte(trimmed), &parsed); err == nil {
		return parsed, true
	}
	return 0, false
}

// openAICreditSnapshotFromExtra 读取 account.Extra 里的余额快照。热路径上 Extra 已经是
// 解码后的 map，优先走无分配的字段读取；其余形态（json.RawMessage、字符串等）退回
// 一次 JSON 往返。
func openAICreditSnapshotFromExtra(extra map[string]any) (openAICreditsSnapshot, bool) {
	if len(extra) == 0 {
		return openAICreditsSnapshot{}, false
	}
	raw, ok := extra[openaiQuotaCreditsKey]
	if !ok || raw == nil {
		return openAICreditsSnapshot{}, false
	}
	switch value := raw.(type) {
	case openAICreditsSnapshot:
		if value.Credits == nil {
			return openAICreditsSnapshot{}, false
		}
		return value, true
	case map[string]any:
		if snapshot, ok := openAICreditSnapshotFromMap(value); ok {
			return snapshot, true
		}
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return openAICreditsSnapshot{}, false
	}
	var snapshot openAICreditsSnapshot
	if err := json.Unmarshal(encoded, &snapshot); err != nil || snapshot.Credits == nil {
		return openAICreditsSnapshot{}, false
	}
	return snapshot, true
}

func openAICreditSnapshotFromMap(raw map[string]any) (openAICreditsSnapshot, bool) {
	if raw == nil {
		return openAICreditsSnapshot{}, false
	}
	creditsRaw, ok := raw["credits"].(map[string]any)
	if !ok || creditsRaw == nil {
		return openAICreditsSnapshot{}, false
	}
	credits := &OpenAICredits{
		HasCredits: resolveAccountExtraBool(creditsRaw, "has_credits"),
		Unlimited:  resolveAccountExtraBool(creditsRaw, "unlimited"),
	}
	if balance, ok := creditsRaw["balance"].(string); ok {
		value := balance
		credits.Balance = &value
	}
	fetchedAt, _ := resolveAccountExtraNumber(raw, "fetched_at")
	sourceID, _ := resolveAccountExtraNumber(raw, "source_account_id")
	sourceIdentity, _ := raw["source_identity"].(string)
	sourceRevision, _ := raw["source_revision"].(string)
	return openAICreditsSnapshot{Credits: credits, FetchedAt: int64(fetchedAt), SourceAccountID: int64(sourceID), SourceIdentity: sourceIdentity, SourceRevision: sourceRevision}, true
}

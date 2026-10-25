// Package turnstate 维护 (账号, 会话线程) → x-codex-turn-state 的映射，
// 复刻官方客户端在 HTTP 回退通道上的行为：从响应头接住票、同回合回带、
// 窗口过期后不再回带（官方 per-turn 边界的外部近似，见 README 的取舍说明）。
package turnstate

import (
	"strconv"
	"sync"
	"time"
)

// Key 生成存储键。thread 为请求上的会话线程标识（x-client-request-id /
// session-id）。线程标识缺失时返回空键：票的生命周期绑定会话线程，无法归属
// 线程的票绝不捕获/回带（fail-closed）——退化为纯账号键会把 A 会话的
// turn-state 注入 B 会话请求，账号级跨线程共享是官方协议不允许的形态。
func Key(accountID int64, thread string) string {
	if thread == "" {
		return ""
	}
	return strconv.FormatInt(accountID, 10) + "\x00" + thread
}

type entry struct {
	ticket     string
	capturedAt time.Time
}

// Store 是带 TTL 的并发安全票存储。
type Store struct {
	mu  sync.RWMutex
	ttl time.Duration
	m   map[string]entry
}

// NewStore 创建存储；ttl 必须 > 0。
func NewStore(ttl time.Duration) *Store {
	if ttl <= 0 {
		panic("turnstate: ttl must be positive")
	}
	return &Store{ttl: ttl, m: make(map[string]entry)}
}

// ReplaceTTL 原子调整 TTL（配置热更新）。
func (s *Store) ReplaceTTL(ttl time.Duration) {
	if ttl <= 0 {
		return
	}
	s.mu.Lock()
	s.ttl = ttl
	s.mu.Unlock()
}

// pruneThreshold 触发顺带清理的条目数阈值。键空间按 (账号, 线程) 只增不减，
// 又无后台清扫协程与 Prune 调用方，故在写入路径上自愈：条目数达到阈值即在
// 持锁状态下整扫一次过期条目（O(n)，n≥4096，摊销到捕获频率——每上游响应
// 一次——可忽略；条目全未过期时为空扫，此后每次捕获仍会重扫直到有条目过期）。
const pruneThreshold = 4096

// Capture 记录上游下发的票（覆盖同键旧值，模拟服务端换票后的最新值生效）。
// 空键（无线程标识，见 Key）与空票一律不存储。条目数达到 pruneThreshold 时
// 顺带清理过期条目（策略见 pruneThreshold 注释）。
func (s *Store) Capture(key, ticket string) {
	if key == "" || ticket == "" {
		return
	}
	s.mu.Lock()
	s.m[key] = entry{ticket: ticket, capturedAt: time.Now()}
	if len(s.m) >= pruneThreshold {
		s.pruneLocked()
	}
	s.mu.Unlock()
}

// Replay 返回未过期的票；空键、过期或缺失返回 ("", false)。
func (s *Store) Replay(key string) (string, bool) {
	if key == "" {
		return "", false
	}
	s.mu.RLock()
	e, ok := s.m[key]
	ttl := s.ttl
	s.mu.RUnlock()
	if !ok || time.Since(e.capturedAt) >= ttl {
		return "", false
	}
	return e.ticket, true
}

// Len 返回当前条目数（观测用）。
func (s *Store) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.m)
}

// Prune 清除过期条目，返回清除数。
func (s *Store) Prune() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pruneLocked()
}

// pruneLocked 整扫过期条目并删除，返回清除数；调用方须持写锁。
func (s *Store) pruneLocked() int {
	removed := 0
	now := time.Now()
	for k, e := range s.m {
		if now.Sub(e.capturedAt) >= s.ttl {
			delete(s.m, k)
			removed++
		}
	}
	return removed
}

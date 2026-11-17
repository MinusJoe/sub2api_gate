package service

import (
	"sync"
	"time"
)

// openAIAccountRPMLimiter 是"每账号每分钟请求数"的软限流器。
//
// 目的不是拒绝请求，而是让调度层在候选里跳过已经打满 RPM 的账号，
// 把负载摊到其它账号（轮换），避免"同一账号高频突发"这种明显的机器特征。
// 默认 0 = 关闭，行为与引入前完全一致。
type openAIAccountRPMLimiter struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	now    func() time.Time
	hits   map[int64][]time.Time
}

var openAIAccountRPM = newOpenAIAccountRPMLimiter(0)

func newOpenAIAccountRPMLimiter(limit int) *openAIAccountRPMLimiter {
	return &openAIAccountRPMLimiter{
		limit:  limit,
		window: time.Minute,
		now:    time.Now,
		hits:   make(map[int64][]time.Time),
	}
}

// SetOpenAIPacingRPM 发布进程级 RPM 上限（0 或负数表示关闭）。
func SetOpenAIPacingRPM(limit int) {
	openAIAccountRPM.setLimit(limit)
}

// OpenAIPacingRPM 返回当前生效上限，便于观测与测试。
func OpenAIPacingRPM() int {
	return openAIAccountRPM.currentLimit()
}

func (l *openAIAccountRPMLimiter) setLimit(limit int) {
	if l == nil {
		return
	}
	if limit < 0 {
		limit = 0
	}
	l.mu.Lock()
	l.limit = limit
	if limit == 0 {
		l.hits = make(map[int64][]time.Time)
	}
	l.mu.Unlock()
}

func (l *openAIAccountRPMLimiter) currentLimit() int {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.limit
}

// allow 只做检查，不计数：调度阶段可能探测多个候选，计数应发生在真正出站时。
func (l *openAIAccountRPMLimiter) allow(accountID int64) bool {
	if l == nil {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.limit <= 0 {
		return true
	}
	return len(l.pruneLocked(accountID)) < l.limit
}

// consume 记录一次真实出站；超限调用仍会记录（由调用方决定是否跳过）。
func (l *openAIAccountRPMLimiter) consume(accountID int64) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.limit <= 0 {
		return
	}
	now := l.now()
	kept := l.pruneLocked(accountID)
	l.hits[accountID] = append(kept, now)
}

// pruneLocked 丢弃窗口外的记录；调用方必须持有锁。
func (l *openAIAccountRPMLimiter) pruneLocked(accountID int64) []time.Time {
	now := l.now()
	cutoff := now.Add(-l.window)
	list := l.hits[accountID]
	idx := 0
	for idx < len(list) && !list[idx].After(cutoff) {
		idx++
	}
	if idx > 0 {
		list = append([]time.Time(nil), list[idx:]...)
		if len(list) == 0 {
			delete(l.hits, accountID)
		} else {
			l.hits[accountID] = list
		}
	}
	return list
}

// openAIAccountRPMAllowed 供调度器使用：返回 false 时跳过该账号。
func openAIAccountRPMAllowed(accountID int64) bool {
	return openAIAccountRPM.allow(accountID)
}

// consumeOpenAIAccountRPM 供出站路径使用。
func consumeOpenAIAccountRPM(accountID int64) {
	openAIAccountRPM.consume(accountID)
}

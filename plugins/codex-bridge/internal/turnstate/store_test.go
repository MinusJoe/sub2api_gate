package turnstate

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestCaptureReplayWithinTTL(t *testing.T) {
	s := NewStore(50 * time.Millisecond)
	key := Key(7, "thread-1")
	_, ok := s.Replay(key)
	assert.False(t, ok)

	s.Capture(key, "gAAAAAticket1")
	got, ok := s.Replay(key)
	assert.True(t, ok)
	assert.Equal(t, "gAAAAAticket1", got)

	// 覆盖：服务端换票后最新值生效。
	s.Capture(key, "gAAAAAticket2")
	got, _ = s.Replay(key)
	assert.Equal(t, "gAAAAAticket2", got)

	time.Sleep(60 * time.Millisecond)
	_, ok = s.Replay(key)
	assert.False(t, ok, "过期后不应回带")
}

func TestKeysAreAccountScoped(t *testing.T) {
	s := NewStore(time.Minute)
	s.Capture(Key(1, "thread-a"), "ticket-account-1")
	got, ok := s.Replay(Key(2, "thread-a"))
	assert.False(t, ok)
	assert.Empty(t, got, "不同账号不得共享票")

	got, ok = s.Replay(Key(1, "thread-b"))
	assert.False(t, ok, "不同线程默认不回带")
}

func TestCaptureEmptyIgnoredAndPrune(t *testing.T) {
	s := NewStore(10 * time.Millisecond)
	// 空票不存储。
	s.Capture(Key(3, "thread-x"), "")
	assert.Equal(t, 0, s.Len())

	// 空键（无线程标识）fail-closed：不存储、永不回带。
	assert.Equal(t, "", Key(3, ""))
	s.Capture("", "t")
	_, ok := s.Replay("")
	assert.False(t, ok, "无线程标识的票不得回带")
	assert.Equal(t, 0, s.Len())

	s.Capture(Key(3, "thread-a"), "t")
	s.Capture(Key(4, "thread-a"), "t")
	time.Sleep(15 * time.Millisecond)
	assert.Equal(t, 2, s.Prune())
	assert.Equal(t, 0, s.Len())
}

func TestCaptureTriggersPruneAtThreshold(t *testing.T) {
	s := NewStore(10 * time.Millisecond)
	// 灌满至阈值：中途触发的整扫时条目尚未过期，条目数保持不变。
	for i := 0; i < pruneThreshold; i++ {
		s.Capture(Key(int64(i), "prune"), "t")
	}
	assert.Equal(t, pruneThreshold, s.Len())
	time.Sleep(15 * time.Millisecond)
	// 第 pruneThreshold+1 条捕获触发顺带 Prune：过期条目清空，仅剩新键。
	s.Capture(Key(int64(pruneThreshold), "prune"), "fresh")
	assert.Equal(t, 1, s.Len(), "超阈值捕获应触发 Prune 清掉过期条目")
	got, ok := s.Replay(Key(int64(pruneThreshold), "prune"))
	assert.True(t, ok)
	assert.Equal(t, "fresh", got)
}

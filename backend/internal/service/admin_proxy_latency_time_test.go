package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type usageProxyLatencyCache struct {
	ProxyLatencyCache
	values map[int64]*ProxyLatencyInfo
}

func (c usageProxyLatencyCache) GetProxyLatencies(context.Context, []int64) (map[int64]*ProxyLatencyInfo, error) {
	return c.values, nil
}

func TestUsageProxyLatencyPreservesActualSampleTime(t *testing.T) {
	checked := time.Unix(1793180000, 0)
	ms := int64(0)
	svc := &adminServiceImpl{proxyLatencyCache: usageProxyLatencyCache{values: map[int64]*ProxyLatencyInfo{
		1: {Success: true, LatencyMs: &ms, UpdatedAt: checked},
		2: {Success: false, UpdatedAt: checked.Add(-time.Hour)},
		3: {Success: true, LatencyMs: &ms},
	}}}
	proxies := []ProxyWithAccountCount{{Proxy: Proxy{ID: 1}}, {Proxy: Proxy{ID: 2}}, {Proxy: Proxy{ID: 3}}, {Proxy: Proxy{ID: 4}}}
	svc.attachProxyLatency(context.Background(), proxies)
	require.Equal(t, checked.Unix(), *proxies[0].LatencyCheckedAt)
	require.Equal(t, int64(0), *proxies[0].LatencyMs)
	require.Equal(t, checked.Add(-time.Hour).Unix(), *proxies[1].LatencyCheckedAt)
	require.Equal(t, "failed", proxies[1].LatencyStatus)
	require.Nil(t, proxies[2].LatencyCheckedAt)
	require.Nil(t, proxies[3].LatencyCheckedAt)
}

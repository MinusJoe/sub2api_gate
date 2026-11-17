package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func ptrID(v int64) *int64 { return &v }

// 同一 proxy_id 的账号必须被归到一组；未配代理的账号归为 direct（共用网关 IP）。
func TestAnalyzeEgressSharing(t *testing.T) {
	accounts := []Account{
		{ID: 1, ProxyID: ptrID(7)},
		{ID: 2, ProxyID: ptrID(7)},
		{ID: 3, ProxyID: ptrID(9)},
		{ID: 4},
		{ID: 5},
	}
	groups := analyzeEgressSharing(accounts)
	require.Len(t, groups, 3)
	// 排序：账号数多的在前；同数量按 key 字典序。
	require.Equal(t, "direct", groups[0].Key)
	require.Equal(t, []int64{4, 5}, groups[0].AccountIDs)
	require.Equal(t, "proxy:7", groups[1].Key)
	require.Equal(t, []int64{1, 2}, groups[1].AccountIDs)
	require.Equal(t, "proxy:9", groups[2].Key)
	require.Equal(t, []int64{3}, groups[2].AccountIDs)
}

// 每个账号独立出口时不产生告警组；nil/0 proxy_id 视为直连。
func TestAnalyzeEgressSharingUniqueAndZero(t *testing.T) {
	groups := analyzeEgressSharing([]Account{
		{ID: 1, ProxyID: ptrID(1)},
		{ID: 2, ProxyID: ptrID(2)},
		{ID: 3, ProxyID: ptrID(0)},
	})
	require.Len(t, groups, 3)
	for _, g := range groups {
		require.Len(t, g.AccountIDs, 1)
	}
}

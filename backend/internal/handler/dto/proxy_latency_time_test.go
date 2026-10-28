package dto

import (
	"encoding/json"
	"testing"

	"github.com/MACOS-DO/sub4api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestUsageProxyLatencyTimeDTO(t *testing.T) {
	checked := int64(1793180000)
	p := &service.ProxyWithAccountCount{LatencyCheckedAt: &checked}
	for _, value := range []any{ProxyWithAccountCountFromService(p), ProxyWithAccountCountFromServiceAdmin(p)} {
		encoded, err := json.Marshal(value)
		require.NoError(t, err)
		require.Contains(t, string(encoded), `"latency_checked_at":1793180000`)
	}
	p.LatencyCheckedAt = nil
	encoded, err := json.Marshal(ProxyWithAccountCountFromServiceAdmin(p))
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "latency_checked_at")
}

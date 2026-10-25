package pluginconfig

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseDefaultsOnEmpty(t *testing.T) {
	for _, raw := range [][]byte{nil, []byte(``), []byte(`{}`), []byte(`null`)} {
		cfg, err := Parse(raw)
		require.NoError(t, err, "input=%q", raw)
		assert.Equal(t, Default(), cfg, "input=%q", raw)
		assert.True(t, cfg.ManageTurnState)
		assert.Equal(t, 300, cfg.TurnStateTTLSeconds)
	}
}

func TestParseRejectsUnknownFieldsAndRanges(t *testing.T) {
	_, err := Parse([]byte(`{"unexpected": true}`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected")

	_, err = Parse([]byte(`{"turn_state_ttl_seconds": 10}`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "turn_state_ttl_seconds")

	_, err = Parse([]byte(`{"max_idle_conns_per_account": 999}`))
	require.Error(t, err)

	_, err = Parse([]byte(`{"dial_timeout_seconds": 1}`))
	require.Error(t, err)

	_, err = Parse([]byte(`{"model_sniff_bytes": 1000000}`))
	require.Error(t, err)
}

func TestParseNormalizesZeroValues(t *testing.T) {
	cfg, err := Parse([]byte(`{"manage_turn_state": false, "turn_state_ttl_seconds": 0}`))
	require.NoError(t, err)
	assert.False(t, cfg.ManageTurnState)
	assert.Equal(t, Default().TurnStateTTLSeconds, cfg.TurnStateTTLSeconds)
}

// W1/W2：ws_read_limit_bytes / ws_idle_ttl_seconds 默认值与越界校验。
func TestWSBridgeConfigDefaultsAndRanges(t *testing.T) {
	cfg, err := Parse(nil)
	require.NoError(t, err)
	assert.EqualValues(t, 16<<20, cfg.WSReadLimitBytes, "默认单消息读上限 16MiB")
	assert.Equal(t, 120, cfg.WSIdleTTLSeconds, "默认空闲连接 TTL 120s")

	for _, raw := range []string{
		`{"ws_read_limit_bytes": 65535}`,
		`{"ws_read_limit_bytes": 268435457}`,
		`{"ws_idle_ttl_seconds": 4}`,
		`{"ws_idle_ttl_seconds": 3601}`,
	} {
		_, err := Parse([]byte(raw))
		require.Error(t, err, "越界配置必须拒绝: %s", raw)
	}
	for _, raw := range []string{
		`{"ws_read_limit_bytes": 65536}`,
		`{"ws_read_limit_bytes": 268435456}`,
		`{"ws_idle_ttl_seconds": 5}`,
		`{"ws_idle_ttl_seconds": 3600}`,
	} {
		_, err := Parse([]byte(raw))
		require.NoError(t, err, "边界内配置必须接受: %s", raw)
	}
}

func TestMarshalRoundTrip(t *testing.T) {
	cfg := Default()
	cfg.TurnStateTTLSeconds = 600
	parsed, err := Parse(Marshal(cfg))
	require.NoError(t, err)
	assert.Equal(t, cfg, parsed)

	var keys map[string]any
	require.NoError(t, json.Unmarshal(Marshal(cfg), &keys))
	for _, key := range []string{"manage_turn_state", "turn_state_ttl_seconds", "cookie_jar_enabled", "max_idle_conns_per_account", "idle_conn_timeout_seconds", "dial_timeout_seconds", "response_header_timeout_seconds", "observe_downgrade", "model_sniff_bytes"} {
		assert.Contains(t, keys, key)
	}
}

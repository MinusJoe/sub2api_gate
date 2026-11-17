package repository

import (
	"testing"

	"github.com/MACOS-DO/sub4api/internal/config"
	"github.com/MACOS-DO/sub4api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

// 同一个账号、同一个 proxy，不同平台的指纹必须落到不同缓存键，
// 否则 isolation=proxy 时两个形态迥异的 ClientHello 会互相串用。
func TestTLSFingerprintCacheKeysIsolateProfiles(t *testing.T) {
	anthropic := &tlsfingerprint.Profile{Name: "node24", CipherSuites: []uint16{0x1301}, ALPNProtocols: []string{"http/1.1"}}
	openai := tlsfingerprint.CodexRustlsProfile()

	keyA, suffixA := tlsFingerprintCacheKeys(config.ConnectionPoolIsolationProxy, "10.0.0.1:8080", 0, anthropic)
	keyO, suffixO := tlsFingerprintCacheKeys(config.ConnectionPoolIsolationProxy, "10.0.0.1:8080", 0, openai)

	require.NotEqual(t, keyA, keyO, "不同 profile 不能共享缓存槽位")
	require.NotEqual(t, suffixA, suffixO, "连接池后缀也必须隔离")
	require.Contains(t, keyA, tlsfingerprint.ProfileKey(anthropic))
	require.Contains(t, keyO, tlsfingerprint.ProfileKey(openai))
}

// 相同内容必须稳定：缓存复用依赖这一点。
func TestTLSFingerprintCacheKeysStableForSameProfile(t *testing.T) {
	a, sa := tlsFingerprintCacheKeys(config.ConnectionPoolIsolationAccount, "", 7, tlsfingerprint.CodexRustlsProfile())
	b, sb := tlsFingerprintCacheKeys(config.ConnectionPoolIsolationAccount, "", 7, tlsfingerprint.CodexRustlsProfile())
	require.Equal(t, a, b)
	require.Equal(t, sa, sb)
	require.Contains(t, a, "account:7")
}

// 改一个字段就必须换键，否则管理员改模板后旧连接仍用旧指纹。
func TestTLSFingerprintCacheKeysChangeWithProfileEdits(t *testing.T) {
	base := tlsfingerprint.CodexRustlsProfile()
	edited := tlsfingerprint.CodexRustlsProfile()
	edited.CipherSuites = append([]uint16{0x1301}, edited.CipherSuites...)

	keyBase, _ := tlsFingerprintCacheKeys(config.ConnectionPoolIsolationAccount, "", 1, base)
	keyEdited, _ := tlsFingerprintCacheKeys(config.ConnectionPoolIsolationAccount, "", 1, edited)
	require.NotEqual(t, keyBase, keyEdited)
}

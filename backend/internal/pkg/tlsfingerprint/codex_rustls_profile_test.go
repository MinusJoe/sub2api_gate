package tlsfingerprint

import (
	"context"
	"net"
	"os"
	"strconv"
	"strings"
	"testing"

	utls "github.com/refraction-networking/utls"
	"github.com/stretchr/testify/require"
)

// 抓包得到的 JA3 原文（13/14 条一致）。profile 的任何字段改动都必须让它继续保持一致，
// 否则等于悄悄换掉了"官方客户端"的身份。
const codexRustlsJA3 = "771,4866-4867-4865-49196-49200-159-52393-52392-52394-49195-49199-158-49188-49192-107-49187-49191-103-49162-49172-57-49161-49171-51-157-156-61-60-53-47,65281-0-11-10-35-22-23-13-43-45-51,4588-29-23-30-24-25-256-257,0"

func joinUint16(values []uint16) string {
	parts := make([]string, 0, len(values))
	for _, v := range values {
		parts = append(parts, strconv.FormatUint(uint64(v), 10))
	}
	return strings.Join(parts, "-")
}

// ja3FromProfile 按 JA3 定义（SSLVersion,Ciphers,Extensions,Curves,PointFormats）拼接。
func ja3FromProfile(p *Profile) string {
	return strings.Join([]string{
		"771", // ClientHello legacy_version（TLS 1.2）
		joinUint16(p.CipherSuites),
		joinUint16(p.Extensions),
		joinUint16(p.Curves),
		joinUint16(p.PointFormats),
	}, ",")
}

func TestCodexRustlsProfileMatchesCapturedJA3(t *testing.T) {
	profile := CodexRustlsProfile()
	require.Equal(t, codexRustlsJA3, ja3FromProfile(profile))
}

// 官方客户端不协商 h2：扩展里不能出现 16(ALPN)，spec 里也不应有 ALPNExtension。
func TestCodexRustlsProfileHasNoALPN(t *testing.T) {
	profile := CodexRustlsProfile()
	require.NotContains(t, profile.Extensions, uint16(16))
	require.Empty(t, profile.ALPNProtocols)

	spec := buildClientHelloSpecFromProfile(profile)
	for _, ext := range spec.Extensions {
		_, isALPN := ext.(*utls.ALPNExtension)
		require.False(t, isALPN, "spec 中不应出现 ALPN 扩展")
	}
}

// 后量子参数必须真的进入 spec，而不是只写在结构体里。
func TestCodexRustlsProfileSpecCarriesPQParameters(t *testing.T) {
	profile := CodexRustlsProfile()
	spec := buildClientHelloSpecFromProfile(profile)

	require.Equal(t, profile.CipherSuites, spec.CipherSuites)

	var (
		versions []uint16
		groups   []utls.CurveID
		shares   []utls.KeyShare
		sigAlgs  []utls.SignatureScheme
		extOrder []uint16
	)
	for _, ext := range spec.Extensions {
		switch e := ext.(type) {
		case *utls.SupportedVersionsExtension:
			versions = e.Versions
		case *utls.SupportedCurvesExtension:
			groups = e.Curves
		case *utls.KeyShareExtension:
			shares = e.KeyShares
		case *utls.SignatureAlgorithmsExtension:
			sigAlgs = e.SupportedSignatureAlgorithms
		case *utls.GenericExtension:
			extOrder = append(extOrder, e.Id)
		}
	}
	require.Equal(t, []uint16{0x0304, 0x0303}, versions)
	require.Equal(t, utls.CurveID(0x11ec), groups[0], "supported_groups 首位必须是 X25519MLKEM768")
	require.Equal(t, utls.CurveID(0x11ec), shares[0].Group, "key_share 首位必须是 X25519MLKEM768")
	require.Equal(t, utls.CurveID(0x001d), groups[1])
	require.Equal(t, utls.SignatureScheme(0x0905), sigAlgs[0], "sigalgs 首位必须是 ML-DSA-65")
	// 22(encrypt_then_mac) 走 GenericExtension，必须仍然出现在扩展序列里。
	require.Contains(t, extOrder, uint16(22))
}

// 端到端探针：设置 TLSFP_PROBE_PROXY=<host:port> 指向 fpcap peek，
// 真实握手一次，用于人工核对抓到的 JA3 是否与 codexRustlsJA3 一致。
func TestCodexRustlsProfileHandshakeProbe(t *testing.T) {
	proxy := strings.TrimSpace(os.Getenv("TLSFP_PROBE_PROXY"))
	if proxy == "" {
		t.Skip("未设置 TLSFP_PROBE_PROXY，跳过真实握手探针")
	}
	base := func(ctx context.Context, network, addr string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "tcp", proxy)
	}
	dialer := NewDialer(CodexRustlsProfile(), base)
	conn, err := dialer.DialTLSContext(context.Background(), "tcp", "chatgpt.com:443")
	require.NoError(t, err)
	_ = conn.Close()
}

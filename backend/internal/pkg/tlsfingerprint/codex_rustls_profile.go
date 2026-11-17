package tlsfingerprint

// codexRustlsProfile 描述官方 Codex CLI（Linux 构建，rustls）真实发出的 ClientHello。
//
// 数据来源：2026-11 用 cmd/fpcap 在 Ubuntu 26.04 (WSL2) 上抓取 codex-cli 0.169.0 的
// 14 次握手，其中 13 次完全一致，JA3 为：
//
//	771,4866-4867-4865-49196-49200-159-52393-52392-52394-49195-49199-158-49188-49192-107-
//	49187-49191-103-49162-49172-57-49161-49171-51-157-156-61-60-53-47,
//	65281-0-11-10-35-22-23-13-43-45-51,4588-29-23-30-24-25-256-257,0
//
// 关键观察（不是推断，是抓包结果）：
//   - TLS 1.3（supported_versions 0304,0303），带 X25519MLKEM768(4588) 后量子混合密钥交换；
//   - 签名算法包含 ML-DSA(2308-2310)；
//   - **没有 ALPN 扩展**：官方客户端只走 HTTP/1.1，不协商 h2。
//
// 因此该 profile 故意不包含扩展 16（ALPN）。改这里之前先重新抓包核对。
func codexRustlsProfile() *Profile {
	return &Profile{
		Name: "codex_cli_rs_0.169.0_linux_rustls",
		CipherSuites: []uint16{
			// TLS 1.3
			0x1302, 0x1303, 0x1301,
			// TLS 1.2 回退（rustls 默认顺序，抓包原样）
			0xc02c, 0xc030, 0x009f, 0xcca9, 0xcca8, 0xccaa,
			0xc02b, 0xc02f, 0x009e, 0xc024, 0xc028, 0x006b,
			0xc023, 0xc027, 0x0067, 0xc00a, 0xc014, 0x0039,
			0xc009, 0xc013, 0x0033, 0x009d, 0x009c, 0x003d,
			0x003c, 0x0035, 0x002f,
		},
		Curves: []uint16{
			0x11ec, // X25519MLKEM768
			0x001d, 0x0017, 0x001e, 0x0018, 0x0019, 0x0100, 0x0101,
		},
		PointFormats: []uint16{0},
		SignatureAlgorithms: []uint16{
			0x0905, 0x0906, 0x0904,
			0x0403, 0x0503, 0x0603,
			0x0807, 0x0808, 0x081a, 0x081b, 0x081c,
			0x0809, 0x080a, 0x080b,
			0x0804, 0x0805, 0x0806,
			0x0401, 0x0501, 0x0601,
			0x0303, 0x0301, 0x0302,
			0x0402, 0x0502, 0x0602,
		},
		// 不设 ALPN：官方客户端握手不带 ALPN 扩展。这里保持空切片只用于文档意图，
		// 真正生效的是 Extensions 里没有 16。
		ALPNProtocols:     nil,
		SupportedVersions: []uint16{0x0304, 0x0303},
		KeyShareGroups:    []uint16{0x11ec, 0x001d},
		PSKModes:          []uint16{1}, // psk_dhe_ke
		Extensions:        []uint16{65281, 0, 11, 10, 35, 22, 23, 13, 43, 45, 51},
		EnableGREASE:      false,
	}
}

// CodexRustlsProfile 返回官方 Codex CLI（Linux/rustls）指纹 profile 的副本，
// 避免调用方修改共享定义。
func CodexRustlsProfile() *Profile {
	p := codexRustlsProfile()
	clone := *p
	clone.CipherSuites = append([]uint16(nil), p.CipherSuites...)
	clone.Curves = append([]uint16(nil), p.Curves...)
	clone.PointFormats = append([]uint16(nil), p.PointFormats...)
	clone.SignatureAlgorithms = append([]uint16(nil), p.SignatureAlgorithms...)
	clone.SupportedVersions = append([]uint16(nil), p.SupportedVersions...)
	clone.KeyShareGroups = append([]uint16(nil), p.KeyShareGroups...)
	clone.PSKModes = append([]uint16(nil), p.PSKModes...)
	clone.Extensions = append([]uint16(nil), p.Extensions...)
	return &clone
}

// CodexRustlsProfileJA3 返回抓包得到的官方 JA3 原文，供验收测试与外部工具比对。
func CodexRustlsProfileJA3() string {
	return "771,4866-4867-4865-49196-49200-159-52393-52392-52394-49195-49199-158-49188-49192-107-49187-49191-103-49162-49172-57-49161-49171-51-157-156-61-60-53-47,65281-0-11-10-35-22-23-13-43-45-51,4588-29-23-30-24-25-256-257,0"
}

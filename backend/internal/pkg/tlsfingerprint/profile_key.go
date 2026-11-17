package tlsfingerprint

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"strconv"
	"strings"
)

// ProfileKey 返回 profile 内容的稳定摘要，用于把不同指纹的客户端隔离到不同缓存槽位。
//
// 为什么需要它：TLS 客户端是按（isolation, proxy, account）缓存的；在
// connection_pool_isolation=proxy 时缓存键里没有 accountID，Anthropic（Node.js，带 ALPN）
// 与 OpenAI（rustls，无 ALPN）会落进同一个槽位，谁先建就连谁。把内容摘要拼进键，
// 既能跨平台隔离，也能在管理员改绑/改模板后让旧连接自然失效。
func ProfileKey(p *Profile) string {
	if p == nil {
		return ""
	}
	h := sha256.New()
	writeString := func(s string) {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(s)))
		_, _ = h.Write(n[:])
		_, _ = h.Write([]byte(s))
	}
	writeUint16s := func(values []uint16) {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(values)))
		_, _ = h.Write(n[:])
		var b [2]byte
		for _, v := range values {
			binary.BigEndian.PutUint16(b[:], v)
			_, _ = h.Write(b[:])
		}
	}

	writeString(p.Name)
	writeUint16s(p.CipherSuites)
	writeUint16s(p.Curves)
	writeUint16s(p.PointFormats)
	writeUint16s(p.SignatureAlgorithms)
	writeUint16s(p.SupportedVersions)
	writeUint16s(p.KeyShareGroups)
	writeUint16s(p.PSKModes)
	writeUint16s(p.Extensions)
	if p.EnableGREASE {
		writeString("grease:1")
	} else {
		writeString("grease:0")
	}
	writeString(strings.Join(p.ALPNProtocols, ","))

	return hex.EncodeToString(h.Sum(nil))[:16]
}

// ProfileKeyForDebug 便于日志里打印"profile 名#短摘要"，避免只靠名字区分。
func ProfileKeyForDebug(p *Profile) string {
	if p == nil {
		return "none"
	}
	name := p.Name
	if name == "" {
		name = "unnamed"
	}
	return name + "#" + ProfileKey(p) + "@" + strconv.Itoa(len(p.CipherSuites)) + "cs"
}

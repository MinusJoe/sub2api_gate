// clienthello.go 从 TLS 记录流里取出 ClientHello 并解析成指纹原料。
//
// 只用 io.Reader，且严格按记录长度读取，ClientHello 之后的字节不会被消费，
// 因此可以直接用在纯透传代理里（peek 模式）。
package main

import (
	"encoding/binary"
	"fmt"
	"io"
)

// TLS 记录/握手/扩展常量（只覆盖解析 ClientHello 所需的最小集合）。
const (
	tlsRecordTypeHandshake      uint8 = 0x16
	tlsHandshakeTypeClientHello uint8 = 0x01
	tlsMaxRecordPayload               = 1 << 14
	tlsRecordOverhead                 = 2048
	tlsMaxHandshakeBytes              = 64 << 10

	extServerName          uint16 = 0
	extSupportedGroups     uint16 = 10
	extPointFormats        uint16 = 11
	extSignatureAlgorithms uint16 = 13
	extALPN                uint16 = 16
	extSupportedVersions   uint16 = 43
	extKeyShare            uint16 = 51
)

// TLSInfo 是一条记录的 TLS 层观察结果，字段顺序即 JSON 输出顺序。
//
// 版本类字段（version、supported_versions）用 0x 前缀十六进制，和线上字节一一对应；
// JA3 的原料（ciphers/extensions/curves/sigalgs/ec_point_formats）用十进制整数，
// 便于和 ja3 原文逐段对照。ec_point_formats 用 []int 而非 []uint8：
// 后者等价于 []byte，会被 encoding/json 编成 base64 而不是数字数组。
type TLSInfo struct {
	Version           string   `json:"version"`
	SNI               string   `json:"sni"`
	ALPN              []string `json:"alpn"`
	Ciphers           []uint16 `json:"ciphers"`
	Extensions        []uint16 `json:"extensions"`
	Curves            []uint16 `json:"curves"`
	SigAlgs           []uint16 `json:"sigalgs"`
	SupportedVersions []string `json:"supported_versions"`
	KeyShareGroups    []uint16 `json:"key_share_groups"`
	PointFormats      []int    `json:"ec_point_formats"`
	JA3               string   `json:"ja3"`
	JA3MD5            string   `json:"ja3_md5"`
}

// ClientHello 汇总一次握手读到的 ClientHello 观察结果。
// 各列表都保持线上出现顺序，GREASE 值也原样保留——它们本身就是指纹的一部分。
type ClientHello struct {
	// RecordVersion 是首条记录头里的版本号（TLS 1.3 客户端通常仍是 0x0301）。
	RecordVersion uint16
	// Raw 是本次已读出的全部字节（含记录头），透传时按此原样回放。
	Raw []byte
	// Version 是 ClientHello 体内的 legacy_version。
	Version           uint16
	Ciphers           []uint16
	Extensions        []uint16
	SNI               string
	ALPN              []string
	Curves            []uint16
	SigAlgs           []uint16
	SupportedVersions []uint16
	KeyShareGroups    []uint16
	PointFormats      []uint8
	// Warnings 记录可容忍的解析异常（例如某个扩展长度不自洽），不影响其余字段。
	Warnings []string
}

// ReadClientHello 读取 TLS 记录直到凑齐完整的 ClientHello 握手消息。
//
// 返回的 ClientHello.Raw 必须原样转发给上游；出错时已经读走的字节会随 error 一起丢弃，
// 因此调用方应在解析成功后立刻回放 Raw。ClientHello 可能被拆到多条记录里，这里会继续读。
func ReadClientHello(r io.Reader) (*ClientHello, error) {
	ch := &ClientHello{}
	var raw []byte
	var hs []byte
	for {
		rec := make([]byte, 5)
		if _, err := io.ReadFull(r, rec); err != nil {
			return nil, fmt.Errorf("读取 TLS 记录头: %w", err)
		}
		raw = append(raw, rec...)
		typ := rec[0]
		recVer := binary.BigEndian.Uint16(rec[1:3])
		length := int(binary.BigEndian.Uint16(rec[3:5]))
		if ch.RecordVersion == 0 {
			ch.RecordVersion = recVer
		}
		if typ != tlsRecordTypeHandshake {
			return nil, fmt.Errorf("首条记录不是 TLS 握手记录: type=0x%02x", typ)
		}
		if length == 0 || length > tlsMaxRecordPayload+tlsRecordOverhead {
			return nil, fmt.Errorf("非法 TLS 记录长度: %d", length)
		}
		if len(raw)+length > tlsMaxHandshakeBytes {
			return nil, fmt.Errorf("ClientHello 超过 %d 字节上限", tlsMaxHandshakeBytes)
		}
		payload := make([]byte, length)
		if _, err := io.ReadFull(r, payload); err != nil {
			return nil, fmt.Errorf("读取 TLS 记录负载: %w", err)
		}
		raw = append(raw, payload...)
		hs = append(hs, payload...)
		if len(hs) < 4 {
			continue // 握手头还没凑齐
		}
		if hs[0] != tlsHandshakeTypeClientHello {
			return nil, fmt.Errorf("首条握手消息不是 ClientHello: type=0x%02x", hs[0])
		}
		msgLen := int(hs[1])<<16 | int(hs[2])<<8 | int(hs[3])
		if msgLen > tlsMaxHandshakeBytes {
			return nil, fmt.Errorf("ClientHello 消息长度 %d 超过上限", msgLen)
		}
		if len(hs) < 4+msgLen {
			continue // 握手消息还没收完，继续读后续记录
		}
		if err := ch.parseBody(hs[4 : 4+msgLen]); err != nil {
			return nil, err
		}
		ch.Raw = raw
		return ch, nil
	}
}

// parseBody 解析 ClientHello 体（不含 4 字节握手头）。
func (ch *ClientHello) parseBody(body []byte) error {
	c := &byteCursor{b: body}
	version, err := c.u16()
	if err != nil {
		return fmt.Errorf("ClientHello 版本: %w", err)
	}
	ch.Version = version
	if _, err := c.take(32); err != nil {
		return fmt.Errorf("ClientHello random: %w", err)
	}
	sidLen, err := c.u8()
	if err != nil {
		return fmt.Errorf("ClientHello session_id 长度: %w", err)
	}
	if _, err := c.take(int(sidLen)); err != nil {
		return fmt.Errorf("ClientHello session_id: %w", err)
	}
	csLen, err := c.u16()
	if err != nil {
		return fmt.Errorf("ClientHello cipher_suites 长度: %w", err)
	}
	if csLen%2 != 0 {
		return fmt.Errorf("cipher_suites 长度 %d 为奇数", csLen)
	}
	cs, err := c.take(int(csLen))
	if err != nil {
		return fmt.Errorf("ClientHello cipher_suites: %w", err)
	}
	ch.Ciphers = make([]uint16, 0, len(cs)/2)
	for i := 0; i+1 < len(cs); i += 2 {
		ch.Ciphers = append(ch.Ciphers, binary.BigEndian.Uint16(cs[i:]))
	}
	cmLen, err := c.u8()
	if err != nil {
		return fmt.Errorf("ClientHello compression_methods 长度: %w", err)
	}
	if _, err := c.take(int(cmLen)); err != nil {
		return fmt.Errorf("ClientHello compression_methods: %w", err)
	}
	if c.remaining() == 0 {
		return nil // 老式 ClientHello 允许没有 extensions
	}
	extLen, err := c.u16()
	if err != nil {
		return fmt.Errorf("ClientHello extensions 长度: %w", err)
	}
	ext, err := c.take(int(extLen))
	if err != nil {
		return fmt.Errorf("ClientHello extensions: %w", err)
	}
	return ch.parseExtensions(ext)
}

// parseExtensions 顺序遍历扩展，未识别的扩展只记录 ID。
func (ch *ClientHello) parseExtensions(b []byte) error {
	for len(b) > 0 {
		if len(b) < 4 {
			ch.warnf("扩展尾部不足 4 字节（剩 %d），已忽略", len(b))
			return nil
		}
		id := binary.BigEndian.Uint16(b[0:2])
		l := int(binary.BigEndian.Uint16(b[2:4]))
		b = b[4:]
		if l > len(b) {
			ch.warnf("扩展 0x%04x 声明长度 %d 超过剩余 %d，已忽略", id, l, len(b))
			return nil
		}
		data := b[:l]
		b = b[l:]
		ch.Extensions = append(ch.Extensions, id)
		switch id {
		case extServerName:
			ch.parseServerName(data)
		case extSupportedGroups:
			ch.Curves = parseUint16Vector(data, "supported_groups", ch)
		case extPointFormats:
			ch.PointFormats = parsePointFormats(data, ch)
		case extSignatureAlgorithms:
			ch.SigAlgs = parseUint16Vector(data, "signature_algorithms", ch)
		case extALPN:
			ch.ALPN = parseALPN(data, ch)
		case extSupportedVersions:
			ch.SupportedVersions = parseSupportedVersions(data, ch)
		case extKeyShare:
			ch.KeyShareGroups = parseKeyShareGroups(data, ch)
		}
	}
	return nil
}

// parseServerName 解析 SNI 扩展，只取 host_name（type 0）条目。
func (ch *ClientHello) parseServerName(d []byte) {
	if len(d) < 2 {
		ch.warnf("server_name 扩展过短（%d 字节），已忽略", len(d))
		return
	}
	total := int(binary.BigEndian.Uint16(d[0:2]))
	if total > len(d)-2 {
		ch.warnf("server_name 列表长度 %d 超过剩余 %d，按实际长度截断", total, len(d)-2)
		total = len(d) - 2
	}
	rest := d[2 : 2+total]
	for len(rest) >= 3 {
		nameType := rest[0]
		l := int(binary.BigEndian.Uint16(rest[1:3]))
		rest = rest[3:]
		if l > len(rest) {
			ch.warnf("server_name 条目长度 %d 超过剩余 %d，已忽略", l, len(rest))
			return
		}
		if nameType == 0 {
			ch.SNI = string(rest[:l])
		}
		rest = rest[l:]
	}
}

// parseALPN 解析 ALPN 扩展，保持协议声明顺序。
func parseALPN(d []byte, ch *ClientHello) []string {
	if len(d) < 2 {
		ch.warnf("ALPN 扩展过短（%d 字节），已忽略", len(d))
		return nil
	}
	total := int(binary.BigEndian.Uint16(d[0:2]))
	if total > len(d)-2 {
		ch.warnf("ALPN 列表长度 %d 超过剩余 %d，按实际长度截断", total, len(d)-2)
		total = len(d) - 2
	}
	rest := d[2 : 2+total]
	out := make([]string, 0, 2)
	for len(rest) > 0 {
		l := int(rest[0])
		rest = rest[1:]
		if l == 0 || l > len(rest) {
			ch.warnf("ALPN 条目长度 %d 不合法（剩余 %d），停止解析", l, len(rest))
			return out
		}
		out = append(out, string(rest[:l]))
		rest = rest[l:]
	}
	return out
}

// parsePointFormats 解析 ec_point_formats 扩展（单字节长度前缀）。
func parsePointFormats(d []byte, ch *ClientHello) []uint8 {
	if len(d) < 1 {
		ch.warnf("ec_point_formats 扩展为空，已忽略")
		return nil
	}
	total := int(d[0])
	if total > len(d)-1 {
		ch.warnf("ec_point_formats 长度 %d 超过剩余 %d，按实际长度截断", total, len(d)-1)
		total = len(d) - 1
	}
	out := make([]uint8, 0, total)
	out = append(out, d[1:1+total]...)
	return out
}

// parseSupportedVersions 解析 supported_versions 扩展：
// RFC 8446 规定这里是 1 字节总长 + 若干 uint16（与 supported_groups 的 2 字节总长不同）。
func parseSupportedVersions(d []byte, ch *ClientHello) []uint16 {
	if len(d) < 1 {
		ch.warnf("supported_versions 扩展为空，已忽略")
		return nil
	}
	total := int(d[0])
	if total > len(d)-1 {
		ch.warnf("supported_versions 长度 %d 超过剩余 %d，按实际长度截断", total, len(d)-1)
		total = len(d) - 1
	}
	if total%2 != 0 {
		ch.warnf("supported_versions 长度为奇数（%d），丢弃末尾 %d 字节", total, total%2)
		total -= total % 2
	}
	rest := d[1 : 1+total]
	out := make([]uint16, 0, total/2)
	for i := 0; i+1 < len(rest); i += 2 {
		out = append(out, binary.BigEndian.Uint16(rest[i:]))
	}
	return out
}

// parseUint16Vector 解析“2 字节总长 + 若干 uint16”的向量，
// 用于 supported_groups / signature_algorithms。
func parseUint16Vector(d []byte, name string, ch *ClientHello) []uint16 {
	if len(d) < 2 {
		ch.warnf("%s 扩展过短（%d 字节），已忽略", name, len(d))
		return nil
	}
	total := int(binary.BigEndian.Uint16(d[0:2]))
	if total > len(d)-2 {
		ch.warnf("%s 长度 %d 超过剩余 %d，按实际长度截断", name, total, len(d)-2)
		total = len(d) - 2
	}
	if total%2 != 0 {
		ch.warnf("%s 长度为奇数（%d），丢弃末尾 %d 字节", name, total, total%2)
		total -= total % 2
	}
	rest := d[2 : 2+total]
	out := make([]uint16, 0, total/2)
	for i := 0; i+1 < len(rest); i += 2 {
		out = append(out, binary.BigEndian.Uint16(rest[i:]))
	}
	return out
}

// parseKeyShareGroups 解析 key_share 扩展里的 group 列表（不保留公钥字节）。
func parseKeyShareGroups(d []byte, ch *ClientHello) []uint16 {
	if len(d) < 2 {
		ch.warnf("key_share 扩展过短（%d 字节），已忽略", len(d))
		return nil
	}
	total := int(binary.BigEndian.Uint16(d[0:2]))
	if total > len(d)-2 {
		ch.warnf("key_share 长度 %d 超过剩余 %d，按实际长度截断", total, len(d)-2)
		total = len(d) - 2
	}
	rest := d[2 : 2+total]
	out := make([]uint16, 0, 2)
	for len(rest) >= 4 {
		group := binary.BigEndian.Uint16(rest[0:2])
		l := int(binary.BigEndian.Uint16(rest[2:4]))
		rest = rest[4:]
		if l > len(rest) {
			ch.warnf("key_share 条目 group=0x%04x 长度 %d 超过剩余 %d，停止解析", group, l, len(rest))
			return out
		}
		out = append(out, group)
		rest = rest[l:]
	}
	return out
}

// toTLSInfo 把解析结果整理成稳定的 JSON 结构，并补上 JA3 与其 MD5。
func (ch *ClientHello) toTLSInfo() *TLSInfo {
	info := &TLSInfo{
		Version:           fmt.Sprintf("0x%04x", ch.Version),
		SNI:               ch.SNI,
		ALPN:              nonNil(ch.ALPN),
		Ciphers:           nonNil(ch.Ciphers),
		Extensions:        nonNil(ch.Extensions),
		Curves:            nonNil(ch.Curves),
		SigAlgs:           nonNil(ch.SigAlgs),
		SupportedVersions: hexVersionList(ch.SupportedVersions),
		KeyShareGroups:    nonNil(ch.KeyShareGroups),
		PointFormats:      intList(ch.PointFormats),
	}
	info.JA3 = JA3String(ch)
	info.JA3MD5 = JA3MD5Sum(info.JA3)
	return info
}

// warnf 追加一条可容忍的解析告警。
func (ch *ClientHello) warnf(format string, args ...any) {
	ch.Warnings = append(ch.Warnings, fmt.Sprintf(format, args...))
}

// hexVersionList 把 16 位版本号渲染成 0x 前缀十六进制，保持线上字节的可读形式。
func hexVersionList(v []uint16) []string {
	out := make([]string, 0, len(v))
	for _, x := range v {
		out = append(out, fmt.Sprintf("0x%04x", x))
	}
	return out
}

// nonNil 保证空列表序列化为 []，避免同一 schema 在 JSON 里时而是 null。
func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// intList 把 uint8 列表转成 []int：[]uint8 会被 encoding/json 当成 []byte 编成 base64，
// 转成 []int 才能在 JSON 里保持数字数组。
func intList(v []uint8) []int {
	out := make([]int, 0, len(v))
	for _, x := range v {
		out = append(out, int(x))
	}
	return out
}

// byteCursor 是 ClientHello 体内的顺序读取器，越界返回错误而不是 panic。
type byteCursor struct {
	b   []byte
	off int
}

func (c *byteCursor) remaining() int { return len(c.b) - c.off }

func (c *byteCursor) u8() (uint8, error) {
	if c.remaining() < 1 {
		return 0, fmt.Errorf("需要 1 字节，剩余 0")
	}
	v := c.b[c.off]
	c.off++
	return v, nil
}

func (c *byteCursor) u16() (uint16, error) {
	if c.remaining() < 2 {
		return 0, fmt.Errorf("需要 2 字节，剩余 %d", c.remaining())
	}
	v := binary.BigEndian.Uint16(c.b[c.off:])
	c.off += 2
	return v, nil
}

func (c *byteCursor) take(n int) ([]byte, error) {
	if n < 0 {
		return nil, fmt.Errorf("非法读取长度 %d", n)
	}
	if c.remaining() < n {
		return nil, fmt.Errorf("需要 %d 字节，剩余 %d", n, c.remaining())
	}
	v := c.b[c.off : c.off+n]
	c.off += n
	return v, nil
}

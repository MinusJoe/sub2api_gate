package main

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/http2/hpack"
)

// ---- 测试用的字节构造辅助 ----

func be16(v uint16) []byte {
	b := make([]byte, 2)
	binary.BigEndian.PutUint16(b, v)
	return b
}

func be32(v uint32) []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, v)
	return b
}

func concat(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// vector16 生成 “2 字节总长 + 内容” 的向量。
func vector16(parts ...[]byte) []byte {
	body := concat(parts...)
	return concat(be16(uint16(len(body))), body)
}

// vector8 生成 “1 字节总长 + 内容” 的向量。
func vector8(parts ...[]byte) []byte {
	body := concat(parts...)
	return concat([]byte{byte(len(body))}, body)
}

// tlsExt 生成一个完整的扩展：id + 2 字节长度 + 数据。
func tlsExt(id uint16, data []byte) []byte {
	return concat(be16(id), be16(uint16(len(data))), data)
}

// tlsRecord 生成一条 TLS 记录：类型 + 版本(0x0301) + 2 字节长度 + 负载。
func tlsRecord(typ uint8, body []byte) []byte {
	return concat([]byte{typ, 0x03, 0x01}, be16(uint16(len(body))), body)
}

// handshakeMsg 生成握手消息：类型 + 3 字节长度 + 负载。
func handshakeMsg(typ uint8, body []byte) []byte {
	l := len(body)
	return concat([]byte{typ, byte(l >> 16), byte(l >> 8), byte(l)}, body)
}

// buildClientHelloBody 组装 ClientHello 体（不含握手头）。
// extBody 为所有扩展拼接后的内容；为空表示不带 extensions 字段。
func buildClientHelloBody(version uint16, ciphers []uint16, extBody []byte) []byte {
	var cs []byte
	for _, c := range ciphers {
		cs = append(cs, be16(c)...)
	}
	body := concat(
		be16(version),
		bytes.Repeat([]byte{0xab}, 32), // random
		[]byte{0},                      // session_id 长度 0
		be16(uint16(len(cs))), cs,      // cipher_suites
		[]byte{1, 0}, // compression_methods：仅 null
	)
	if len(extBody) > 0 {
		body = append(body, be16(uint16(len(extBody)))...)
		body = append(body, extBody...)
	}
	return body
}

// h2FrameBytes 生成一个完整的 HTTP/2 帧。
func h2FrameBytes(typ, flags uint8, streamID uint32, payload []byte) []byte {
	hdr := make([]byte, 9)
	hdr[0] = byte(len(payload) >> 16)
	hdr[1] = byte(len(payload) >> 8)
	hdr[2] = byte(len(payload))
	hdr[3] = typ
	hdr[4] = flags
	binary.BigEndian.PutUint32(hdr[5:9], streamID&0x7fffffff)
	return concat(hdr, payload)
}

func assertUint16Slice(t *testing.T, name string, got, want []uint16) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s 长度 = %d，期望 %d（实际 %v）", name, len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s[%d] = %d，期望 %d（整体 %v）", name, i, got[i], want[i], got)
		}
	}
}

func assertUint8Slice(t *testing.T, name string, got, want []uint8) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s 长度 = %d，期望 %d（实际 %v）", name, len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s[%d] = %d，期望 %d（整体 %v）", name, i, got[i], want[i], got)
		}
	}
}

func assertStringSlice(t *testing.T, name string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s 长度 = %d，期望 %d（实际 %v）", name, len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s[%d] = %q，期望 %q（整体 %v）", name, i, got[i], want[i], got)
		}
	}
}

// ---- ClientHello 解析 ----

func TestReadClientHelloFieldsAndJA3(t *testing.T) {
	sni := "api.example.com"
	sniData := vector16(concat([]byte{0}, be16(uint16(len(sni))), []byte(sni)))
	alpnData := vector16(
		concat([]byte{2}, []byte("h2")),
		concat([]byte{8}, []byte("http/1.1")),
	)
	extBody := concat(
		tlsExt(extServerName, sniData),
		tlsExt(extSupportedGroups, vector16(be16(29), be16(23))),
		tlsExt(extPointFormats, vector8([]byte{0})),
		tlsExt(extSignatureAlgorithms, vector16(be16(0x0804), be16(0x0403))),
		tlsExt(extALPN, alpnData),
		tlsExt(extSupportedVersions, vector8(be16(0x0304), be16(0x0303))),
		tlsExt(extKeyShare, vector16(concat(be16(29), be16(32), bytes.Repeat([]byte{0x11}, 32)))),
		tlsExt(0xfafa, []byte{0xde, 0xad}), // 未识别扩展也要保留在顺序里
	)
	body := buildClientHelloBody(0x0303, []uint16{0x1301, 0x1302, 0xc02f}, extBody)
	record := tlsRecord(tlsRecordTypeHandshake, handshakeMsg(tlsHandshakeTypeClientHello, body))

	trailing := []byte("REMAINING-AFTER-CLIENTHELLO")
	stream := concat(record, trailing)

	ch, err := ReadClientHello(bytes.NewReader(stream))
	if err != nil {
		t.Fatalf("ReadClientHello 失败: %v", err)
	}
	if ch.RecordVersion != 0x0301 {
		t.Fatalf("RecordVersion = 0x%04x，期望 0x0301", ch.RecordVersion)
	}
	if ch.Version != 0x0303 {
		t.Fatalf("Version = 0x%04x，期望 0x0303", ch.Version)
	}
	assertUint16Slice(t, "ciphers", ch.Ciphers, []uint16{0x1301, 0x1302, 0xc02f})
	assertUint16Slice(t, "extensions", ch.Extensions, []uint16{0, 10, 11, 13, 16, 43, 51, 0xfafa})
	if ch.SNI != sni {
		t.Fatalf("SNI = %q，期望 %q", ch.SNI, sni)
	}
	assertStringSlice(t, "alpn", ch.ALPN, []string{"h2", "http/1.1"})
	assertUint16Slice(t, "curves", ch.Curves, []uint16{29, 23})
	assertUint16Slice(t, "sigalgs", ch.SigAlgs, []uint16{0x0804, 0x0403})
	assertUint16Slice(t, "supported_versions", ch.SupportedVersions, []uint16{0x0304, 0x0303})
	assertUint16Slice(t, "key_share_groups", ch.KeyShareGroups, []uint16{29})
	assertUint8Slice(t, "ec_point_formats", ch.PointFormats, []uint8{0})
	if len(ch.Warnings) != 0 {
		t.Fatalf("不应有解析告警，实际 %v", ch.Warnings)
	}
	// 只消费 ClientHello 所在的记录，不能吞掉后续字节。
	if !bytes.Equal(ch.Raw, record) {
		t.Fatalf("Raw 长度 = %d，期望 %d（必须只包含已读记录）", len(ch.Raw), len(record))
	}
	rest, err := io.ReadAll(bytes.NewReader(stream[len(ch.Raw):]))
	if err != nil {
		t.Fatalf("读取剩余字节失败: %v", err)
	}
	if !bytes.Equal(rest, trailing) {
		t.Fatalf("剩余字节 = %q，期望 %q", rest, trailing)
	}

	info := ch.toTLSInfo()
	wantJA3 := "771,4865-4866-49199,0-10-11-13-16-43-51-64250,29-23,0"
	if info.JA3 != wantJA3 {
		t.Fatalf("JA3 = %q，期望 %q", info.JA3, wantJA3)
	}
	if info.JA3MD5 != JA3MD5Sum(wantJA3) {
		t.Fatalf("JA3 MD5 = %q，期望 %q", info.JA3MD5, JA3MD5Sum(wantJA3))
	}
	if info.Version != "0x0303" {
		t.Fatalf("tls.version = %q，期望 0x0303", info.Version)
	}
	assertStringSlice(t, "supported_versions(hex)", info.SupportedVersions, []string{"0x0304", "0x0303"})
}

func TestReadClientHelloFragmentAcrossRecords(t *testing.T) {
	body := buildClientHelloBody(0x0303, []uint16{0x1301}, nil)
	hs := handshakeMsg(tlsHandshakeTypeClientHello, body)
	half := len(hs) / 2
	rec1 := tlsRecord(tlsRecordTypeHandshake, hs[:half])
	rec2 := tlsRecord(tlsRecordTypeHandshake, hs[half:])

	ch, err := ReadClientHello(bytes.NewReader(concat(rec1, rec2)))
	if err != nil {
		t.Fatalf("跨记录 ClientHello 解析失败: %v", err)
	}
	if ch.Version != 0x0303 {
		t.Fatalf("Version = 0x%04x，期望 0x0303", ch.Version)
	}
	assertUint16Slice(t, "ciphers", ch.Ciphers, []uint16{0x1301})
	if len(ch.Extensions) != 0 {
		t.Fatalf("无扩展时应为空，实际 %v", ch.Extensions)
	}
	if !bytes.Equal(ch.Raw, concat(rec1, rec2)) {
		t.Fatalf("Raw 应包含两条记录，实际 %d 字节", len(ch.Raw))
	}
}

func TestReadClientHelloRejectsNonHandshake(t *testing.T) {
	record := concat([]byte{0x17, 0x03, 0x03, 0x00, 0x01}, []byte{0x00})
	if _, err := ReadClientHello(bytes.NewReader(record)); err == nil {
		t.Fatal("应用数据记录不应被当作 ClientHello")
	}
}

// TestTLSInfoJSONShape 守住 JSON schema：ec_point_formats 必须是数字数组而不是 base64，
// 空列表也必须是 []，否则 compare 与外部脚本拿到的结构会在不同输入下变形。
func TestTLSInfoJSONShape(t *testing.T) {
	full, err := json.Marshal((&ClientHello{
		Version:      0x0303,
		PointFormats: []uint8{0},
		Ciphers:      []uint16{0x1301},
	}).toTLSInfo())
	if err != nil {
		t.Fatalf("序列化 TLSInfo 失败: %v", err)
	}
	if !strings.Contains(string(full), `"ec_point_formats":[0]`) {
		t.Fatalf("ec_point_formats 应为数字数组，实际: %s", full)
	}
	empty, err := json.Marshal((&ClientHello{Version: 0x0303}).toTLSInfo())
	if err != nil {
		t.Fatalf("序列化空 TLSInfo 失败: %v", err)
	}
	for _, want := range []string{`"alpn":[]`, `"ciphers":[]`, `"ec_point_formats":[]`, `"supported_versions":[]`} {
		if !strings.Contains(string(empty), want) {
			t.Fatalf("空列表应序列化为 %s，实际: %s", want, empty)
		}
	}
	if strings.Contains(string(empty), "null") {
		t.Fatalf("不应出现 null 字段: %s", empty)
	}
}

// TestServeRecordsClientHelloOnALPNMismatch 覆盖 serve 的退化路径：
// 客户端只支持 h2、服务端只声明 http/1.1 时握手会失败，但 ClientHello 仍要落盘。
func TestServeRecordsClientHelloOnALPNMismatch(t *testing.T) {
	cert, err := generateSelfSignedCert()
	if err != nil {
		t.Fatalf("生成自签证书失败: %v", err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听失败: %v", err)
	}
	defer func() { _ = ln.Close() }()

	var out bytes.Buffer
	writer := &captureWriter{buf: bufio.NewWriter(&out)}
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		handleServeConn(conn, &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
			NextProtos:   []string{"http/1.1"},
		}, false, writer, "")
	}()

	rawConn, err := net.DialTimeout("tcp", ln.Addr().String(), 3*time.Second)
	if err != nil {
		t.Fatalf("连接本地监听失败: %v", err)
	}
	defer func() { _ = rawConn.Close() }()
	_ = rawConn.SetDeadline(time.Now().Add(3 * time.Second))
	client := tls.Client(rawConn, &tls.Config{
		InsecureSkipVerify: true,
		NextProtos:         []string{"h2"}, // 与服务端声明的 http/1.1 无交集
	})
	if err := client.Handshake(); err == nil {
		t.Fatal("ALPN 无交集时客户端握手应当失败")
	}
	<-done

	line := strings.TrimSpace(out.String())
	if line == "" {
		t.Fatal("ALPN 不匹配时也应记录 ClientHello")
	}
	var cap Capture
	if err := json.Unmarshal([]byte(line), &cap); err != nil {
		t.Fatalf("记录不是合法 JSON: %v（%s）", err, line)
	}
	if cap.TLS == nil || cap.TLS.JA3 == "" || cap.TLS.JA3MD5 == "" {
		t.Fatalf("应记录完整的 TLS 指纹，实际: %+v", cap.TLS)
	}
	if cap.HTTP1 != nil || cap.HTTP2 != nil {
		t.Fatalf("握手未完成时不应有应用层字段: http1=%+v http2=%+v", cap.HTTP1, cap.HTTP2)
	}
}

// ---- JA3 ----

func TestJA3EmptyLists(t *testing.T) {
	ch := &ClientHello{Version: 0x0301}
	if got, want := JA3String(ch), "769,,,,"; got != want {
		t.Fatalf("JA3 = %q，期望 %q", got, want)
	}
}

func TestJA3MD5SumKnownVector(t *testing.T) {
	const want = "900150983cd24fb0d6963f7d28e17f72" // md5("abc")
	if got := JA3MD5Sum("abc"); got != want {
		t.Fatalf("JA3MD5Sum(\"abc\") = %q，期望 %q", got, want)
	}
}

// ---- HTTP/2 ----

func TestReadHTTP2SettingsAndHeadersOrder(t *testing.T) {
	want := [][2]string{
		{":method", "POST"},
		{":path", "/v1/responses"},
		{"user-agent", "codex_cli_rs/0.20.0"},
		{"x-stainless-lang", "go"},
		{"accept", "*/*"},
	}
	var hbuf bytes.Buffer
	enc := hpack.NewEncoder(&hbuf)
	for _, kv := range want {
		if err := enc.WriteField(hpack.HeaderField{Name: kv[0], Value: kv[1]}); err != nil {
			t.Fatalf("HPACK 编码失败: %v", err)
		}
	}
	block := hbuf.Bytes()
	cut := len(block) / 2

	settingsPayload := concat(
		be16(1), be32(65536), // HEADER_TABLE_SIZE
		be16(4), be32(1048576), // INITIAL_WINDOW_SIZE
		be16(5), be32(16384), // MAX_FRAME_SIZE
	)
	stream := concat(
		[]byte(h2Preface),
		h2FrameBytes(h2FrameSettings, 0, 0, settingsPayload),
		h2FrameBytes(h2FrameSettings, h2FlagSettingsAck, 0, nil),
		h2FrameBytes(h2FrameHeaders, h2FlagEndStream, 1, block[:cut]),
		h2FrameBytes(h2FrameContinuation, h2FlagEndHeaders, 1, block[cut:]),
	)

	info, err := ReadHTTP2(bytes.NewReader(stream))
	if err != nil {
		t.Fatalf("ReadHTTP2 失败: %v", err)
	}
	wantSettings := []H2Setting{{ID: 1, Value: 65536}, {ID: 4, Value: 1048576}, {ID: 5, Value: 16384}}
	if len(info.Settings) != len(wantSettings) {
		t.Fatalf("settings 数量 = %d，期望 %d（%v）", len(info.Settings), len(wantSettings), info.Settings)
	}
	for i := range wantSettings {
		if info.Settings[i] != wantSettings[i] {
			t.Fatalf("settings[%d] = %+v，期望 %+v", i, info.Settings[i], wantSettings[i])
		}
	}
	if len(info.Headers) != len(want) {
		t.Fatalf("header 数量 = %d，期望 %d（%v）", len(info.Headers), len(want), info.Headers)
	}
	for i := range want {
		if info.Headers[i].Name != want[i][0] || info.Headers[i].Value != want[i][1] {
			t.Fatalf("headers[%d] = %+v，期望 %s=%s", i, info.Headers[i], want[i][0], want[i][1])
		}
	}
}

func TestReadHTTP2PaddedPriorityHeaders(t *testing.T) {
	var hbuf bytes.Buffer
	enc := hpack.NewEncoder(&hbuf)
	if err := enc.WriteField(hpack.HeaderField{Name: ":method", Value: "GET"}); err != nil {
		t.Fatalf("HPACK 编码失败: %v", err)
	}
	block := hbuf.Bytes()
	// PADDED + PRIORITY：1 字节 pad 长度 + 5 字节优先级 + 头块 + 4 字节 padding。
	payload := concat([]byte{4}, be32(0), []byte{0}, block, bytes.Repeat([]byte{0}, 4))
	stream := concat(
		[]byte(h2Preface),
		h2FrameBytes(h2FrameHeaders, h2FlagEndHeaders|h2FlagPadded|h2FlagPriority, 1, payload),
	)
	info, err := ReadHTTP2(bytes.NewReader(stream))
	if err != nil {
		t.Fatalf("带 padding/priority 的 HEADERS 解析失败: %v", err)
	}
	if len(info.Headers) != 1 || info.Headers[0].Name != ":method" || info.Headers[0].Value != "GET" {
		t.Fatalf("headers = %v，期望只有 :method=GET", info.Headers)
	}
}

func TestReadHTTP2RejectsBadPreface(t *testing.T) {
	stream := concat([]byte("GET / HTTP/1.1\r\n\r\n"), bytes.Repeat([]byte{0}, 16))
	if _, err := ReadHTTP2(bytes.NewReader(stream)); err == nil {
		t.Fatal("非 h2 preface 应当报错")
	}
}

// ---- HTTP/1.1 ----

func TestReadHTTP1KeepsOrderAndCase(t *testing.T) {
	body := `{"model":"gpt-5"}`
	raw := "POST /v1/responses?stream=true HTTP/1.1\r\n" +
		"Host: api.example.com\r\n" +
		"User-Agent: Codex/1.0\r\n" +
		"X-Stainless-Lang: go\r\n" +
		"x-lower-name: yes\r\n" +
		"X-Spaced:   padded-value   \r\n" +
		"\r\n" + body
	br := bufio.NewReader(strings.NewReader(raw))
	info, err := ReadHTTP1(br)
	if err != nil {
		t.Fatalf("ReadHTTP1 失败: %v", err)
	}
	if info.Method != "POST" {
		t.Fatalf("method = %q，期望 POST", info.Method)
	}
	if info.Path != "/v1/responses?stream=true" {
		t.Fatalf("path = %q", info.Path)
	}
	wantNames := []string{"Host", "User-Agent", "X-Stainless-Lang", "x-lower-name", "X-Spaced"}
	if len(info.Headers) != len(wantNames) {
		t.Fatalf("header 数量 = %d，期望 %d（%v）", len(info.Headers), len(wantNames), info.Headers)
	}
	for i, name := range wantNames {
		if info.Headers[i].Name != name {
			t.Fatalf("headers[%d].Name = %q，期望 %q（顺序与大小写都要保留）", i, info.Headers[i].Name, name)
		}
	}
	if got, want := info.Headers[4].Value, "padded-value"; got != want {
		t.Fatalf("头值 = %q，期望去掉首尾空白后的 %q", got, want)
	}
	// body 必须原样留在 reader 里。
	rest, err := io.ReadAll(br)
	if err != nil {
		t.Fatalf("读取剩余 body 失败: %v", err)
	}
	if string(rest) != body {
		t.Fatalf("剩余 body = %q，期望 %q", string(rest), body)
	}
}

func TestReadHTTP1RejectsBadRequestLine(t *testing.T) {
	br := bufio.NewReader(strings.NewReader("NONSENSE\r\n\r\n"))
	if _, err := ReadHTTP1(br); err == nil {
		t.Fatal("非法请求行应当报错")
	}
}

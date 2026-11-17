package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// 测试基线：与 fpcap 输出的 JSON 结构一致。
const captureA = `{
  "source": "peek 127.0.0.1:5000 -> api.example.com:443",
  "captured_at": "2026-11-17T00:00:00Z",
  "tls": {
    "version": "0x0303",
    "sni": "api.example.com",
    "alpn": ["h2", "http/1.1"],
    "ciphers": [4865, 4866, 49199],
    "extensions": [0, 10, 11, 13, 16, 43, 51],
    "curves": [29, 23],
    "sigalgs": [2052, 2053],
    "supported_versions": ["0x0304", "0x0303"],
    "key_share_groups": [29],
    "ec_point_formats": [0],
    "ja3": "771,4865-4866-49199,0-10-11-13-16-43-51,29-23,0",
    "ja3_md5": "0f4c9b1c0d5a5d5d1f0b0a09"
  },
  "http1": {
    "method": "POST",
    "path": "/v1/responses",
    "headers": [
      {"name": "Host", "value": "api.example.com"},
      {"name": "User-Agent", "value": "Codex/1.0"},
      {"name": "Authorization", "value": "Bearer AAA"},
      {"name": "Date", "value": "Mon, 17 Nov 2026 00:00:00 GMT"},
      {"name": "Accept", "value": "*/*"}
    ]
  }
}`

// 仅 volatile 取值不同：应当判定为一致。
const captureAVolatileOnly = `{
  "source": "peek 127.0.0.1:5000 -> api.example.com:443",
  "captured_at": "2026-11-17T00:00:01Z",
  "tls": {
    "version": "0x0303",
    "sni": "api.example.com",
    "alpn": ["h2", "http/1.1"],
    "ciphers": [4865, 4866, 49199],
    "extensions": [0, 10, 11, 13, 16, 43, 51],
    "curves": [29, 23],
    "sigalgs": [2052, 2053],
    "supported_versions": ["0x0304", "0x0303"],
    "key_share_groups": [29],
    "ec_point_formats": [0],
    "ja3": "771,4865-4866-49199,0-10-11-13-16-43-51,29-23,0",
    "ja3_md5": "0f4c9b1c0d5a5d5d1f0b0a09"
  },
  "http1": {
    "method": "POST",
    "path": "/v1/responses",
    "headers": [
      {"name": "Host", "value": "api.example.com"},
      {"name": "User-Agent", "value": "Codex/1.0"},
      {"name": "Authorization", "value": "Bearer BBB"},
      {"name": "Date", "value": "Tue, 18 Nov 2026 03:04:05 GMT"},
      {"name": "Accept", "value": "*/*"}
    ]
  }
}`

// 指纹层面存在真实差异：ja3/alpn/cipher 顺序/extension 顺序/curves/sigalgs/头顺序/头值。
const captureBDiff = `{
  "source": "gateway outbound",
  "captured_at": "2026-11-17T00:00:02Z",
  "tls": {
    "version": "0x0303",
    "sni": "api.example.com",
    "alpn": ["http/1.1", "h2"],
    "ciphers": [4866, 4865, 49199],
    "extensions": [0, 11, 10, 13, 16, 43, 51],
    "curves": [23, 29],
    "sigalgs": [2053, 2052],
    "supported_versions": ["0x0304", "0x0303"],
    "key_share_groups": [29],
    "ec_point_formats": [0],
    "ja3": "771,4866-4865-49199,0-11-10-13-16-43-51,23-29,0",
    "ja3_md5": "ffffffffffffffffffffffffffffffff"
  },
  "http1": {
    "method": "POST",
    "path": "/v1/responses",
    "headers": [
      {"name": "Host", "value": "api.example.com"},
      {"name": "Accept", "value": "*/*"},
      {"name": "User-Agent", "value": "Go-http-client/1.1"},
      {"name": "Authorization", "value": "Bearer AAA"},
      {"name": "Date", "value": "Mon, 17 Nov 2026 00:00:00 GMT"}
    ]
  }
}`

const captureH2A = `{
  "source": "serve h2 A",
  "captured_at": "2026-11-17T00:00:03Z",
  "tls": {"version": "0x0303", "sni": "api.example.com", "alpn": ["h2"]},
  "http2": {
    "settings": [{"id": 1, "value": 65536}, {"id": 4, "value": 65535}],
    "headers": [
      {"name": ":method", "value": "POST"},
      {"name": ":path", "value": "/x"},
      {"name": "user-agent", "value": "A"}
    ]
  }
}`

const captureH2B = `{
  "source": "serve h2 B",
  "captured_at": "2026-11-17T00:00:04Z",
  "tls": {"version": "0x0303", "sni": "api.example.com", "alpn": ["h2"]},
  "http2": {
    "settings": [{"id": 1, "value": 4096}, {"id": 4, "value": 65535}],
    "headers": [
      {"name": ":method", "value": "POST"},
      {"name": ":path", "value": "/x"},
      {"name": "user-agent", "value": "A"},
      {"name": "accept", "value": "*/*"}
    ]
  }
}`

func mustCapture(t *testing.T, raw string) *Capture {
	t.Helper()
	var c Capture
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		t.Fatalf("解析测试基线失败: %v", err)
	}
	return &c
}

// diffFields 把差异按字段名索引，便于按需断言。
func diffFields(rep CompareReport) map[string]Diff {
	out := make(map[string]Diff, len(rep.Diffs))
	for _, d := range rep.Diffs {
		out[d.Field] = d
	}
	return out
}

func diffFieldNames(rep CompareReport) []string {
	names := make([]string, 0, len(rep.Diffs))
	for _, d := range rep.Diffs {
		names = append(names, d.Field)
	}
	sort.Strings(names)
	return names
}

func requireDiff(t *testing.T, fields map[string]Diff, field string) Diff {
	t.Helper()
	d, ok := fields[field]
	if !ok {
		t.Fatalf("缺少差异字段 %q，实际差异字段：%v", field, sortedKeys(fields))
	}
	return d
}

func sortedKeys(m map[string]Diff) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func TestCompareEqualIgnoresVolatileValues(t *testing.T) {
	a := mustCapture(t, captureA)
	b := mustCapture(t, captureAVolatileOnly)

	rep := CompareCaptures(a, b)
	if !rep.Equal {
		t.Fatalf("应判定为一致，实际差异：%v", diffFieldNames(rep))
	}
	if len(rep.Diffs) != 0 {
		t.Fatalf("不应有差异，实际 %+v", rep.Diffs)
	}
	if len(rep.Ignored) == 0 {
		t.Fatal("应记录被忽略的 volatile 头")
	}
	joined := strings.Join(rep.Ignored, "\n")
	for _, want := range []string{"http1.header.authorization", "http1.header.date"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("已忽略项缺少 %q，实际：%v", want, rep.Ignored)
		}
	}
}

func TestCompareReportsFingerprintDiffs(t *testing.T) {
	rep := CompareCaptures(mustCapture(t, captureA), mustCapture(t, captureBDiff))
	if rep.Equal {
		t.Fatal("指纹不同时不应判定为一致")
	}
	fields := diffFields(rep)
	for _, want := range []string{
		"tls.ja3", "tls.alpn", "tls.ciphers", "tls.extensions",
		"tls.curves", "tls.sigalgs",
		"http1.headers.order", "http1.header.user-agent",
	} {
		requireDiff(t, fields, want)
	}
	// 顺序差异要能看见具体列表，而不是只有 ja3 一句话。
	ja3Diff := requireDiff(t, fields, "tls.ja3")
	if ja3Diff.A == ja3Diff.B {
		t.Fatalf("ja3 差异两侧相同: %+v", ja3Diff)
	}
	cipherDiff := requireDiff(t, fields, "tls.ciphers")
	if cipherDiff.A != "4865, 4866, 49199" || cipherDiff.B != "4866, 4865, 49199" {
		t.Fatalf("cipher 差异渲染异常: %+v", cipherDiff)
	}
	// 未变化的字段不应被列入。
	if _, ok := fields["tls.sni"]; ok {
		t.Fatal("SNI 相同，不应产生差异")
	}
	if _, ok := fields["http1.method"]; ok {
		t.Fatal("method 相同，不应产生差异")
	}
}

func TestCompareHTTP2SettingsAndHeaderOrder(t *testing.T) {
	rep := CompareCaptures(mustCapture(t, captureH2A), mustCapture(t, captureH2B))
	if rep.Equal {
		t.Fatal("SETTINGS 与头列表不同时不应判定为一致")
	}
	fields := diffFields(rep)
	settingsDiff := requireDiff(t, fields, "http2.settings")
	if !strings.Contains(settingsDiff.A, "1:65536") || !strings.Contains(settingsDiff.B, "1:4096") {
		t.Fatalf("settings 差异渲染异常: %+v", settingsDiff)
	}
	orderDiff := requireDiff(t, fields, "http2.headers.order")
	if strings.Contains(orderDiff.A, "accept") || !strings.Contains(orderDiff.B, "accept") {
		t.Fatalf("头名顺序差异应体现 accept 的有无: %+v", orderDiff)
	}
	acceptDiff := requireDiff(t, fields, "http2.header.accept")
	if acceptDiff.A != absentValue || acceptDiff.B != "*/*" {
		t.Fatalf("A 侧缺 accept 时应显示 %q/%q，实际 %+v", absentValue, "*/*", acceptDiff)
	}
	// 两边都有的头且值相同，不应报差异。
	if _, ok := fields["http2.header.user-agent"]; ok {
		t.Fatal("user-agent 相同，不应产生差异")
	}
}

func TestCompareVolatilePresenceStillReported(t *testing.T) {
	a := mustCapture(t, captureA)
	b := mustCapture(t, captureA)
	b.HTTP1.Headers = removeHeader(b.HTTP1.Headers, "authorization")

	rep := CompareCaptures(a, b)
	if rep.Equal {
		t.Fatal("volatile 头出现与否不同，应判定有差异")
	}
	d := requireDiff(t, diffFields(rep), "http1.header.authorization")
	if d.A != ignoredValue || d.B != absentValue {
		t.Fatalf("volatile 差异应为 %q/%q，实际 %+v", ignoredValue, absentValue, d)
	}
}

func TestCompareHTTPProtocolMismatch(t *testing.T) {
	a := mustCapture(t, captureA)
	b := mustCapture(t, captureH2A)
	rep := CompareCaptures(a, b)
	if rep.Equal {
		t.Fatal("一侧是 http1、另一侧是 http2 时应判定有差异")
	}
	d := requireDiff(t, diffFields(rep), "http.protocol")
	if d.A != "http1" || d.B != "http2" {
		t.Fatalf("协议差异应为 http1/http2，实际 %+v", d)
	}
}

func TestCompareTLSMissing(t *testing.T) {
	a := mustCapture(t, captureA)
	b := &Capture{Source: "peek 无 TLS"}
	rep := CompareCaptures(a, b)
	if rep.Equal {
		t.Fatal("一侧缺少 tls 字段时应判定有差异")
	}
	if _, ok := diffFields(rep)["tls"]; !ok {
		t.Fatalf("应报告 tls 缺失，实际：%v", diffFieldNames(rep))
	}
}

// TestReadCaptureAcceptsBOMAndJSONLines 覆盖 compare 的读入路径：
// 带 UTF-8 BOM 的文件、以及多行 JSON Lines（取第一条记录）都要能读。
func TestReadCaptureAcceptsBOMAndJSONLines(t *testing.T) {
	dir := t.TempDir()
	bomPath := filepath.Join(dir, "bom.json")
	if err := os.WriteFile(bomPath, append([]byte{0xEF, 0xBB, 0xBF}, []byte(captureA)...), 0o644); err != nil {
		t.Fatalf("写入带 BOM 的基线失败: %v", err)
	}
	if _, err := readCapture(bomPath); err != nil {
		t.Fatalf("带 BOM 的基线应能读取: %v", err)
	}

	linesPath := filepath.Join(dir, "lines.jsonl")
	// fpcap 写出的是紧凑单行 JSON，这里按同样形态拼两条记录。
	first, err := json.Marshal(mustCapture(t, captureA))
	if err != nil {
		t.Fatalf("序列化第一条记录失败: %v", err)
	}
	second, err := json.Marshal(mustCapture(t, captureBDiff))
	if err != nil {
		t.Fatalf("序列化第二条记录失败: %v", err)
	}
	if err := os.WriteFile(linesPath, []byte(string(first)+"\n"+string(second)+"\n"), 0o644); err != nil {
		t.Fatalf("写入 JSON Lines 失败: %v", err)
	}
	got, err := readCapture(linesPath)
	if err != nil {
		t.Fatalf("JSON Lines 应能读取: %v", err)
	}
	if got.TLS == nil || got.TLS.JA3 != mustCapture(t, captureA).TLS.JA3 {
		t.Fatalf("JSON Lines 应取第一条记录，实际 %+v", got.TLS)
	}
}

// removeHeader 删除指定（大小写不敏感）名字的所有头，用于构造 volatile 缺失场景。
func removeHeader(fields []HeaderField, name string) []HeaderField {
	out := make([]HeaderField, 0, len(fields))
	for _, f := range fields {
		if strings.EqualFold(f.Name, name) {
			continue
		}
		out = append(out, f)
	}
	return out
}

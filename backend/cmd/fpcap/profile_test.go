package main

import (
	"strings"
	"testing"
)

// 基线：抓包里出现 GREASE 时必须剔除，且其余顺序不变。
func TestBuildProfileStripsGREASE(t *testing.T) {
	tls := &TLSInfo{
		Version:           "0x0303",
		ALPN:              []string{"h2", "http/1.1"},
		Ciphers:           []uint16{0x0a0a, 4865, 4866},
		Extensions:        []uint16{0x1a1a, 0, 10, 43, 51, 65281},
		Curves:            []uint16{0x2a2a, 29},
		SigAlgs:           []uint16{0x3a3a, 2052, 2053},
		SupportedVersions: []string{"0x0304", "0x0303"},
		KeyShareGroups:    []uint16{29},
		PointFormats:      []int{0},
	}
	p, err := buildProfile("codex-test", tls)
	if err != nil {
		t.Fatalf("buildProfile: %v", err)
	}
	if want := []uint16{4865, 4866}; !equalUint16(p.CipherSuites, want) {
		t.Fatalf("ciphers = %v, want %v", p.CipherSuites, want)
	}
	if want := []uint16{0, 10, 43, 51, 65281}; !equalUint16(p.Extensions, want) {
		t.Fatalf("extensions = %v, want %v", p.Extensions, want)
	}
	if want := []uint16{29}; !equalUint16(p.Curves, want) {
		t.Fatalf("curves = %v, want %v", p.Curves, want)
	}
	if want := []uint16{2052, 2053}; !equalUint16(p.SignatureAlgorithms, want) {
		t.Fatalf("sigalgs = %v, want %v", p.SignatureAlgorithms, want)
	}
	if want := []uint16{772, 771}; !equalUint16(p.SupportedVersions, want) {
		t.Fatalf("supported versions = %v, want %v", p.SupportedVersions, want)
	}
	if want := []uint16{0}; !equalUint16(p.PointFormats, want) {
		t.Fatalf("point formats = %v, want %v", p.PointFormats, want)
	}
	if p.EnableGREASE {
		t.Fatal("profile 不应默认启用 GREASE")
	}
	if !strings.Contains(strings.Join(p.Notes, " "), "GREASE") {
		t.Fatalf("缺少 GREASE 提示: %v", p.Notes)
	}
}

// 未支持的扩展要显式提示"出站只能空数据"，不能静默当成精确复刻。
func TestBuildProfileNotesUnknownExtension(t *testing.T) {
	tls := &TLSInfo{Extensions: []uint16{0, 0x1234}, SupportedVersions: []string{"0x0304"}}
	p, err := buildProfile("x", tls)
	if err != nil {
		t.Fatalf("buildProfile: %v", err)
	}
	joined := strings.Join(p.Notes, " ")
	if !strings.Contains(joined, "4660") {
		t.Fatalf("未知扩展 0x1234 应有提示，notes=%v", p.Notes)
	}
}

// Go 字面量必须包含关键字段，便于直接粘进代码。
func TestGoLiteralContainsFields(t *testing.T) {
	p := &profileOutput{
		Name:              "codex",
		CipherSuites:      []uint16{4865, 4866},
		Curves:            []uint16{29},
		PointFormats:      []uint16{0},
		SupportedVersions: []uint16{772},
		KeyShareGroups:    []uint16{29},
		Extensions:        []uint16{0, 43},
		ALPNProtocols:     []string{"h2"},
	}
	lit := goLiteral(p)
	for _, want := range []string{"Name:", "CipherSuites:", "[]uint16{4865, 4866}", `[]string{"h2"}`, "ALPNProtocols:"} {
		if !strings.Contains(lit, want) {
			t.Fatalf("literal 缺少 %q:\n%s", want, lit)
		}
	}
}

// 非 0x 前缀与坏输入都要有明确行为：合法值接受，非法值报错。
func TestParseSupportedVersions(t *testing.T) {
	got, err := parseSupportedVersionStrings([]string{"0x0304", "0303"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if want := []uint16{772, 771}; !equalUint16(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if _, err := parseSupportedVersionStrings([]string{"zz"}); err == nil {
		t.Fatal("非法版本应报错")
	}
}

// 缺少 tls 段或 name 时必须失败，避免生成空 profile。
func TestBuildProfileRequiresTLSSectionAndName(t *testing.T) {
	if _, err := buildProfile("x", nil); err == nil {
		t.Fatal("缺少 tls 段应报错")
	}
	if _, err := buildProfile("", &TLSInfo{}); err == nil {
		t.Fatal("空 name 应报错")
	}
}

func equalUint16(a, b []uint16) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

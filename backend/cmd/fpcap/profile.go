package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// profileOutput 是 tlsfingerprint.Profile 的可落盘形态：字段名与
// internal/pkg/tlsfingerprint 的 Profile 一一对应，供手工或后续导入使用。
type profileOutput struct {
	Name                string   `json:"name"`
	EnableGREASE        bool     `json:"enable_grease"`
	CipherSuites        []uint16 `json:"cipher_suites"`
	Curves              []uint16 `json:"curves"`
	PointFormats        []uint16 `json:"point_formats"`
	SignatureAlgorithms []uint16 `json:"signature_algorithms"`
	ALPNProtocols       []string `json:"alpn_protocols"`
	SupportedVersions   []uint16 `json:"supported_versions"`
	KeyShareGroups      []uint16 `json:"key_share_groups"`
	Extensions          []uint16 `json:"extensions"`
	Notes               []string `json:"notes,omitempty"`
}

// knownExtensionIDs 是 tlsfingerprint 里能精确构造的扩展类型；
// 其余扩展会被 GenericExtension（空数据）代替，因此必须显式提示。
var knownExtensionIDs = map[uint16]bool{
	0: true, 5: true, 10: true, 11: true, 13: true, 16: true, 18: true,
	23: true, 35: true, 43: true, 45: true, 50: true, 51: true,
	65037: true, 65281: true,
}

// isGREASE 判断一个 16 位值是否为 GREASE（0x?a?a）。
func isGREASE(v uint16) bool {
	return v&0x0f0f == 0x0a0a && v>>8 == v&0xff
}

// stripGREASE 去掉 GREASE 值，保持其余元素顺序；返回保留值与剔除数量。
func stripGREASE(in []uint16) ([]uint16, int) {
	out := make([]uint16, 0, len(in))
	dropped := 0
	for _, v := range in {
		if isGREASE(v) {
			dropped++
			continue
		}
		out = append(out, v)
	}
	return out, dropped
}

// parseSupportedVersionStrings 把 "0x0304" 形式的字符串还原成 uint16。
func parseSupportedVersionStrings(in []string) ([]uint16, error) {
	out := make([]uint16, 0, len(in))
	for _, raw := range in {
		s := strings.TrimSpace(raw)
		s = strings.TrimPrefix(strings.TrimPrefix(s, "0x"), "0X")
		v, err := strconv.ParseUint(s, 16, 16)
		if err != nil {
			return nil, fmt.Errorf("解析 supported_versions %q: %w", raw, err)
		}
		out = append(out, uint16(v))
	}
	return out, nil
}

// buildProfile 把一次 TLS 观察结果转换成 profile；不做任何"补全"或猜测。
func buildProfile(name string, tls *TLSInfo) (*profileOutput, error) {
	if tls == nil {
		return nil, fmt.Errorf("capture 中没有 tls 段")
	}
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("-name 不能为空")
	}
	out := &profileOutput{Name: name}

	var droppedCiphers, droppedExts, droppedCurves, droppedSigAlgs int
	out.CipherSuites, droppedCiphers = stripGREASE(tls.Ciphers)
	out.Extensions, droppedExts = stripGREASE(tls.Extensions)
	out.Curves, droppedCurves = stripGREASE(tls.Curves)
	out.SignatureAlgorithms, droppedSigAlgs = stripGREASE(tls.SigAlgs)
	out.KeyShareGroups, _ = stripGREASE(tls.KeyShareGroups)

	for _, pf := range tls.PointFormats {
		if pf < 0 || pf > 255 {
			continue
		}
		out.PointFormats = append(out.PointFormats, uint16(pf))
	}

	versions, err := parseSupportedVersionStrings(tls.SupportedVersions)
	if err != nil {
		return nil, err
	}
	out.SupportedVersions = versions
	out.ALPNProtocols = append([]string(nil), tls.ALPN...)

	if dropped := droppedCiphers + droppedExts + droppedCurves + droppedSigAlgs; dropped > 0 {
		out.Notes = append(out.Notes, fmt.Sprintf(
			"抓包含 %d 个 GREASE 值，已按标准指纹算法剔除；profile 默认不启用 GREASE", dropped))
	}
	for _, id := range out.Extensions {
		if !knownExtensionIDs[id] {
			out.Notes = append(out.Notes, fmt.Sprintf(
				"扩展 %d 不能精确构造，出站会以 GenericExtension(空数据) 代替，需实测服务端是否接受", id))
		}
	}
	if len(out.ALPNProtocols) == 0 {
		out.Notes = append(out.Notes, "抓包未包含 ALPN；请在客户端真正协商 h2/http1 后再采集")
	}
	return out, nil
}

// formatUint16List 输出 Go 字面量里的 []uint16{...}。
func formatUint16List(values []uint16) string {
	parts := make([]string, 0, len(values))
	for _, v := range values {
		parts = append(parts, strconv.FormatUint(uint64(v), 10))
	}
	return "[]uint16{" + strings.Join(parts, ", ") + "}"
}

// goLiteral 输出可直接粘进 Go 代码的 Profile 字面量。
func goLiteral(p *profileOutput) string {
	var b strings.Builder
	_, _ = b.WriteString("&tlsfingerprint.Profile{\n")
	fmt.Fprintf(&b, "\tName:                %q,\n", p.Name)
	fmt.Fprintf(&b, "\tCipherSuites:        %s,\n", formatUint16List(p.CipherSuites))
	fmt.Fprintf(&b, "\tCurves:              %s,\n", formatUint16List(p.Curves))
	fmt.Fprintf(&b, "\tPointFormats:        %s,\n", formatUint16List(p.PointFormats))
	fmt.Fprintf(&b, "\tSignatureAlgorithms: %s,\n", formatUint16List(p.SignatureAlgorithms))
	fmt.Fprintf(&b, "\tSupportedVersions:   %s,\n", formatUint16List(p.SupportedVersions))
	fmt.Fprintf(&b, "\tKeyShareGroups:      %s,\n", formatUint16List(p.KeyShareGroups))
	fmt.Fprintf(&b, "\tExtensions:          %s,\n", formatUint16List(p.Extensions))
	alpn := make([]string, 0, len(p.ALPNProtocols))
	for _, v := range p.ALPNProtocols {
		alpn = append(alpn, strconv.Quote(v))
	}
	fmt.Fprintf(&b, "\tALPNProtocols:       []string{%s},\n", strings.Join(alpn, ", "))
	fmt.Fprintf(&b, "\tEnableGREASE:        %v,\n", p.EnableGREASE)
	_, _ = b.WriteString("}")
	return b.String()
}

// runProfile 从一份抓包生成 tlsfingerprint.Profile（JSON 或 Go 字面量）。
func runProfile(args []string) {
	fs := flag.NewFlagSet("profile", flag.ExitOnError)
	in := fs.String("in", "", "抓包文件（JSON Lines，取第一条非空记录）")
	name := fs.String("name", "", "profile 名称，例如 codex_cli_rs_0.153.4")
	out := fs.String("out", "", "输出文件，空表示 stdout")
	goOut := fs.Bool("go", false, "输出 Go 字面量而不是 JSON")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, "用法：fpcap profile -in capture.jsonl -name NAME [-out profile.json] [-go]\n\n")
		fs.PrintDefaults()
	}
	_ = fs.Parse(args)
	if *in == "" || *name == "" {
		fmt.Fprintln(os.Stderr, "profile 需要 -in 与 -name")
		fs.Usage()
		os.Exit(2)
	}
	cap, err := readCapture(*in)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(2)
	}
	profile, err := buildProfile(*name, cap.TLS)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(2)
	}

	var payload []byte
	if *goOut {
		payload = []byte(goLiteral(profile) + "\n")
	} else {
		payload, err = json.MarshalIndent(profile, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "编码 profile: %v\n", err)
			os.Exit(2)
		}
		payload = append(payload, '\n')
	}

	if *out == "" {
		_, _ = os.Stdout.Write(payload)
		return
	}
	if err := os.WriteFile(*out, payload, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "写入 %s: %v\n", *out, err)
		os.Exit(2)
	}
	fmt.Fprintf(os.Stderr, "已写入 %s\n", *out)
}

// compare.go 逐字段对比两份基线，输出差异与被忽略的 volatile 头。
//
// 对比范围：ja3、alpn、cipher 列表、extension ID 顺序、curves、sigalgs、SNI、
// h2 SETTINGS、头名顺序、头取值；另外比较 h1 请求行的 method/path，
// 避免两份不同端点的捕获被误判为一致。
// date/x-request-id/authorization/cookie 等天然不稳定的头只比“是否出现”，
// 取值一律按 <ignored> 处理并记入“已忽略”。
package main

import (
	"fmt"
	"strings"
)

// volatileHeaderNames 是取值不参与比较的头（键为小写）。
var volatileHeaderNames = map[string]struct{}{
	"date":               {},
	"x-request-id":       {},
	"authorization":      {},
	"cookie":             {},
	"x-codex-turn-state": {},
	"session_id":         {},
	"conversation_id":    {},
	"prompt_cache_key":   {},
}

const (
	ignoredValue = "<ignored>"
	absentValue  = "<absent>"
	emptyLabel   = "(空)"
)

// Diff 是一处字段差异；A/B 为两边的可读表示。
type Diff struct {
	Field string `json:"field"`
	A     string `json:"a"`
	B     string `json:"b"`
}

// CompareReport 是 compare 模式的完整结果。
type CompareReport struct {
	Equal   bool     `json:"equal"`
	Diffs   []Diff   `json:"diffs"`
	Ignored []string `json:"ignored"`
}

// CompareCaptures 逐字段比较两份基线；完全一致时 Equal 为 true。
func CompareCaptures(a, b *Capture) CompareReport {
	rep := CompareReport{Diffs: []Diff{}, Ignored: []string{}}
	compareTLS(&rep, a.TLS, b.TLS)
	compareHTTP(&rep, a, b)
	rep.Equal = len(rep.Diffs) == 0
	return rep
}

// compareTLS 比较 ClientHello 层面的指纹原料。
func compareTLS(rep *CompareReport, a, b *TLSInfo) {
	if a == nil || b == nil {
		if a != nil || b != nil {
			rep.Diffs = append(rep.Diffs, Diff{
				Field: "tls",
				A:     presenceLabel(a != nil),
				B:     presenceLabel(b != nil),
			})
		}
		return
	}
	// ja3 已覆盖 cipher/extension/curve/point format 的顺序，单独列出便于定位；
	// 下面几项分别比较，差异才能指名到具体列表。
	addDiff(rep, "tls.ja3", a.JA3, b.JA3)
	addDiff(rep, "tls.sni", a.SNI, b.SNI)
	addDiff(rep, "tls.alpn", formatStringList(a.ALPN), formatStringList(b.ALPN))
	addDiff(rep, "tls.ciphers", formatNumberList(a.Ciphers), formatNumberList(b.Ciphers))
	addDiff(rep, "tls.extensions", formatNumberList(a.Extensions), formatNumberList(b.Extensions))
	addDiff(rep, "tls.curves", formatNumberList(a.Curves), formatNumberList(b.Curves))
	addDiff(rep, "tls.sigalgs", formatNumberList(a.SigAlgs), formatNumberList(b.SigAlgs))
}

// compareHTTP 比较应用层：协议类型、h1 请求行、SETTINGS、头名顺序与头取值。
func compareHTTP(rep *CompareReport, a, b *Capture) {
	if a.HTTP1 != nil && b.HTTP1 != nil {
		addDiff(rep, "http1.method", a.HTTP1.Method, b.HTTP1.Method)
		addDiff(rep, "http1.path", a.HTTP1.Path, b.HTTP1.Path)
	}
	if a.HTTP2 != nil && b.HTTP2 != nil {
		addDiff(rep, "http2.settings", formatSettings(a.HTTP2.Settings), formatSettings(b.HTTP2.Settings))
	}
	protoA, fieldsA := captureHeaders(a)
	protoB, fieldsB := captureHeaders(b)
	if protoA != protoB {
		if protoA != "" || protoB != "" {
			rep.Diffs = append(rep.Diffs, Diff{
				Field: "http.protocol",
				A:     presenceOr(protoA),
				B:     presenceOr(protoB),
			})
		}
		return
	}
	if protoA == "" {
		return
	}
	addDiff(rep, protoA+".headers.order", formatHeaderNames(fieldsA), formatHeaderNames(fieldsB))
	compareHeaderValues(rep, protoA, fieldsA, fieldsB)
}

// captureHeaders 取出可对比的头列表：优先 HTTP/2 的 HEADERS，其次 HTTP/1.1。
func captureHeaders(c *Capture) (string, []HeaderField) {
	if c.HTTP2 != nil && len(c.HTTP2.Headers) > 0 {
		return "http2", c.HTTP2.Headers
	}
	if c.HTTP1 != nil {
		return "http1", c.HTTP1.Headers
	}
	return "", nil
}

// compareHeaderValues 逐个头比较取值；volatile 头只比较是否出现。
func compareHeaderValues(rep *CompareReport, proto string, a, b []HeaderField) {
	valuesA := headerValues(a)
	valuesB := headerValues(b)
	reported := make(map[string]struct{}, len(rep.Ignored))
	for _, name := range headerNameUnion(a, b) {
		field := proto + ".header." + name
		valsA, hasA := valuesA[name]
		valsB, hasB := valuesB[name]
		if _, isVolatile := volatileHeaderNames[name]; isVolatile {
			if hasA != hasB {
				rep.Diffs = append(rep.Diffs, Diff{
					Field: field,
					A:     volatileLabel(hasA),
					B:     volatileLabel(hasB),
				})
			}
			note := field + ": 值已忽略"
			if _, dup := reported[note]; !dup {
				reported[note] = struct{}{}
				rep.Ignored = append(rep.Ignored, note)
			}
			continue
		}
		if !hasA || !hasB {
			rep.Diffs = append(rep.Diffs, Diff{
				Field: field,
				A:     valuesLabel(hasA, valsA),
				B:     valuesLabel(hasB, valsB),
			})
			continue
		}
		if !equalStringSlices(valsA, valsB) {
			rep.Diffs = append(rep.Diffs, Diff{
				Field: field,
				A:     strings.Join(valsA, ", "),
				B:     strings.Join(valsB, ", "),
			})
		}
	}
}

// headerValues 把同名（大小写不敏感）头的取值按出现顺序聚在一起。
func headerValues(fields []HeaderField) map[string][]string {
	out := make(map[string][]string, len(fields))
	for _, f := range fields {
		key := strings.ToLower(f.Name)
		out[key] = append(out[key], f.Value)
	}
	return out
}

// headerNameUnion 先按 A 的顺序列出头名（小写去重），再补 B 独有的名字。
func headerNameUnion(a, b []HeaderField) []string {
	seen := make(map[string]struct{}, len(a)+len(b))
	out := make([]string, 0, len(a)+len(b))
	for _, group := range [][]HeaderField{a, b} {
		for _, f := range group {
			key := strings.ToLower(f.Name)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, key)
		}
	}
	return out
}

// addDiff 只在两侧可读表示不同时记一条差异。
func addDiff(rep *CompareReport, field, a, b string) {
	if a == b {
		return
	}
	rep.Diffs = append(rep.Diffs, Diff{Field: field, A: orEmptyLabel(a), B: orEmptyLabel(b)})
}

// formatHeaderNames 用原始大小写拼出头名顺序，大小写差异也算指纹差异。
func formatHeaderNames(fields []HeaderField) string {
	names := make([]string, 0, len(fields))
	for _, f := range fields {
		names = append(names, f.Name)
	}
	return formatStringList(names)
}

// formatSettings 把 SETTINGS 渲染成 [id:value, ...]，保持帧内顺序。
func formatSettings(settings []H2Setting) string {
	if len(settings) == 0 {
		return emptyLabel
	}
	parts := make([]string, 0, len(settings))
	for _, s := range settings {
		parts = append(parts, fmt.Sprintf("%d:%d", s.ID, s.Value))
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// formatStringList 用 ", " 连接字符串列表，空列表显示为 (空)。
func formatStringList(v []string) string {
	if len(v) == 0 {
		return emptyLabel
	}
	return strings.Join(v, ", ")
}

// formatNumberList 用 ", " 连接整数列表，空列表显示为 (空)。
func formatNumberList[T ~int | ~uint8 | ~uint16 | ~uint32](v []T) string {
	if len(v) == 0 {
		return emptyLabel
	}
	return joinNumberList(v, ", ")
}

func equalStringSlices(a, b []string) bool {
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

func presenceLabel(present bool) string {
	if present {
		return "有"
	}
	return "缺失"
}

func presenceOr(s string) string {
	if s == "" {
		return emptyLabel
	}
	return s
}

func orEmptyLabel(s string) string {
	if s == "" {
		return emptyLabel
	}
	return s
}

func volatileLabel(present bool) string {
	if !present {
		return absentValue
	}
	return ignoredValue
}

func valuesLabel(present bool, vals []string) string {
	if !present {
		return absentValue
	}
	if len(vals) == 0 {
		return emptyLabel
	}
	return strings.Join(vals, ", ")
}

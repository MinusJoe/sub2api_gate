// ja3.go 按 JA3（Salesforce）规范拼出指纹原文并计算 MD5。
//
// 原文格式：SSLVersion,Ciphers,Extensions,EllipticCurves,EllipticCurvePointFormats
// 各列表内部以 "-" 连接、元素为十进制；某个列表为空时该位置留空串（不填占位符）。
package main

import (
	"crypto/md5"
	"encoding/hex"
	"strconv"
	"strings"
)

// JA3String 返回 JA3 原文（不含 MD5）。
func JA3String(ch *ClientHello) string {
	var b strings.Builder
	b.WriteString(strconv.FormatUint(uint64(ch.Version), 10))
	b.WriteByte(',')
	b.WriteString(joinNumberList(ch.Ciphers, "-"))
	b.WriteByte(',')
	b.WriteString(joinNumberList(ch.Extensions, "-"))
	b.WriteByte(',')
	b.WriteString(joinNumberList(ch.Curves, "-"))
	b.WriteByte(',')
	b.WriteString(joinNumberList(ch.PointFormats, "-"))
	return b.String()
}

// JA3MD5Sum 返回 JA3 原文的 MD5，小写十六进制。
func JA3MD5Sum(ja3 string) string {
	sum := md5.Sum([]byte(ja3))
	return hex.EncodeToString(sum[:])
}

// joinNumberList 用 sep 连接小整数列表；这些值都是协议里的非负 ID，
// 统一按 int64 格式化即可。空列表返回空串（JA3 空项语义）。
func joinNumberList[T ~int | ~uint8 | ~uint16 | ~uint32](v []T, sep string) string {
	var b strings.Builder
	for i, x := range v {
		if i > 0 {
			b.WriteString(sep)
		}
		b.WriteString(strconv.FormatInt(int64(x), 10))
	}
	return b.String()
}

package main

import (
	"bufio"
	"fmt"
	"strings"
)

// isConnectRequest 判断连接的第一段是否为 HTTP CONNECT（HTTP 代理隧道）。
// 只做前缀判断，不消费字节：调用方随后用 readConnectRequest 真正读取。
func isConnectRequest(br *bufio.Reader) bool {
	prefix, err := br.Peek(8)
	if err != nil || len(prefix) < 8 {
		return false
	}
	return strings.EqualFold(string(prefix), "CONNECT ")
}

// readConnectRequest 读取并消费一个 CONNECT 请求，返回目标 host:port。
// 只接受 "CONNECT host:port HTTP/1.x"，其余形态返回错误（由调用方记录后断开）。
func readConnectRequest(br *bufio.Reader) (string, error) {
	line, err := br.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("读取 CONNECT 请求行: %w", err)
	}
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) < 2 || !strings.EqualFold(fields[0], "CONNECT") {
		return "", fmt.Errorf("不是 CONNECT 请求: %q", strings.TrimSpace(line))
	}
	target := fields[1]
	if !strings.Contains(target, ":") {
		return "", fmt.Errorf("CONNECT 目标缺少端口: %q", target)
	}
	// 丢弃头部直到空行；HTTPS 代理请求通常只有 Host 一行。
	for {
		header, err := br.ReadString('\n')
		if err != nil {
			return "", fmt.Errorf("读取 CONNECT 头部: %w", err)
		}
		if header == "\r\n" || header == "\n" {
			break
		}
	}
	return target, nil
}

// isPlainHTTPRequest 判断首段是否为明文 HTTP 请求（GET/POST/... 开头）。
// 这类连接不是 TLS 握手——通常是代理探测或健康检查；抓包工具应识别并优雅关闭，
// 而不是当成 ClientHello 解析失败刷屏。
func isPlainHTTPRequest(br *bufio.Reader) bool {
	prefix, err := br.Peek(8)
	if err != nil || len(prefix) < 8 {
		return false
	}
	upper := strings.ToUpper(string(prefix))
	for _, method := range []string{"GET ", "POST ", "HEAD ", "PUT ", "DELETE ", "OPTIONS ", "PATCH ", "CONNECT "} {
		if strings.HasPrefix(upper, method) {
			return true
		}
	}
	return false
}

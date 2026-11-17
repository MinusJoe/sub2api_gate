package main

import (
	"bufio"
	"strings"
	"testing"
)

// HTTPS_PROXY 场景：客户端先发 CONNECT，再在同一连接上做 TLS 握手。
func TestReadConnectRequest(t *testing.T) {
	br := bufio.NewReader(strings.NewReader(
		"CONNECT chatgpt.com:443 HTTP/1.1\r\nHost: chatgpt.com:443\r\nProxy-Connection: keep-alive\r\n\r\n"))
	if !isConnectRequest(br) {
		t.Fatal("应识别为 CONNECT 请求")
	}
	target, err := readConnectRequest(br)
	if err != nil {
		t.Fatalf("readConnectRequest: %v", err)
	}
	if target != "chatgpt.com:443" {
		t.Fatalf("target = %q", target)
	}
	// 头部必须被完整消费：后续字节应是 TLS 记录（这里用哨兵字节验证）。
	if _, err := br.Peek(1); err == nil {
		t.Fatal("CONNECT 头部之后不应还有缓冲字节")
	}
}

// 非 CONNECT（例如直接 TLS）不能被误判。
func TestIsConnectRequestFalseForTLS(t *testing.T) {
	br := bufio.NewReader(strings.NewReader("\x16\x03\x01\x02\x00\x01\x00\x01\xfc"))
	if isConnectRequest(br) {
		t.Fatal("TLS 记录不应被识别为 CONNECT")
	}
}

// 畸形请求要报错而不是猜一个目标。
func TestReadConnectRequestRejectsMalformed(t *testing.T) {
	cases := []string{
		"GET / HTTP/1.1\r\n\r\n",
		"CONNECT chatgpt.com HTTP/1.1\r\n\r\n",
		"CONNECT\r\n\r\n",
	}
	for _, raw := range cases {
		br := bufio.NewReader(strings.NewReader(raw))
		if _, err := readConnectRequest(br); err == nil {
			t.Fatalf("应拒绝畸形请求 %q", raw)
		}
	}
}

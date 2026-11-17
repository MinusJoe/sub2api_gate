package main

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"io"
	"net"
	"testing"
	"time"

	utls "github.com/refraction-networking/utls"

	"github.com/MACOS-DO/sub4api/internal/pkg/tlsfingerprint"
)

// 线级验收：用生产同一份 ClientHelloSpec 真实握手一次，服务端从字节里解析出的
// JA3 必须与官方抓包完全一致，且不带 ALPN。这一条覆盖"结构体对、线上不对"的风险。
func TestCodexRustlsProfileWireJA3(t *testing.T) {
	cert, err := generateSelfSignedCert()
	if err != nil {
		t.Fatalf("生成测试证书失败: %v", err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听失败: %v", err)
	}
	defer func() { _ = ln.Close() }()

	serverErr := make(chan error, 1)
	observed := make(chan string, 1)

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			serverErr <- err
			return
		}
		defer func() { _ = conn.Close() }()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		br := bufio.NewReader(conn)
		hello, err := ReadClientHello(br)
		if err != nil {
			serverErr <- err
			return
		}
		info := hello.toTLSInfo()
		observed <- info.JA3
		// 读完 ClientHello 后把已消费的原始字节 + 剩余缓冲一起回放给 tls.Server。
		srv := tls.Server(&replayConn{
			Conn: conn,
			r:    io.MultiReader(bytes.NewReader(hello.Raw), br),
		}, &tls.Config{Certificates: []tls.Certificate{cert}})
		if err := srv.Handshake(); err != nil {
			serverErr <- err
			return
		}
		serverErr <- nil
	}()

	raw, err := net.DialTimeout("tcp", ln.Addr().String(), 3*time.Second)
	if err != nil {
		t.Fatalf("连接测试服务失败: %v", err)
	}
	defer func() { _ = raw.Close() }()

	uconn := utls.UClient(raw, &utls.Config{
		ServerName:         "chatgpt.com",
		InsecureSkipVerify: true,
	}, utls.HelloCustom)
	if err := uconn.ApplyPreset(tlsfingerprint.BuildClientHelloSpec(tlsfingerprint.CodexRustlsProfile())); err != nil {
		t.Fatalf("ApplyPreset 失败: %v", err)
	}
	if err := uconn.Handshake(); err != nil {
		t.Fatalf("握手失败: %v", err)
	}

	if err := <-serverErr; err != nil {
		t.Fatalf("服务端握手失败: %v", err)
	}
	got := <-observed
	if want := tlsfingerprint.CodexRustlsProfileJA3(); got != want {
		t.Fatalf("线级 JA3 不一致:\n got: %s\nwant: %s", got, want)
	}
}

// replayConn 把"已经被 ReadClientHello 消费过的字节"重新喂给 tls.Server。
type replayConn struct {
	net.Conn
	r io.Reader
}

func (c *replayConn) Read(p []byte) (int, error) { return c.r.Read(p) }

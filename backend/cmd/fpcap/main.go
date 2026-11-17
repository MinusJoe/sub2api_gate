// fpcap 采集并对比“官方客户端 vs 网关出站”的握手指纹基线。
//
// 用法：
//
//	fpcap peek    -upstream HOST:PORT [-listen ADDR] [-out cap.jsonl]
//	fpcap serve   [-listen ADDR] [-cert cert.pem -key key.pem | -self-signed] [-proto h1|h2] [-out cap.jsonl]
//	fpcap compare -a a.json -b b.json [-json]
//
// peek 是纯 TCP 透传：握手前截取 ClientHello，再把原始字节原样转发给上游，
// 客户端不需要信任任何证书；能拿到 JA3/ALPN/SNI 与 cipher、扩展顺序，
// 但 TLS 之后的内容是密文，看不到 HTTP 层。
//
// serve 自己做 TLS 终止：需要客户端信任 -cert/-key 提供的证书，
// 或显式 -self-signed 现场生成临时证书。默认只声明 ALPN http/1.1，
// 便于抓 HTTP/1.1 原始头行；客户端若不接受该 ALPN，仍会先记录 ClientHello 再关闭。
//
// 输出是 JSON Lines，每条连接一行，字段顺序固定：
//
//	{"source","captured_at","tls":{...},"http2":{...},"http1":{...}}
package main

import (
	"bufio"
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"strings"
	"sync"
	"time"
)

// 各模式的读取上限与超时，避免连接挂死或异常输入拖垮采集端。
const (
	peekHandshakeTimeout = 10 * time.Second
	serveHandshakeWindow = 15 * time.Second
	upstreamDialTimeout  = 10 * time.Second
	recordingByteLimit   = 64 << 10
)

// Capture 是一条指纹记录，也是 JSON Lines 里的一行。
type Capture struct {
	Source     string     `json:"source"`
	CapturedAt string     `json:"captured_at"`
	TLS        *TLSInfo   `json:"tls,omitempty"`
	HTTP2      *HTTP2Info `json:"http2,omitempty"`
	HTTP1      *HTTP1Info `json:"http1,omitempty"`
}

func main() {
	if len(os.Args) < 2 {
		usage(os.Stderr)
		os.Exit(2)
	}
	switch os.Args[1] {
	case "peek":
		runPeek(os.Args[2:])
	case "serve":
		runServe(os.Args[2:])
	case "compare":
		runCompare(os.Args[2:])
	case "profile":
		runProfile(os.Args[2:])
	case "-h", "--help", "-help", "help":
		usage(os.Stdout)
	default:
		fmt.Fprintf(os.Stderr, "未知模式 %q\n\n", os.Args[1])
		usage(os.Stderr)
		os.Exit(2)
	}
}

// usage 打印顶层说明：显式 -h 走 stdout，参数错误走 stderr。
func usage(w io.Writer) {
	fmt.Fprint(w, `fpcap —— 采集并对比“官方客户端 vs 网关出站”的握手指纹基线

用法：
  fpcap peek    -upstream HOST:PORT [-listen 127.0.0.1:18443] [-out cap.jsonl]
  fpcap serve   [-listen 127.0.0.1:18444] [-cert cert.pem -key key.pem | -self-signed] [-proto h1|h2] [-out cap.jsonl]
  fpcap compare -a a.json -b b.json [-json]
  fpcap profile -in capture.jsonl -name NAME [-out profile.json] [-go]

模式：
  peek     纯 TCP 透传代理。握手前截取 ClientHello，再把已读字节原样转发给上游；
           客户端不需要信任任何证书。可得到 JA3/ALPN/SNI、cipher 与扩展顺序，
           但无法解密 TLS 之后的内容。
           也接受 HTTP CONNECT 隧道：可直接把 HTTPS_PROXY 指向它，无需改证书信任。
  serve    自己做 TLS 终止。需要客户端信任 -cert/-key 的证书，或显式 -self-signed
           现场生成 ECDSA 临时证书。默认只声明 ALPN http/1.1，抓 HTTP/1.1 原始头行；
           客户端若只支持 h2（无匹配 ALPN），仍会记录 ClientHello 后关闭。
           -proto h2 是实验开关，用于抓 h2 preface/SETTINGS/HEADERS。
  compare  逐字段对比两份 JSON Lines 基线（各取第一条非空记录）：
           ja3、sni、alpn、ciphers、extensions、curves、sigalgs、h2 settings、
           头名顺序、头取值（另含 h1 的 method/path）。
           完全一致 exit 0，有差异 exit 1，参数或读取错误 exit 2。
           取值不参与比较的头：date、x-request-id、authorization、cookie、
           x-codex-turn-state、session_id、conversation_id、prompt_cache_key，
           这些头只比“是否出现”，取值以 <ignored> 代替并在报告中标注“值已忽略”。

输出：每条连接一行 JSON，字段顺序固定——
  {"source","captured_at","tls":{...},"http2":{...},"http1":{...}}
  tls.ciphers/extensions/curves/sigalgs/ec_point_formats 为十进制（JA3 原料），
  tls.version 与 tls.supported_versions 为 0x 前缀十六进制。
`)
}

// nowStamp 返回 UTC 的 RFC3339Nano 时间戳，便于跨机器对齐采集时间。
func nowStamp() string { return time.Now().UTC().Format(time.RFC3339Nano) }

// captureWriter 把每条捕获以一行 JSON 追加写入目标（文件或 stdout），并发安全。
type captureWriter struct {
	mu     sync.Mutex
	buf    *bufio.Writer
	closer io.Closer
}

// openCaptureWriter 打开输出目标；path 为空时写 stdout。
func openCaptureWriter(path string) (*captureWriter, error) {
	if path == "" {
		return &captureWriter{buf: bufio.NewWriter(os.Stdout)}, nil
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("打开输出文件 %s: %w", path, err)
	}
	return &captureWriter{buf: bufio.NewWriter(f), closer: f}, nil
}

// write 追加一行 JSON，并立刻 flush，保证 -out 文件能被 tail/compare 即时读到。
func (w *captureWriter) write(c *Capture) error {
	raw, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("序列化捕获: %w", err)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, err := w.buf.Write(append(raw, '\n')); err != nil {
		return fmt.Errorf("写入捕获: %w", err)
	}
	return w.buf.Flush()
}

// Close 刷盘并关闭底层文件（stdout 不关）。
func (w *captureWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	flushErr := w.buf.Flush()
	if w.closer == nil {
		return flushErr
	}
	closeErr := w.closer.Close()
	if flushErr != nil {
		return flushErr
	}
	return closeErr
}

// runPeek 实现 peek 模式：监听本地端口，把连接透传到 -upstream。
func runPeek(args []string) {
	fs := flag.NewFlagSet("peek", flag.ExitOnError)
	listen := fs.String("listen", "127.0.0.1:18443", "本地监听地址（TCP）")
	upstream := fs.String("upstream", "", "上游地址 host:port（必填），例如 api.example.com:443")
	out := fs.String("out", "", "追加写入的 JSON Lines 文件，空表示输出到 stdout")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, "用法：fpcap peek -upstream HOST:PORT [-listen ADDR] [-out FILE]\n\n")
		fs.PrintDefaults()
	}
	_ = fs.Parse(args)
	if *upstream == "" {
		fmt.Fprintln(os.Stderr, "peek 需要 -upstream host:port")
		fs.Usage()
		os.Exit(2)
	}
	writer, err := openCaptureWriter(*out)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(2)
	}
	defer func() {
		if err := writer.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "关闭输出: %v\n", err)
		}
	}()

	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		fmt.Fprintf(os.Stderr, "监听 %s 失败: %v\n", *listen, err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "peek 已就绪：%s -> %s（Ctrl+C 结束）\n", ln.Addr(), *upstream)
	for {
		conn, err := ln.Accept()
		if err != nil {
			fmt.Fprintf(os.Stderr, "accept 失败: %v\n", err)
			continue
		}
		go handlePeekConn(conn, *upstream, writer)
	}
}

// handlePeekConn 截取 ClientHello → 落盘 → 连接上游 → 回放已读字节 → 双向透传。
func handlePeekConn(client net.Conn, upstream string, writer *captureWriter) {
	defer func() { _ = client.Close() }()
	_ = client.SetReadDeadline(time.Now().Add(peekHandshakeTimeout))
	br := bufio.NewReader(client)
	target := upstream
	if isConnectRequest(br) {
		connectTarget, err := readConnectRequest(br)
		if err != nil {
			fmt.Fprintf(os.Stderr, "peek %s: parse CONNECT failed: %v", client.RemoteAddr(), err)
			return
		}
		target = connectTarget
		if _, err := io.WriteString(client, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
			fmt.Fprintf(os.Stderr, "peek %s: reply CONNECT failed: %v", client.RemoteAddr(), err)
			return
		}
	} else if isPlainHTTPRequest(br) {
		line, _ := br.ReadString('\n')
		fmt.Fprintf(os.Stderr, "peek %s: non-TLS HTTP request (proxy probe, ignored): %s\n",
			client.RemoteAddr(), strings.TrimSpace(line))
		_, _ = io.WriteString(client, "HTTP/1.1 204 No Content\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
		return
	}
	ch, err := ReadClientHello(br)
	_ = client.SetReadDeadline(time.Time{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "peek %s: 解析 ClientHello 失败: %v\n", client.RemoteAddr(), err)
		return
	}
	reportWarnings("peek "+client.RemoteAddr().String(), ch.Warnings)

	cap := &Capture{
		Source:     fmt.Sprintf("peek %s -> %s", client.RemoteAddr(), target),
		CapturedAt: nowStamp(),
		TLS:        ch.toTLSInfo(),
	}
	if err := writer.write(cap); err != nil {
		fmt.Fprintf(os.Stderr, "peek %s: 写入捕获失败: %v\n", client.RemoteAddr(), err)
	}

	server, err := net.DialTimeout("tcp", target, upstreamDialTimeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "peek %s: 连接上游 %s 失败: %v\n", client.RemoteAddr(), upstream, err)
		return
	}
	defer func() { _ = server.Close() }()
	if _, err := server.Write(ch.Raw); err != nil {
		fmt.Fprintf(os.Stderr, "peek %s: 回放 ClientHello 失败: %v\n", client.RemoteAddr(), err)
		return
	}
	// 任一半方向结束就收尾（本工具不依赖 TCP half-close 语义）。
	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(server, br)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(client, server)
		done <- struct{}{}
	}()
	<-done
}

// runServe 实现 serve 模式：TLS 终止后抓应用层头/帧。
func runServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	listen := fs.String("listen", "127.0.0.1:18444", "本地监听地址（TCP）")
	certPath := fs.String("cert", "", "服务端证书 PEM（需与 -key 同时提供）")
	keyPath := fs.String("key", "", "服务端私钥 PEM（需与 -cert 同时提供）")
	selfSigned := fs.Bool("self-signed", false, "未提供证书时现场生成 ECDSA 自签证书（仅本地测试）")
	proto := fs.String("proto", "h1", "应用层协议：h1（默认）或 h2（实验性）")

	forward := fs.String("forward", "", "反向代理模式：把请求转发到该 base URL（例如 https://chatgpt.com），用于抓业务请求头")
	out := fs.String("out", "", "追加写入的 JSON Lines 文件，空表示输出到 stdout")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, "用法：fpcap serve [-listen ADDR] [-cert cert.pem -key key.pem | -self-signed] [-proto h1|h2] [-out FILE]\n\n")
		fs.PrintDefaults()
	}
	_ = fs.Parse(args)
	if *proto != "h1" && *proto != "h2" {
		fmt.Fprintf(os.Stderr, "-proto 只支持 h1 或 h2，收到 %q\n", *proto)
		os.Exit(2)
	}
	cert, err := loadOrGenerateCert(*certPath, *keyPath, *selfSigned)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(2)
	}
	writer, err := openCaptureWriter(*out)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(2)
	}
	defer func() {
		if err := writer.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "关闭输出: %v\n", err)
		}
	}()

	cfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}
	if *proto == "h2" {
		cfg.NextProtos = []string{"h2"}
	} else {
		// 只声明 http/1.1：便于按原始字节抓 HTTP/1.1 头顺序。
		cfg.NextProtos = []string{"http/1.1"}
	}

	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		fmt.Fprintf(os.Stderr, "监听 %s 失败: %v\n", *listen, err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "serve 已就绪：%s（proto=%s，Ctrl+C 结束）\n", ln.Addr(), *proto)
	for {
		conn, err := ln.Accept()
		if err != nil {
			fmt.Fprintf(os.Stderr, "accept 失败: %v\n", err)
			continue
		}
		go handleServeConn(conn, cfg, *proto == "h2", writer, *forward)
	}
}

// handleServeConn 完成一次 TLS 握手并抓取应用层观察结果；
// 握手失败（例如无匹配 ALPN）时仍把已读到的 ClientHello 记录下来。
func handleServeConn(raw net.Conn, cfg *tls.Config, useH2 bool, writer *captureWriter, forwardBase string) {
	defer func() { _ = raw.Close() }()
	rec := &recordingConn{Conn: raw, limit: recordingByteLimit}
	tlsConn := tls.Server(rec, cfg)
	_ = tlsConn.SetDeadline(time.Now().Add(serveHandshakeWindow))
	handshakeErr := tlsConn.Handshake()
	_ = tlsConn.SetDeadline(time.Time{})

	cap := &Capture{
		Source:     fmt.Sprintf("serve %s", raw.RemoteAddr()),
		CapturedAt: nowStamp(),
	}
	// 已读字节的开头一定是 ClientHello，握手后用同一份副本解析。
	if ch, err := ReadClientHello(bytes.NewReader(rec.snapshot())); err == nil {
		cap.TLS = ch.toTLSInfo()
		reportWarnings("serve "+raw.RemoteAddr().String(), ch.Warnings)
	} else {
		fmt.Fprintf(os.Stderr, "serve %s: 解析 ClientHello 失败: %v\n", raw.RemoteAddr(), err)
	}
	if handshakeErr != nil {
		fmt.Fprintf(os.Stderr, "serve %s: TLS 握手失败（客户端不受信或 ALPN 不匹配）: %v\n", raw.RemoteAddr(), handshakeErr)
		if cap.TLS != nil {
			if err := writer.write(cap); err != nil {
				fmt.Fprintf(os.Stderr, "serve %s: 写入捕获失败: %v\n", raw.RemoteAddr(), err)
			}
		}
		return
	}
	// 本工具只采集不回包：读完首批请求即结束，客户端会感知到 EOF。
	if useH2 {
		info, err := ReadHTTP2(tlsConn)
		if err != nil {
			fmt.Fprintf(os.Stderr, "serve %s: 解析 h2 首部失败: %v\n", raw.RemoteAddr(), err)
		} else {
			cap.HTTP2 = info
		}
	} else {
		br := bufio.NewReader(tlsConn)
		info, err := ReadHTTP1(br)
		if err != nil {
			fmt.Fprintf(os.Stderr, "serve %s: 解析 HTTP/1.1 头失败: %v\n", raw.RemoteAddr(), err)
		} else {
			cap.HTTP1 = info
			if forwardBase != "" {
				body, _, bodyErr := readRequestBody(br, info.Headers)
				if bodyErr != nil {
					fmt.Fprintf(os.Stderr, "serve %s: 读取请求体失败: %v\n", raw.RemoteAddr(), bodyErr)
				}
				if werr := writer.write(cap); werr != nil {
					fmt.Fprintf(os.Stderr, "serve %s: 写入捕获失败: %v\n", raw.RemoteAddr(), werr)
				}
				if ferr := forwardHTTP1(br, info, body, forwardBase, tlsConn); ferr != nil {
					fmt.Fprintf(os.Stderr, "serve %s: 转发失败: %v\n", raw.RemoteAddr(), ferr)
				}
				return
			}
		}
	}
	if err := writer.write(cap); err != nil {
		fmt.Fprintf(os.Stderr, "serve %s: 写入捕获失败: %v\n", raw.RemoteAddr(), err)
	}
}

// recordingConn 把从客户端读到的字节留一份副本，用于握手后解析 ClientHello。
type recordingConn struct {
	net.Conn
	mu    sync.Mutex
	buf   []byte
	limit int
}

func (c *recordingConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		c.mu.Lock()
		if room := c.limit - len(c.buf); room > 0 {
			take := n
			if take > room {
				take = room
			}
			c.buf = append(c.buf, p[:take]...)
		}
		c.mu.Unlock()
	}
	return n, err
}

// snapshot 返回已读字节的副本。
func (c *recordingConn) snapshot() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]byte(nil), c.buf...)
}

// loadOrGenerateCert 按 -cert/-key/-self-signed 决定证书来源。
func loadOrGenerateCert(certPath, keyPath string, allowSelfSigned bool) (tls.Certificate, error) {
	switch {
	case certPath != "" && keyPath != "":
		cert, err := tls.LoadX509KeyPair(certPath, keyPath)
		if err != nil {
			return tls.Certificate{}, fmt.Errorf("加载证书 %s / %s: %w", certPath, keyPath, err)
		}
		return cert, nil
	case certPath != "" || keyPath != "":
		return tls.Certificate{}, fmt.Errorf("-cert 与 -key 必须同时提供")
	case allowSelfSigned:
		return generateSelfSignedCert()
	default:
		return tls.Certificate{}, fmt.Errorf("需要 -cert/-key，或显式加 -self-signed 生成临时证书")
	}
}

// generateSelfSignedCert 现场生成 ECDSA P-256 自签证书，仅用于本地抓包。
func generateSelfSignedCert() (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("生成自签私钥: %w", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("生成证书序列号: %w", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "fpcap self-signed"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(30 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("签发自签证书: %w", err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, nil
}

// runCompare 实现 compare 模式：一致 exit 0，有差异 exit 1。
func runCompare(args []string) {
	fs := flag.NewFlagSet("compare", flag.ExitOnError)
	pathA := fs.String("a", "", "基线 A 路径（JSON Lines 取第一条非空记录）")
	pathB := fs.String("b", "", "基线 B 路径（JSON Lines 取第一条非空记录）")
	jsonOut := fs.Bool("json", false, "以 JSON 输出对比报告")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, "用法：fpcap compare -a a.json -b b.json [-json]\n\n")
		fs.PrintDefaults()
	}
	_ = fs.Parse(args)
	if *pathA == "" || *pathB == "" {
		fmt.Fprintln(os.Stderr, "compare 需要 -a 与 -b")
		fs.Usage()
		os.Exit(2)
	}
	a, err := readCapture(*pathA)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(2)
	}
	b, err := readCapture(*pathB)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(2)
	}
	rep := CompareCaptures(a, b)
	printCompareReport(rep, *jsonOut)
	if rep.Equal {
		os.Exit(0)
	}
	os.Exit(1)
}

// readCapture 读取一份基线：优先按第一条非空行（JSON Lines）解析，失败再整体解析。
// 允许文件带 UTF-8 BOM——Windows 编辑器另存时很容易加上，不该让 compare 直接失败。
func readCapture(path string) (*Capture, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取 %s: %w", path, err)
	}
	raw = bytes.TrimPrefix(raw, []byte{0xEF, 0xBB, 0xBF})
	var c Capture
	if line := firstNonEmptyLine(raw); line != "" {
		if err := json.Unmarshal([]byte(line), &c); err == nil {
			return &c, nil
		}
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("解析 %s: %w", path, err)
	}
	return &c, nil
}

// firstNonEmptyLine 返回第一行非空文本。
func firstNonEmptyLine(raw []byte) string {
	for _, line := range strings.Split(string(raw), "\n") {
		if s := strings.TrimSpace(line); s != "" {
			return s
		}
	}
	return ""
}

// printCompareReport 输出差异与被忽略项。
func printCompareReport(rep CompareReport, jsonOut bool) {
	if jsonOut {
		raw, err := json.MarshalIndent(rep, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "序列化报告失败: %v\n", err)
			return
		}
		fmt.Println(string(raw))
		return
	}
	if len(rep.Ignored) > 0 {
		fmt.Println("已忽略（volatile 取值不参与比较）：")
		for _, item := range rep.Ignored {
			fmt.Println("  ~ " + item)
		}
	}
	if rep.Equal {
		fmt.Println("一致：未发现差异")
		return
	}
	fmt.Printf("发现 %d 处差异：\n", len(rep.Diffs))
	for _, d := range rep.Diffs {
		fmt.Printf("  - %s\n      A = %s\n      B = %s\n", d.Field, d.A, d.B)
	}
}

// reportWarnings 把可容忍的解析告警打到 stderr，不打断采集。
func reportWarnings(prefix string, warnings []string) {
	for _, w := range warnings {
		fmt.Fprintf(os.Stderr, "%s: 告警: %s\n", prefix, w)
	}
}

// h1.go 读取 HTTP/1.1 请求行与原始头行。
//
// 不用 net/http 的解析器：它会归一化头名、丢失顺序，而这两项正是要对比的指纹。
package main

import (
	"bufio"
	"fmt"
	"strings"
)

const (
	h1MaxLineBytes = 64 << 10
	h1MaxHeaderNum = 200
)

// HTTP1Info 保存请求行与原始头行（按到达顺序，头名保留发送时的大小写）。
type HTTP1Info struct {
	Method  string        `json:"method"`
	Path    string        `json:"path"`
	Headers []HeaderField `json:"headers"`
}

// ReadHTTP1 依次读取请求行与头行，遇空行结束；body 不读，留给调用方。
func ReadHTTP1(r *bufio.Reader) (*HTTP1Info, error) {
	line, err := readH1Line(r)
	if err != nil {
		return nil, fmt.Errorf("读取请求行: %w", err)
	}
	method, target, proto, ok := parseH1RequestLine(line)
	if !ok {
		return nil, fmt.Errorf("请求行格式非法: %q", line)
	}
	if proto == "" {
		return nil, fmt.Errorf("请求行缺少协议版本: %q", line)
	}
	info := &HTTP1Info{Method: method, Path: target, Headers: []HeaderField{}}
	for i := 0; ; i++ {
		if i > h1MaxHeaderNum {
			return nil, fmt.Errorf("头行数量超过 %d 上限", h1MaxHeaderNum)
		}
		line, err := readH1Line(r)
		if err != nil {
			return nil, fmt.Errorf("读取头行: %w", err)
		}
		if line == "" {
			return info, nil
		}
		name, value, ok := strings.Cut(line, ":")
		if !ok || name == "" {
			return nil, fmt.Errorf("头行格式非法: %q", line)
		}
		if strings.ContainsAny(name, " \t") {
			return nil, fmt.Errorf("头名含空白: %q", name)
		}
		// 头名原样保留大小写；值去掉首尾空白（obsolete line folding 不支持）。
		info.Headers = append(info.Headers, HeaderField{Name: name, Value: strings.TrimSpace(value)})
	}
}

// parseH1RequestLine 按 "METHOD SP TARGET SP VERSION" 拆请求行。
func parseH1RequestLine(line string) (method, target, proto string, ok bool) {
	parts := strings.Split(line, " ")
	if len(parts) < 3 {
		return "", "", "", false
	}
	method, target = parts[0], parts[1]
	proto = parts[len(parts)-1]
	if method == "" || target == "" {
		return "", "", "", false
	}
	return method, target, proto, true
}

// readH1Line 读到 '\n' 为止并去掉行尾 CR/LF，长行按 bufio 缓冲循环拼接。
func readH1Line(r *bufio.Reader) (string, error) {
	var buf []byte
	for {
		frag, err := r.ReadSlice('\n')
		buf = append(buf, frag...)
		if len(buf) > h1MaxLineBytes {
			return "", fmt.Errorf("行长度超过 %d 字节", h1MaxLineBytes)
		}
		if err == nil {
			break
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		return "", err
	}
	return strings.TrimRight(string(buf), "\r\n"), nil
}

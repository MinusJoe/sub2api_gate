package main

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const forwardMaxBodyBytes = 32 << 20

// readRequestBody 按 Content-Length / chunked 读取请求体；支持的上限见 forwardMaxBodyBytes。
// 只用于抓包转发，不追求完整的 HTTP 语义（不支持 trailer、expect-continue 等）。
func readRequestBody(br *bufio.Reader, headers []HeaderField) ([]byte, bool, error) {
	chunked := false
	contentLength := int64(-1)
	for _, h := range headers {
		switch strings.ToLower(h.Name) {
		case "content-length":
			if n, err := strconv.ParseInt(strings.TrimSpace(h.Value), 10, 64); err == nil {
				contentLength = n
			}
		case "transfer-encoding":
			if strings.Contains(strings.ToLower(h.Value), "chunked") {
				chunked = true
			}
		}
	}
	if chunked {
		body, err := readChunkedBody(br)
		return body, true, err
	}
	if contentLength <= 0 {
		return nil, false, nil
	}
	if contentLength > forwardMaxBodyBytes {
		return nil, true, fmt.Errorf("请求体超过上限: %d", contentLength)
	}
	body := make([]byte, contentLength)
	if _, err := io.ReadFull(br, body); err != nil {
		return nil, true, fmt.Errorf("读取请求体: %w", err)
	}
	return body, true, nil
}

func readChunkedBody(br *bufio.Reader) ([]byte, error) {
	var out bytes.Buffer
	for {
		line, err := readH1Line(br)
		if err != nil {
			return nil, err
		}
		sizeStr := line
		if i := strings.IndexByte(sizeStr, ';'); i >= 0 {
			sizeStr = sizeStr[:i]
		}
		size, err := strconv.ParseInt(strings.TrimSpace(sizeStr), 16, 64)
		if err != nil {
			return nil, fmt.Errorf("chunk 长度非法: %q", line)
		}
		if size == 0 {
			// 丢弃 trailer，直到空行。
			for {
				trailer, err := readH1Line(br)
				if err != nil || trailer == "" {
					break
				}
			}
			return out.Bytes(), nil
		}
		if out.Len()+int(size) > forwardMaxBodyBytes {
			return nil, fmt.Errorf("chunked 请求体超过上限")
		}
		if _, err := io.CopyN(&out, br, size); err != nil {
			return nil, err
		}
		if _, err := readH1Line(br); err != nil { // 吃掉 chunk 结尾的 CRLF
			return nil, err
		}
	}
}

// headerValue 取第一个匹配的头值（大小写不敏感）。
func headerValue(headers []HeaderField, name string) string {
	for _, h := range headers {
		if strings.EqualFold(h.Name, name) {
			return h.Value
		}
	}
	return ""
}

// forwardHTTP1 把已解析的请求转发到 base 指向的真实上游，并把响应原样流式写回。
// 目的：让客户端走完真实业务流程（discovery 成功），从而抓到业务请求头。
func forwardHTTP1(br *bufio.Reader, info *HTTP1Info, body []byte, base string, out io.Writer) error {
	baseURL, err := url.Parse(base)
	if err != nil {
		return fmt.Errorf("forward base 非法: %w", err)
	}
	target := *baseURL
	if info.Path != "" {
		if strings.HasPrefix(info.Path, "http://") || strings.HasPrefix(info.Path, "https://") {
			parsed, err := url.Parse(info.Path)
			if err != nil {
				return err
			}
			target = *parsed
		} else {
			target.Path = ""
			target.RawQuery = ""
			path, query, _ := strings.Cut(info.Path, "?")
			target.Path = path
			target.RawQuery = query
		}
	}

	req, err := http.NewRequest(info.Method, target.String(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	for _, h := range info.Headers {
		switch strings.ToLower(h.Name) {
		case "host", "connection", "proxy-connection", "content-length", "transfer-encoding":
			continue
		}
		req.Header.Add(h.Name, h.Value)
	}
	req.Host = headerValue(info.Headers, "host")
	if req.Host == "" {
		req.Host = target.Host
	}

	// SNI 必须是被请求的主机名（Host 头），而不是 base URL 里的 IP；
	// 否则转发到真实 IP 时 SNI 会变成 IP，TLS 直接失败。
	serverName := headerValue(info.Headers, "host")
	if host, _, ok := strings.Cut(serverName, ":"); ok {
		serverName = host
	}
	if serverName == "" {
		serverName = baseURL.Hostname()
	}
	client := &http.Client{
		Transport: &http.Transport{
			ForceAttemptHTTP2:   false,
			DisableCompression:  true, // 保留原始字节，避免 Go 解压后头体不一致
			TLSHandshakeTimeout: 10 * time.Second,
			TLSClientConfig:     &tls.Config{ServerName: serverName},
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if _, err := fmt.Fprintf(out, "HTTP/1.1 %d %s\r\n", resp.StatusCode, http.StatusText(resp.StatusCode)); err != nil {
		return err
	}
	chunked := false
	for _, te := range resp.TransferEncoding {
		if strings.EqualFold(te, "chunked") {
			chunked = true
		}
	}
	for name, values := range resp.Header {
		if strings.EqualFold(name, "transfer-encoding") {
			continue // 由下面按需重新分块
		}
		for _, v := range values {
			if _, err := fmt.Fprintf(out, "%s: %s\r\n", name, v); err != nil {
				return err
			}
		}
	}
	if chunked {
		// Go 已把上游分块解开，这里按 HTTP/1.1 重新分块写回。
		if _, err := io.WriteString(out, "Transfer-Encoding: chunked\r\n\r\n"); err != nil {
			return err
		}
		return copyChunked(out, resp.Body)
	}
	if _, err := io.WriteString(out, "Connection: close\r\n\r\n"); err != nil {
		return err
	}
	_, err = io.Copy(out, resp.Body)
	return err
}

// copyChunked 把 body 按 HTTP/1.1 chunked 重新写回，保证流式响应（SSE）逐块可见。
func copyChunked(out io.Writer, body io.Reader) error {
	buf := make([]byte, 32<<10)
	for {
		n, err := body.Read(buf)
		if n > 0 {
			if _, werr := fmt.Fprintf(out, "%x\r\n", n); werr != nil {
				return werr
			}
			if _, werr := out.Write(buf[:n]); werr != nil {
				return werr
			}
			if _, werr := io.WriteString(out, "\r\n"); werr != nil {
				return werr
			}
		}
		if err == io.EOF {
			_, werr := io.WriteString(out, "0\r\n\r\n")
			return werr
		}
		if err != nil {
			return err
		}
	}
}

// h2.go 读取客户端 HTTP/2 开头：preface、SETTINGS 与首批 HEADERS（含 CONTINUATION）。
//
// 帧与头部还原都保持到达顺序：SETTINGS 按帧内顺序累积，HEADERS 块用 hpack.Decoder
// 按序还原，不做排序、去重或大小写归一，便于与官方客户端逐项比对。
package main

import (
	"encoding/binary"
	"fmt"
	"io"

	"golang.org/x/net/http2/hpack"
)

// HTTP/2 帧类型与标志（只列本工具关心的部分）。
// 注意 0x1 在 SETTINGS 里是 ACK、在 HEADERS 里是 END_STREAM，语义随帧类型变化，
// 因此分别命名。
const (
	h2Preface           = "PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n"
	h2FrameHeaders      = 0x1
	h2FrameSettings     = 0x4
	h2FrameContinuation = 0x9

	h2FlagSettingsAck = 0x1
	h2FlagEndStream   = 0x1
	h2FlagEndHeaders  = 0x4
	h2FlagPadded      = 0x8
	h2FlagPriority    = 0x20

	h2MaxFramePayload   = 1<<24 - 1
	h2MaxHeaderBlockLen = 1 << 20
)

// H2Setting 是 SETTINGS 帧里的一项参数。
type H2Setting struct {
	ID    uint16 `json:"id"`
	Value uint32 `json:"value"`
}

// HeaderField 是一条原始头，name 保持客户端发送时的形式，不排序、不去重。
type HeaderField struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// HTTP2Info 保存客户端 HTTP/2 首部的观察结果。
type HTTP2Info struct {
	Settings []H2Setting   `json:"settings"`
	Headers  []HeaderField `json:"headers"`
}

// ReadHTTP2 读取 preface、SETTINGS 与首批 HEADERS，返回时已消费到头块结束。
// 其它帧（PING、WINDOW_UPDATE、PRIORITY 等）跳过，不参与指纹。
func ReadHTTP2(r io.Reader) (*HTTP2Info, error) {
	preface := make([]byte, len(h2Preface))
	if _, err := io.ReadFull(r, preface); err != nil {
		return nil, fmt.Errorf("读取 h2 preface: %w", err)
	}
	if string(preface) != h2Preface {
		return nil, fmt.Errorf("h2 preface 不匹配: %q", preface)
	}
	info := &HTTP2Info{Settings: []H2Setting{}, Headers: []HeaderField{}}
	dec := hpack.NewDecoder(4096, nil)
	for {
		typ, flags, payload, err := readH2Frame(r)
		if err != nil {
			return nil, err
		}
		switch typ {
		case h2FrameSettings:
			if flags&h2FlagSettingsAck != 0 {
				continue // ACK 无负载
			}
			if len(payload)%6 != 0 {
				return nil, fmt.Errorf("SETTINGS 负载长度 %d 不是 6 的倍数", len(payload))
			}
			for i := 0; i < len(payload); i += 6 {
				info.Settings = append(info.Settings, H2Setting{
					ID:    binary.BigEndian.Uint16(payload[i : i+2]),
					Value: binary.BigEndian.Uint32(payload[i+2 : i+6]),
				})
			}
		case h2FrameHeaders:
			block, err := readHeaderBlock(r, flags, payload)
			if err != nil {
				return nil, err
			}
			fields, err := decodeHeaderBlock(dec, block)
			if err != nil {
				return nil, err
			}
			info.Headers = fields
			return info, nil
		}
	}
}

// readH2Frame 读取一帧，返回类型、标志与负载（不含 9 字节帧头）。
func readH2Frame(r io.Reader) (typ uint8, flags uint8, payload []byte, err error) {
	hdr := make([]byte, 9)
	if _, err = io.ReadFull(r, hdr); err != nil {
		return 0, 0, nil, fmt.Errorf("读取 h2 帧头: %w", err)
	}
	length := int(hdr[0])<<16 | int(hdr[1])<<8 | int(hdr[2])
	typ, flags = hdr[3], hdr[4]
	if length > h2MaxFramePayload {
		return 0, 0, nil, fmt.Errorf("h2 帧长度 %d 超过协议上限", length)
	}
	if length > 0 {
		payload = make([]byte, length)
		if _, err = io.ReadFull(r, payload); err != nil {
			return 0, 0, nil, fmt.Errorf("读取 h2 帧负载: %w", err)
		}
	}
	return typ, flags, payload, nil
}

// readHeaderBlock 取出首个 HEADERS 帧的头块片段，必要时继续读 CONTINUATION，
// 直到出现 END_HEADERS。返回的是已去掉 PADDED/PRIORITY 前缀的片段拼接结果。
func readHeaderBlock(r io.Reader, flags uint8, payload []byte) ([]byte, error) {
	block, err := headerFragment(flags, payload)
	if err != nil {
		return nil, err
	}
	endHeaders := flags&h2FlagEndHeaders != 0
	for !endHeaders {
		typ, cflags, cpayload, err := readH2Frame(r)
		if err != nil {
			return nil, err
		}
		if typ != h2FrameContinuation {
			return nil, fmt.Errorf("HEADERS 之后期望 CONTINUATION，实际帧类型 0x%02x", typ)
		}
		frag, err := headerFragment(cflags, cpayload)
		if err != nil {
			return nil, err
		}
		block = append(block, frag...)
		if len(block) > h2MaxHeaderBlockLen {
			return nil, fmt.Errorf("头块超过 %d 字节上限", h2MaxHeaderBlockLen)
		}
		endHeaders = cflags&h2FlagEndHeaders != 0
	}
	return block, nil
}

// headerFragment 去掉 HEADERS 帧的 PADDED/PRIORITY 前缀与尾部 padding。
// CONTINUATION 只有 END_HEADERS 一种标志，走同一条路径即为原样返回。
func headerFragment(flags uint8, payload []byte) ([]byte, error) {
	padLen := 0
	if flags&h2FlagPadded != 0 {
		if len(payload) < 1 {
			return nil, fmt.Errorf("标记了 PADDED 但负载为空")
		}
		padLen = int(payload[0])
		payload = payload[1:]
	}
	if flags&h2FlagPriority != 0 {
		if len(payload) < 5 {
			return nil, fmt.Errorf("标记了 PRIORITY 但负载不足 5 字节")
		}
		payload = payload[5:]
	}
	if padLen > len(payload) {
		return nil, fmt.Errorf("padding 长度 %d 超过剩余 %d", padLen, len(payload))
	}
	if padLen > 0 {
		payload = payload[:len(payload)-padLen]
	}
	return payload, nil
}

// decodeHeaderBlock 用 HPACK 还原有序头列表。
func decodeHeaderBlock(dec *hpack.Decoder, block []byte) ([]HeaderField, error) {
	fields, err := dec.DecodeFull(block)
	if err != nil {
		return nil, fmt.Errorf("HPACK 解码: %w", err)
	}
	out := make([]HeaderField, 0, len(fields))
	for _, f := range fields {
		out = append(out, HeaderField{Name: f.Name, Value: f.Value})
	}
	return out, nil
}

package main

import (
	"archive/zip"
	"io"
	"time"
)

// fixedModTime 固定条目时间戳，使同一输入产出字节级可重现的包。
var fixedModTime = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

type zipWriter struct {
	writer *zip.Writer
}

func newZipWriter(out io.Writer) *zipWriter {
	return &zipWriter{writer: zip.NewWriter(out)}
}

func (z *zipWriter) write(name string, data []byte) error {
	entry, err := z.writer.CreateHeader(&zip.FileHeader{
		Name:     name,
		Method:   zip.Deflate,
		Modified: fixedModTime,
	})
	if err != nil {
		return err
	}
	_, err = entry.Write(data)
	return err
}

func (z *zipWriter) close() error { return z.writer.Close() }

package tlsfingerprint

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type baselineRecord struct {
	TLS struct {
		JA3    string   `json:"ja3"`
		JA3MD5 string   `json:"ja3_md5"`
		ALPN   []string `json:"alpn"`
	} `json:"tls"`
}

// 随仓库保存的官方基线：用 fpcap 在 Ubuntu 上抓的 codex-cli 0.169.0 握手。
//
// 这份文件是"契约"：profile 常量一改，测试就会红，提醒重新抓包而不是悄悄漂移。
// 抓包里本来就有极少数离群连接（14 条里 1 条是 OpenSSL 形态），
// 因此断言"主导指纹"而不是"每一行都相同"。
func TestCodexRustlsProfileMatchesVendoredBaseline(t *testing.T) {
	path := filepath.Join("testdata", "codex_cli_rs_0.169.0_linux.jsonl")
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("打开基线失败: %v", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	total := 0
	dominant := 0
	alpnViolations := 0
	for scanner.Scan() {
		line := strings.TrimPrefix(strings.TrimSpace(scanner.Text()), "\ufeff")
		if line == "" {
			continue
		}
		var rec baselineRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("基线第 %d 行不是合法 JSON: %v", total+1, err)
		}
		total++
		if rec.TLS.JA3 != codexRustlsJA3 {
			continue // 已知离群连接（其它 TLS 栈），不计入主导指纹
		}
		dominant++
		if rec.TLS.JA3MD5 != "0b85eb0d4981e69064e40753e4f0ac5f" {
			t.Fatalf("主导指纹的 MD5 异常: %s", rec.TLS.JA3MD5)
		}
		if len(rec.TLS.ALPN) != 0 {
			alpnViolations++
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("读取基线: %v", err)
	}
	if total == 0 {
		t.Fatal("基线为空")
	}
	if dominant < 10 {
		t.Fatalf("主导指纹样本过少: dominant=%d total=%d", dominant, total)
	}
	if dominant*10 < total*7 {
		t.Fatalf("主导指纹占比过低: dominant=%d total=%d", dominant, total)
	}
	if alpnViolations != 0 {
		t.Fatalf("官方客户端不应协商 ALPN，出现 %d 条违规", alpnViolations)
	}
}

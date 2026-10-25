// Command packager 把插件工程打包为 .s2plugin（ZIP：manifest.json + 运行时 + UI，
// 可选 Ed25519 签名）。包格式与 backend/pkg/pluginapi/docs/package-format.md 一致：
// manifest.json 与 signature.json 自身不写入 files；签名覆盖 manifest.json 精确字节。
//
// 用法：
//
//	go run ./tools/packager -source manifest.source.json -out dist/codex-bridge.s2plugin \
//	  [-signing-key key.private -key-id my-publisher-v1]
//	go run ./tools/packager -keygen build/keys/my-publisher
package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type pluginRuntimeEntry struct {
	Path string `json:"path"`
}

type manifestOut struct {
	SchemaVersion int                           `json:"schema_version"`
	ID            string                        `json:"id"`
	Name          string                        `json:"name"`
	Version       string                        `json:"version"`
	Description   string                        `json:"description,omitempty"`
	Author        string                        `json:"author,omitempty"`
	Requires      map[string]any                `json:"requires"`
	Capabilities  []map[string]string           `json:"capabilities"`
	Runtimes      map[string]pluginRuntimeEntry `json:"runtimes"`
	UI            map[string]string             `json:"ui"`
	Files         map[string]string             `json:"files"`
}

type signatureOut struct {
	Algorithm string `json:"algorithm"`
	KeyID     string `json:"key_id"`
	Signature string `json:"signature"`
}

func main() {
	source := flag.String("source", "manifest.source.json", "源清单路径")
	out := flag.String("out", "dist/codex-bridge.s2plugin", "输出 .s2plugin 路径")
	signingKey := flag.String("signing-key", "", "Ed25519 私钥文件（base64），为空则打未签名包")
	keyID := flag.String("key-id", "", "签名 key_id（签名时必填）")
	keygenOut := flag.String("keygen", "", "生成密钥对到此前缀（.private/.public），其他参数忽略")
	flag.Parse()

	if *keygenOut != "" {
		if err := keygen(*keygenOut); err != nil {
			fatal(err)
		}
		return
	}

	if err := pack(*source, *out, *signingKey, *keyID); err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "packager: %v\n", err)
	os.Exit(1)
}

func keygen(prefix string) error {
	if err := os.MkdirAll(filepath.Dir(prefix), 0o755); err != nil {
		return err
	}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	if err := os.WriteFile(prefix+".private", []byte(base64.StdEncoding.EncodeToString(private)), 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(prefix+".public", []byte(base64.StdEncoding.EncodeToString(public)), 0o644); err != nil {
		return err
	}
	fmt.Printf("packager: 密钥已生成：%s.private / %s.public\n公钥（写入宿主 plugins.trusted_publishers）：\n%s\n", prefix, prefix, base64.StdEncoding.EncodeToString(public))
	return nil
}

// collectFiles 收集 runtimes/ 与 ui/ 下全部声明文件：path（相对插件根，正斜杠）→ 绝对路径。
func collectFiles(root string) (map[string]string, error) {
	files := make(map[string]string)
	for _, dir := range []string{"runtimes", "ui"} {
		base := filepath.Join(root, dir)
		entries, err := os.ReadDir(base)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		if len(entries) == 0 {
			continue
		}
		err = filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !d.Type().IsRegular() {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			files[filepath.ToSlash(rel)] = path
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("未发现 runtimes/ 或 ui/ 文件；先执行构建")
	}
	return files, nil
}

func pack(sourcePath, outPath, signingKeyPath, keyID string) error {
	raw, err := os.ReadFile(sourcePath)
	if err != nil {
		return err
	}
	var source manifestOut
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&source); err != nil {
		return fmt.Errorf("解析 %s: %w", sourcePath, err)
	}
	if source.ID == "" || source.Version == "" || source.SchemaVersion != 1 {
		return fmt.Errorf("清单缺少 id/version 或 schema_version != 1")
	}
	if source.UI["entrypoint"] == "" {
		return fmt.Errorf("清单缺少 ui.entrypoint")
	}

	root := filepath.Dir(sourcePath)
	files, err := collectFiles(root)
	if err != nil {
		return err
	}

	// runtimes/<goos>-<goarch>/<binary> → manifest.runtimes
	runtimes := make(map[string]pluginRuntimeEntry)
	for rel := range files {
		parts := strings.SplitN(rel, "/", 3)
		if len(parts) == 3 && parts[0] == "runtimes" {
			key := parts[1]
			if runtimes[key].Path == "" || rel < runtimes[key].Path {
				runtimes[key] = pluginRuntimeEntry{Path: rel}
			}
		}
	}
	if len(runtimes) == 0 {
		return fmt.Errorf("未发现 runtimes/<goos>-<goarch>/ 二进制")
	}
	source.Runtimes = runtimes

	// 计算 SHA-256。
	hashes := make(map[string]string, len(files))
	for rel, abs := range files {
		digest, err := fileSHA256(abs)
		if err != nil {
			return err
		}
		hashes[rel] = digest
	}
	source.Files = hashes

	manifestRaw, err := json.MarshalIndent(source, "", "  ")
	if err != nil {
		return err
	}
	manifestRaw = append(manifestRaw, '\n')

	var signatureRaw []byte
	if signingKeyPath != "" {
		if keyID == "" {
			return fmt.Errorf("-signing-key 需要同时提供 -key-id")
		}
		signature, err := sign(manifestRaw, signingKeyPath)
		if err != nil {
			return err
		}
		sig := signatureOut{Algorithm: "ed25519", KeyID: keyID, Signature: base64.StdEncoding.EncodeToString(signature)}
		signatureRaw, err = json.MarshalIndent(sig, "", "  ")
		if err != nil {
			return err
		}
		signatureRaw = append(signatureRaw, '\n')
	}

	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return err
	}
	outFile, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer outFile.Close()

	zipWriter := newZipWriter(outFile)
	if err := zipWriter.write("manifest.json", manifestRaw); err != nil {
		return err
	}
	paths := make([]string, 0, len(files))
	for rel := range files {
		paths = append(paths, rel)
	}
	sort.Strings(paths)
	for _, rel := range paths {
		data, err := os.ReadFile(files[rel])
		if err != nil {
			return err
		}
		if err := zipWriter.write(rel, data); err != nil {
			return err
		}
	}
	if signatureRaw != nil {
		if err := zipWriter.write("signature.json", signatureRaw); err != nil {
			return err
		}
	}
	if err := zipWriter.close(); err != nil {
		return err
	}

	signed := "未签名（宿主需 plugins.allow_unsigned: true）"
	if signatureRaw != nil {
		signed = "已签名 key_id=" + keyID
	}
	fmt.Printf("packager: %s\n  运行时: %v\n  文件数: %d\n  签名: %s\n", outPath, keys(runtimes), len(files), signed)
	return nil
}

func keys(m map[string]pluginRuntimeEntry) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func sign(message []byte, keyPath string) ([]byte, error) {
	raw, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, err
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil {
		return nil, fmt.Errorf("私钥不是 base64: %w", err)
	}
	var key ed25519.PrivateKey
	switch len(decoded) {
	case ed25519.SeedSize:
		key = ed25519.NewKeyFromSeed(decoded)
	case ed25519.PrivateKeySize:
		key = decoded
	default:
		return nil, fmt.Errorf("私钥长度无效: %d", len(decoded))
	}
	return ed25519.Sign(key, message), nil
}

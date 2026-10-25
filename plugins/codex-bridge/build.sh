#!/usr/bin/env bash
# codex-bridge 构建脚本：单测 → 目标平台二进制 → 打包 .s2plugin。
# 用法：
#   ./build.sh                       # 构建当前平台
#   GOOS=linux GOARCH=amd64 ./build.sh   # 交叉构建（产物追加进 runtimes/）
#   ./build.sh -signing-key k.private -key-id my-v1   # 附加签名参数透传给打包器
set -euo pipefail
cd "$(dirname "$0")"

if [ -z "${GOOS:-}" ]; then GOOS="$(go env GOOS)"; fi
if [ -z "${GOARCH:-}" ]; then GOARCH="$(go env GOARCH)"; fi

BIN="codex-bridge"
EXT=""
if [ "$GOOS" = "windows" ]; then EXT=".exe"; fi
RUNTIME_DIR="runtimes/${GOOS}-${GOARCH}"
OUT="${RUNTIME_DIR}/${BIN}${EXT}"

echo "== go vet =="
go vet ./...

echo "== go test =="
go test ./... -count=1

echo "== build ${GOOS}-${GOARCH} =="
mkdir -p "$RUNTIME_DIR" dist
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o "$OUT" ./cmd/"$BIN"

echo "== package =="
go run ./tools/packager -source manifest.source.json -out "dist/${BIN}.s2plugin" "$@"

echo "== verify =="
unzip -t "dist/${BIN}.s2plugin" >/dev/null && echo "dist/${BIN}.s2plugin OK"

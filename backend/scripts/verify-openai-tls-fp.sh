#!/usr/bin/env bash
# 端到端验证：确认"网关进程实际发出的 TLS ClientHello"与官方 Codex 基线一致。
#
# 用法（在网关机器上、网关源码目录 backend/ 内）：
#   1) go build -o fpcap ./cmd/fpcap
#   2) ./scripts/verify-openai-tls-fp.sh
#   3) 按提示把「一个测试账号」的 proxy 指向脚本打印的地址，并打开 enable_tls_fingerprint
#   4) 用该账号发一次普通请求（HTTP）和一次 WS 请求，回来按回车
#
# 判定：JA3 主导指纹必须与基线一致（exit 0），否则打印差异并 exit 1。
set -uo pipefail

LISTEN="${LISTEN:-127.0.0.1:18443}"
UPSTREAM="${UPSTREAM:-chatgpt.com:443}"
OUT="${OUT:-/tmp/gateway-tls-$(date +%Y%m%d-%H%M%S).jsonl}"
FPCAP="${FPCAP:-./fpcap}"
BASELINE="${BASELINE:-internal/pkg/tlsfingerprint/testdata/codex_cli_rs_0.169.0_linux.jsonl}"
EXPECTED_MD5="0b85eb0d4981e69064e40753e4f0ac5f"

if [[ ! -x "$FPCAP" ]]; then
  echo "找不到可执行的 fpcap（$FPCAP）。先运行: go build -o fpcap ./cmd/fpcap" >&2
  exit 2
fi
if [[ ! -f "$BASELINE" ]]; then
  echo "找不到基线文件（$BASELINE）" >&2
  exit 2
fi

rm -f "$OUT"
"$FPCAP" peek -listen "$LISTEN" -upstream "$UPSTREAM" -out "$OUT" &
PEEK_PID=$!
trap 'kill "$PEEK_PID" 2>/dev/null || true' EXIT

cat <<EOF

=== 采集已启动 ===
监听:   $LISTEN  ->  $UPSTREAM
输出:   $OUT

请执行：
  1) 选一个测试账号，把它的代理设置为:  http://$LISTEN
  2) 该账号 extra 打开:                "enable_tls_fingerprint": true
  3) 不要绑定 tls_fingerprint_profile_id
  4) 用该账号发一次请求（HTTP 与 WS 各一次更好）

完成后按回车，脚本会停止采集并判定。
EOF
read -r _

kill "$PEEK_PID" 2>/dev/null || true
wait "$PEEK_PID" 2>/dev/null || true
trap - EXIT

if [[ ! -s "$OUT" ]]; then
  echo "FAIL: 没有抓到任何连接。检查账号代理是否指向 $LISTEN，以及网关是否真的走了该账号。" >&2
  exit 1
fi

LINES=$(wc -l < "$OUT")
DOMINANT_MD5=$(grep -o '"ja3_md5":"[a-f0-9]*"' "$OUT" | sort | uniq -c | sort -rn | head -1 | grep -o '[a-f0-9]\{32\}')
ALPN_COUNT=$(grep -c '"alpn":\[\]' "$OUT" || true)

echo "=== 采集结果 ==="
echo "连接数:        $LINES"
echo "主导 JA3 MD5:  ${DOMINANT_MD5:-<空>}"
echo "无 ALPN 的连接: $ALPN_COUNT / $LINES"
echo "期望 MD5:      $EXPECTED_MD5"

if [[ "${DOMINANT_MD5:-}" != "$EXPECTED_MD5" ]]; then
  echo
  echo "FAIL: 主导指纹与官方不一致。下面是逐字段对比："
  "$FPCAP" compare -a "$OUT" -b "$BASELINE" || true
  echo
  echo "排查顺序：账号代理是否 https://（会静默降级）→ 是否有 OpenAI OAuth 插件接管 → enable_tls_fingerprint 是否真的生效（看日志 service.openai_tlsfp）。"
  exit 1
fi

if [[ "$ALPN_COUNT" -ne "$LINES" ]]; then
  echo "WARN: 有 $((LINES - ALPN_COUNT)) 条连接带了 ALPN，官方客户端不应出现。"
fi

echo
echo "PASS: 网关出站指纹与官方 Linux 客户端一致（JA3 MD5 = $EXPECTED_MD5，无 ALPN）。"

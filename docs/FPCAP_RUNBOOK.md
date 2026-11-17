# fpcap 采集与对比 Runbook

> 目的：在写任何 TLS 指纹代码之前，先拿到"官方客户端实际发了什么"的实测基线。
> 产出的 JSONL 是后续 `tlsfingerprint` profile 的数据来源，**不允许用猜测代替**。

工具位置：`backend/cmd/fpcap`。本文所有命令都在 `backend/` 目录下执行。

---

## 0. 它解决什么问题

仓库里现有的 TLS profile 是 Node.js 24（Claude Code）抓包得到的；Codex 客户端是 Rust（rustls + h2）。在不知道真实 ClientHello 的情况下伪造 profile，结果只会得到一个"谁都不像"的独特指纹，比直接用 Go 默认栈更可疑。

fpcap 只做三件事：

| 模式 | 拿到什么 | 是否需要客户端信任证书 |
|---|---|---|
| `peek` | JA3 原文/MD5、ALPN、SNI、cipher、扩展顺序、curves、sigalgs | **不需要**（握手前明文读取） |
| `serve` | h1 原始头顺序 / h2 SETTINGS + HPACK 头顺序 | 需要（终止 TLS） |
| `compare` | 两份基线的逐字段差异，exit 0/1 | — |

---

## 1. 准备

```bash
cd backend
go build -o fpcap ./cmd/fpcap
./fpcap            # 无参数打印用法
```

约定目录（建议）：

```
captures/
  codex-desktop-<version>-<os>-<yyyyMMdd-HHmm>/
    meta.txt        # 客户端版本、OS、代理拓扑、采集人、时间
    tls.jsonl       # peek 输出
    h1.jsonl        # serve -proto h1
    h2.jsonl        # serve -proto h2
```

一次采集只跑一种客户端、一种网络拓扑；更换代理或中间盒后必须重新采。

---

## 2. TLS 基线（peek）

拓扑：`客户端 → 重定向 → fpcap peek → 真实主机`

```bash
./fpcap peek -listen 127.0.0.1:18443 -upstream chatgpt.com:443 -out tls.jsonl
```

### 2.1 两种接入方式

**方式 A（推荐，零证书改动）：把 `HTTPS_PROXY` 指向 peek。** peek 支持 HTTP `CONNECT` 隧道，客户端只需要设置代理环境变量：

```bash
HTTPS_PROXY=http://127.0.0.1:18443 codex ...        # Linux/macOS
$env:HTTPS_PROXY="http://127.0.0.1:18443"           # Windows PowerShell
```

已验证：curl `-x http://127.0.0.1:18443 https://<target>` 走 CONNECT 后，peek 仍能抓到 ClientHello 并原样转发，JA3/ALPN 完整。

**方式 B（客户端不走 CONNECT 时）：网络重定向。**

| 方式 | 命令示例 | 适用 |
|---|---|---|
| DNAT | `iptables -t nat -A OUTPUT -p tcp -d <CHATGPT_IP> --dport 443 -j DNAT --to-destination 127.0.0.1:18443` | Linux / 容器 |
| hosts + 443 端口 | hosts 写 `127.0.0.1 chatgpt.com`，peek 监听 `:443`（需 root/管理员） | 单机验证 |
| 直连 IP 端口 | 客户端 base URL 改成 `https://127.0.0.1:18443` | **只做连通性验证**：SNI 会变成 IP，与真实流量不同 |

> DNAT 之后 `-upstream` 仍要填真实主机；CONNECT 模式下 peek 会用 CONNECT 的目标地址拨号，`-upstream` 只作为兜底。

### 2.2 采样与判读

- 至少 **5 次独立进程启动**（每次新连接）；同一版本多次运行 JA3 必须完全一致。
- 若同一客户端两次 JA3 不同：先怀疑中间盒/企业代理改写了 ClientHello，再怀疑版本差异。
- 记录字段：`tls.ja3`、`tls.ja3_md5`、`tls.alpn`、`tls.ciphers`、`tls.extensions`、`tls.curves`、`tls.sigalgs`、`tls.ec_point_formats`。

---

## 3. 头 / 帧基线（serve）

serve 会**终止 TLS**，因此客户端必须信任它的证书：

```bash
# 自签（把生成的证书导入系统信任库，或让客户端信任该 CA）
./fpcap serve -listen 127.0.0.1:18444 -self-signed -proto h1 -out h1.jsonl
./fpcap serve -listen 127.0.0.1:18444 -self-signed -proto h2 -out h2.jsonl

# 有内网 CA 时直接签
./fpcap serve -listen 127.0.0.1:18444 -cert server.pem -key server.key -proto h2 -out h2.jsonl
```

要点：

- **ALPN 只声明一种协议**：`-proto h1` 只给 `http/1.1`，`-proto h2` 只给 `h2`。客户端若只支持另一种，会握手失败——工具仍会记录 ClientHello 再关闭，这是刻意设计，避免"协议不确定"。
- serve **不回包**：读完首组头/帧即关闭，客户端会看到 EOF / `missing close_notify`（curl 报 `(56)`），属预期。
- 每个连接只取**第一组**请求头；h2 只解首个 HEADERS+CONTINUATION，不跟踪后续 DATA。
- 采样：每个协议至少 3 次；h1 保留头名大小写与顺序，h2 头名恒小写（协议强制）。

---

## 4. 对比

```bash
./fpcap compare -a official-h2.jsonl -b gateway-h2.jsonl        # 人类可读
./fpcap compare -a a.jsonl -b b.jsonl -json                     # JSON 报告
```

退出码：`0` 一致 / `1` 有差异 / `2` 输入错误。只读每份文件的**第一条非空记录**；多条连接请先拆文件。

按值忽略（只比存在性）的头：`date`、`x-request-id`、`authorization`、`cookie`、`x-codex-turn-state`、`session_id`、`conversation_id`、`prompt_cache_key`。

---

## 5. 判读矩阵

| 差异 | 含义 | 下一步 |
|---|---|---|
| `tls.ja3` 不同 | TLS 栈/配置不同 | 看 `ciphers`/`extensions`/`curves` 差集，按差集调 profile |
| `tls.alpn` 不同 | 协议栈不同（h2 vs h1） | 先统一协议，再比头 |
| 仅 h1 头顺序不同 | 客户端实现顺序不同；Go `http.Header` 是 map，天然不保序 | 若要完全一致，需要自定义有序 writer（成本高，优先级低） |
| 头集合多/少 | 白名单裁剪或补注 | 对照 `openaiAllowedHeaders` 与补注函数 |
| `http2.settings` 不同 | 帧参数不同 | `x/net/http2` 不可配，只能换实现 |
| 头存在但值不同 | 身份/版本漂移 | 查 UA / `version` / session 派生是否同源 |

---

## 6. 已知限制（写进结论里，别省略）

1. `peek` 无法解密：只有 TLS 层，拿不到头。
2. 证书 pinning：若客户端固定证书，`serve` 直接失败，只能退化为 `peek`。
3. 若客户端既不读 `HTTPS_PROXY` 也不发 CONNECT，需要 DNAT/hosts 重定向到 peek。
4. TLS 1.3 + ECH：SNI 可能不可见（当前抓包可见）。
5. `serve` 每连接只取首请求；h2 不解后续帧。
6. `compare` 只比首条记录；volatile 名单硬编码在 `compare.go`。

---

## 7. 本机自测（已验证，可作为工具健康检查）

以下命令在 Windows + curl 8.22 上实测通过：

```bash
# 1) 起 serve（h1，自签）
./fpcap serve -listen 127.0.0.1:18444 -self-signed -proto h1 -out serve.jsonl

# 2) 起 peek，把它指向 serve（模拟"透传链路"）
./fpcap peek -listen 127.0.0.1:18443 -upstream 127.0.0.1:18444 -out peek.jsonl

# 3) 发一个 TLS 请求（curl 会报 (56) missing close_notify，预期）
curl -k --http1.1 -H "X-Smoke: 1" https://127.0.0.1:18443/ping

# 4) 预期结果
#    peek.jsonl  : ja3 = "771,4866-4865-...,0"，alpn=["http/1.1"]
#    serve.jsonl : http1.headers = [Host, User-Agent, Accept, X-Smoke]（原始顺序）

# 5) 对比
./fpcap compare -a serve.jsonl -b serve.jsonl   # → 一致，exit 0
./fpcap compare -a peek.jsonl  -b serve.jsonl   # → http.protocol 差异，exit 1
```

这条链路验证了三件事：ClientHello 解析、透传不回放丢失字节、h1 头顺序保真。

---

## 8. 采集完成后的交付物

一份目录 + `meta.txt`，至少包含：

- 客户端名称/版本/OS/架构；
- 采集拓扑（DNAT / hosts / 直连）；
- `tls.jsonl`（≥5 条）、`h1.jsonl`（≥3 条）、`h2.jsonl`（≥3 条）；
- 与网关出站的 `compare -json` 报告。

后续动作：按 `tls.*` 差集实现对应 `tlsfingerprint.Profile`；按头顺序差异决定是否需要有序 writer；h2 差异留给 sidecar 方案。

---

## 9. ????? profile

?? `tls.jsonl` ??????? profile,???????:

```bash
./fpcap profile -in tls.jsonl -name codex_cli_rs_0.153.4 -out profile.json
./fpcap profile -in tls.jsonl -name codex_cli_rs_0.153.4 -go      # ???? Go ???
```

??(????????):

- GREASE(`0x?a?a`)????,?? `notes` ??????;
- `supported_versions` ? `"0x0304"` ??? `772`;
- `tlsfingerprint` ????????????? `notes`:????????? `GenericExtension`,**???????????**;
- ????????(?? `psk_modes`)?????,????

???????????????????(????):

1. ?? `tls_fingerprint_profiles`(???cipher?curves????);
2. OpenAI ????? profile ??? `enable_tls_fingerprint`--?????????????????
---

## 10. 端到端验证网关出站指纹

工具证明"仿真握手对"，不等于"网关真的这么发"。上线前必须用网关自己的出站跑一次：

```bash
# 网关机器、backend/ 目录
go build -o fpcap ./cmd/fpcap
./scripts/verify-openai-tls-fp.sh
```

脚本会：起 `peek` → 提示你把**一个测试账号**的代理指向它并打开 `enable_tls_fingerprint` → 你发一次请求 → 脚本比对随仓库保存的官方基线
`internal/pkg/tlsfingerprint/testdata/codex_cli_rs_0.169.0_linux.jsonl`，输出 PASS/FAIL。

判定标准：主导 JA3 MD5 = `0b85eb0d4981e69064e40753e4f0ac5f`，且所有连接无 ALPN。

Windows（cmd / PowerShell 都可以）：

```bat
cd backend
go build -o fpcap.exe ./cmd/fpcap
powershell -NoProfile -ExecutionPolicy Bypass -File scripts\verify-openai-tls-fp.ps1
```

只判定已有抓包（不起监听）：

```bat
powershell -NoProfile -ExecutionPolicy Bypass -File scripts\verify-openai-tls-fp.ps1 -Capture path\to\cap.jsonl
```

注意：`.ps1` 必须带 UTF-8 BOM，否则 Windows PowerShell 5.1 会把中文注释按 ANSI 解析并报语法错误；`.sh` 只能在 Git Bash / WSL 里跑，cmd 不能直接执行。

失败时按顺序排查：

1. 账号代理是 `https://` 或未知 scheme → 指纹会静默降级为 Go TLS；
2. 有 OpenAI OAuth 插件接管 → 看日志 `service.openai_tlsfp` 的告警；
3. `enable_tls_fingerprint` 没生效 → 看是否被全局开关或账号缓存覆盖；
4. 账号绑了 `tls_fingerprint_profile_id` → 会改用 DB 里的 Chrome 形态模板。
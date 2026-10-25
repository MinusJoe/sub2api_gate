# 角 A：官方对「打票/共享票据」的反制措施（notes-4-counter）

- 作者：`agent4-counter`（角 A）。只读取证；**未执行 codex.exe**；未联网；未改动任何原始文件。
- 目标：`C:\Users\zfx\AppData\Local\OpenAI\Codex\bin\a96ca4c1eee0cf35\codex.exe`（PE x86-64，336,011,568B，SHA256 `E757246B4DE87359E660647FA9514955A0971FF6A30AFFDD8C145D1D5ECE7B84`）。
- 数据源：二进制（自写静态工具 `scratch/tmp/binre/`，numpy 2.3.5）、`scratch/sqlite-copy*/logs_2.sqlite`（合并 48,579 行）、网关源码 `backend/internal/service/openai_codex_{turn_state,fingerprint,identity,ticket}.go`。
- 纪律：token/cookie/turn-state 只报长度/前缀/结构/时间/位置，**不贴明文**；email/sub 脱敏。
- 结论分级：**观察** = 二进制字符串/指令或日志直接可见；**推断** = 由观察组合出的行为判断；**未知** = 本机无法回答。引用其它 teammate 笔记时标「二手」；网关源码对 OpenAI 服务端行为的描述一律标「推断（网关源码，二手实测）」。

> 坐标系：`0x...` 一律为 **RVA**（相对 image_base `0x140000000`）；`.rdata` 起始 RVA `0xf621000`/rawptr `0xf61f800`，故 `RVA = 文件偏移 + 0x1800`。本轮所有 RVA 由 `pe.off2rva()` 计算（自洽）。原生 REA provider 不可用（ghidra `not_configured`、java 解析失败、hopper/ida/kuna 不可用），证据来自自写静态解码器，Rust 函数只追到「字符串引用 + 头名/长度 + 值常量」粒度，未做伪代码级还原。

---

## 0. 一句话结论

**OpenAI 的反制不是「一张票一个密码」，而是一整条「身份自洽 + 边缘粘性 + 服务端加密票据 + 账号分类 + 风控词表」的纵深链。** 其中：

1. **唯一真正「实锤在跑」的反制是软性的**：`x-codex-safety-buffering-enabled: true` + `faster-model: gpt-6-luna`（209/347 = 100% 命中 astra/6.1-sol 的 HTTP 响应）——这是服务端把「满血模型」往「更快(更弱)模型」引导的换模提示，与 turn-state 是**两套机制**。
2. **其余「硬」反制全部是客户端内置的词汇/能力，本机窗口内一次都没触发**：`cyber_policy / bio_policy / misalignment_policy_violation / too_many_denials / organization_spend_limit_exceeded / x-openai-authorization-error / Luna classification / attestation` 在 48,579 行日志里 **全 0 次**（401/403/429 的命中全是 UUID/base64 片段，二手 client-evidence §5.1）。
3. **`x-oai-attestation` 是设备级 attestation 的强候选**：它由 Codex Desktop 的 **app-server 守护进程** 通过 `attestation/generate` 请求现场生成「attestation envelope」，且存在 `device_key_bindings(key_id/account_user_id/client_id)` 设备密钥表 + Ed25519/P-256 ECDSA 密码学件；**CLI/exec 模式无法生成**（`attestation generation is not supported in exec mode`）。这是打票探针最可能「缺的那一块」。
4. **turn-state 是服务端加密 + HMAC 的 per-turn Fernet 令牌**（1+8+16+528+32=585B），内嵌铸造时间戳 → 服务端可做 TTL 与单次性；客户端无法伪造（HMAC 密钥在服务端）。跨账号回放是「真实 Codex 永远不会产生的矛盾信号」。

---

## 1. `x-oai-attestation`（Q1）——设备/安装级 attestation？

### 1.1 头本身（观察）

- 字符串 **5 份副本**，RVA：`0xfadadd8 / 0xfb1d990 / 0xfb74d28 / 0xfbc04c8 / 0xfd7e8c8`。
- **全部**与 `responses_websockets=2026-02-06` / `OpenAI-Beta` / `x-responsesapi-include-timing-metrics` 连排（同簇），HTTP `responses.rs` 头簇里**不出现** → **WS 握手专属头**，值运行时计算、无字面量（二手 notes-1-wire §4.4 与此一致；本轮复核）。
- 日志里 `x-oai-attestation` **0 次**（客户端不落它发的头，也不落 WS 握手头）→ 值无法从本机日志观测。

### 1.2 值从哪来（观察，本轮新发现，此前未记录）

`x-oai-attestation` 的值**不是客户端本地拼的常量**，而是走一条「app-server 请求处理器」链路：

| 字符串 | RVA | 含义 |
|---|---|---|
| `app-server\src\attestation.rs`（trace 点 `:86/:93/:101/:118/:168`，`:101` 带 `timeout_seconds`，`:118` 带 `error_category`） | 簇 `0xf8d2cd8..0xf8d2e50` | **attestation 生成逻辑在 app-server**（Codex Desktop 后端守护进程） |
| `failed to serialize app-server attestation envelope` | `0xf8d2cd8` | 产物叫「attestation envelope」（信封），需要序列化 |
| `failed to deserialize attestation generation response` | `0xf8d2d60` | 生成是一次「请求→响应」 |
| `attestation generation request failed / canceled / timed out` | `0xf8d2db8 / 0xf8d2e09` | 有超时、可取消 |
| `attestation/generate`（`ServerRequest` 枚举成员） | `0xf977468` 区 | app-server 暴露 `attestation/generate` 请求类型 |
| `attestation generation is not supported in exec mode` | 见 exec-mode 错误表 | **CLI `codex exec` 模式无法生成** |

### 1.3 设备密钥材料（观察）

- 客户端 state DB 建表：`CREATE TABLE device_key_bindings ( key_id TEXT PRIMARY KEY NOT NULL, account_user_id TEXT NOT NULL, client_id TEXT NOT NULL, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL )`（RVA `0xff953cb`）。
  → 客户端持有「设备密钥」，按 `(key_id, account_user_id, client_id)` 绑定。这是 attestation 的签名密钥候选。
- 密码学件：`ed25519`（27 次）、`elliptic_curve::secret_key::SecretKey<p256::NistP256>`（`0x…` p256 区）、`ring-0.17.14\src\ec\suite_b\ecdsa\signing.rs`（14 次 ecdsa）→ Ed25519 / P-256 ECDSA 都在二进制里，可支撑「设备密钥签名 attestation envelope」。

### 1.4 判定

| 问题 | 结论 | 级别 |
|---|---|---|
| 是设备/浏览器级 attestation 吗 | **是「设备/安装级」attestation 的强候选**（app-server 现场生成 + 设备密钥表 + Ed25519/P-256），但**不是浏览器级**（无 WebAuthn/TPM 字面量，`TPM`=0 次、`remote attestation`=0 次） | 推断 |
| 伪造难度 | **高**：值由 app-server 守护进程生成（信封序列化），绑定设备密钥 `device_key_bindings`；CLI/exec 无 app-server 时 `attestation generation is not supported` → 打票探针（纯 HTTP、无 app-server）**无法原样产出** | 推断 |
| 是否实锤「服务端校验它」 | **未知**：服务端是否强制校验、缺它是否不铸票，本机无法观测（静态只能证明「客户端会发 + 生成需要 app-server」） | 未知 |

> 对打票的含义：sub4api 探针走 HTTP POST（不带 attestation，也不带 `OpenAI-Beta`），而真实客户端拿票时 HTTP 首请求**也不带** attestation（attestation 只在 WS 握手带）。所以「HTTP 拿票」缺 attestation 未必是硬伤；但「WS 回带/续传」缺 attestation 很可能露馅——这正是 §8 表里「attestation：WS 续传必经」的来源。

---

## 2. `x-codex-turn-state` 反重放（Q2）

### 2.1 结构（二手，notes-2-identity §3 + client-runtime §3，本轮接受其数学复核）

- 780 字符 = `gAAAAA` 前缀 + base64url，解码 **585 字节** = `1(版本0x80) + 8(大端时间戳) + 16(IV) + 528(密文=33×16) + 32(HMAC)`。
- = **Fernet（AES-128-CBC + HMAC-SHA256）**；非 AES-GCM、非 gzip（`gAAAAA` ≠ `H4sI`）。
- 末样本时间戳 `1792683611` = 2026-10-22T15:40:11Z = 铸造时刻。

### 2.2 反重放能力（推断，从结构交叉）

| 机制 | 依据 | 级别 |
|---|---|---|
| **TTL** | 内嵌 8 字节铸造时间戳 → 服务端可设过期（网关默认 `ttl_seconds:3600` 只是我们侧镜像，OpenAI 侧 TTL 未知） | 推断 |
| **单次性/每 turn 一张** | 139/141 turn 首请求带票、203 条续请求 0 票 → 每 turn 一张（二手 client-evidence §3.2） | 观察 |
| **不可伪造** | HMAC 密钥在服务端；客户端只能原样回带 | 观察 |
| **跨账号可检测** | 网关 `openai_codex_turn_state.go`：blob 在「出站身份（含收敛后 installation/session/thread）」下铸造，跨账号回放是「真实 Codex 永远不会产生的矛盾信号」，`guardOpenAICodexTurnStateEcho` 剥离已知异账号回带值 | 推断（网关源码，描述 OpenAI 侧语义） |
| **HMAC 密钥分段（按账号/org/installation）** | 无法从密文结构判定（只能证「加密+签名」），但「跨账号回放=矛盾」的语义要求密钥至少按账号/安装上下文分段 | 未知（仅推断） |

### 2.3 是否实锤「服务端会拒重放」

- 本机窗口内**无任何 401/403/429**（全是 200），故「重放被拒」本机**未实锤**。
- 网关侧 `guardOpenAICodexTurnStateEcho` 是**我们自己的防御代码**，不是 OpenAI 的行为证据；它只证明「这个矛盾信号是真实客户端的形态不变量」，OpenAI 服务端是否真的校验仍是**未知**。

---

## 3. 身份 / 行为指纹（Q3）

### 3.1 六标识（二手 notes-2-identity §1.1，本轮复核关键点）

- `installation_id` = **UUIDv4 随机、设备级、账号无关**（`.codex/installation_id`，122bit 随机，无法从 account/sub/org 派生）。
- `session_id/thread_id/turn_id` = UUIDv7（时间有序）；首线程复用 session_id；每 turn 新 turn_id。
- `window_id = thread_id:ordinal`、`root_turn_id`（fork 树）。

### 3.2 `x-codex-turn-metadata` 32 字段（观察，头名 RVA `0xf722f73`，字段簇 `0xf722f00..0xf7230c0`）

`installation_id, session_id, thread_id, turn_id, window_id, window_number, context_window_id, parent_thread_id, parent_turn_id, root_turn_id, forked_from_thread_id, forked_from_ordinal_exclusive, agent_name, request_kind, compaction, thread_source, turn_trigger, subagent_kind, code_mode_tool_names, tool_namespaces_info, sandbox, sandbox_mode, workspaces, turn_started_at_unix_ms, history_ingest_requested, history_initialization, auto_review_enabled, node_repl_auto_review_required, node_repl_disabled, analytics_enabled, guardian_credits_requested, mcp_attribution`

### 3.3 服务端最可能校验、且伪造易露馅的（推断，按证据强度）

1. **身份链自洽**：`x-codex-turn-metadata` 的 `installation_id/session_id/thread_id/turn_id/window_id` 与 HTTP 头（`x-codex-installation-id`/`x-codex-window-id`/`session_id`/`thread_id`）及 body `client_metadata` 三处**必须一致**（网关 `applyCodexFingerprint*` 显式保证三者共享同一 ID 集合，反推 OpenAI 会比对）。
2. **原生客户端指纹**：`thread_source`（desktop/cli/vscode）/`request_kind`（turn/compaction）/`turn_trigger`/`agent_name`/`subagent_kind`/`sandbox`/`sandbox_mode` —— 官方客户端才会自洽填出，非官方 HTTP 打票最容易在这里露馅。
3. **UA/originator/version 配套**：`originator` 与 UA 首段必须配套、`version` 不得低于 `0.144.0`（低于则上游 404）——网关 `openai_codex_identity.go` 明写「否则上游 404，issue #3901，2026-07 实测」。
4. **设备/会话指纹敏感**：网关收敛模式历史（#5553→#5610）证明「1 账号 = 1 设备 = 恒定会话」的收敛会致额度缩水，即 OpenAI **对设备/会话指纹敏感**；打票若把 installation 收敛成账号级恒定值，与真实「1 设备 + 每会话新 session + 每 turn 新 turn」形态不一致，是探针身份不被认可的高概率原因（推断，二手 notes-2-identity §1.2）。

---

## 4. 降智 / 换模 enforcement（Q4）

| 信号 | 位置 | 观察/推断 |
|---|---|---|
| `x-codex-safety-buffering-enabled` / `-faster-model` | 解析函数 `0x3d56430`（二手 notes-3-routing §1.1；343B，从响应头 map 读，`-enabled` 只选分支不 gate 结果）；字符串 `0xf8c3c08`/`0xf8c3c28` | 服务端说「此模型拥塞，可用 faster-model 重试」；**本机 209/347 = 100% 命中 astra/6.1-sol**，值恒定 `true`/`gpt-6-luna` |
| `response.created.response.model` / `openai-model` | `openai-model` 读取在 `0x3d135c0`（SSE responses.rs）+ 6 个 WS connector；`response.created` 事件名在 `0xf8bcd80` 区 | **权威换模判据**；但客户端不落 SSE/WS body，本机**观测不到**（日志 0 次） |
| `fast_mode`/`ultrafast_mode`/`step_model_switching` | 特性名簇 `0xf6ed1b0`/`0xf7b3121` | 「降智」相关三特性；本账号 fast/ultrafast=true、step_model_switching=false |

**结论**：官方「降智」的落点是**服务端路由决定**（safety-buffering 头 + 流内换模），任何本地票据（turn-state）都无法覆盖它。降智与 turn-state 是**两套独立机制**（二手 notes-3-routing §0 一致）。

---

## 5. 账号分类 / 额度（Q5）

| 信号 | RVA | 语义 |
|---|---|---|
| `Luna classification` / `account changed during Luna classification` | `0xfcc0c08 / 0xfcc0c1f`（`core\src\environment_selection.rs`） | **服务端存在账号分类（Luna）**；分类变化会触发环境重选 |
| `standard_user_acl` / `Cyber access programs are disabled for this API-key session` | `0xfb34916 / 0xfb348ac` | API-key 会话受限访问 cyber program；账号 ACL 分级 |
| `plan_type rate_limit spend_control rate_limit_reached_type` | `0xf727098` | 订阅计划/限流/消费控制模型 |
| `allowed limit_reached primary_window secondary_window rate_limit_reached workspace_owner_credits_depleted workspace_member_credits_depleted workspace_owner_usage_limit_reached workspace_member_usage_limit_reached used_percent limit_window_seconds remaining_percent reset_after_seconds` | `0xf780eb4 / 0xfd53946` | 限流窗/额度耗尽判定字段全集 |
| `x-codex-rate-limit-reached-type` / `x-codex-active-limit` / `x-codex-credits-*`（响应头） | `0xf8bf02b` / `0xf8bea18` / `0xf8bef3b` | 服务端下发的额度/计划信号（客户端只读） |
| `organization_spend_limit_exceeded` / `organization_usage_limit_exceeded` + `x-openai-authorization-error`（响应头） | `0xf8be844 / 0xf8be865 / 0xf8be828` | 组织级额度超限 → 授权错误头 |

**结论**：OpenAI 服务端按 `plan_type`（free/go/plus/pro/.../enterprise_cbp_*）+ `primary/secondary window` + `credits` + `Luna classification` 做账号分类与额度/限流 enforcement；这些都是**服务端判定**，客户端只能读响应头、不能伪造「更高档位」。

---

## 6. 边缘反爬（Q6）

| Cookie | 二进制出现 | 出现端点（本机日志） | 值长 | 性质 |
|---|---|---|---|---|
| `__oailb` | **0 次** | 仅 `GET /backend-api/codex/models`（128） | 317 | 服务端/边缘路由粘性 cookie（HttpOnly/Secure/SameSite=Lax），与 `__cflb` 成对 |
| `__cflb` | 0 次 | 仅 `/models`（与 `__oailb` 成对，128） | 43 | 边缘负载均衡粘性 |
| `__cf_bm` | 0 次 | `/models`（470）+ **`/responses`（15/347）** | 197–199 | **Cloudflare Bot Management** cookie（唯一在 /responses 上出现的 Set-Cookie） |

**判定**（观察+推断）：
1. 客户端 cookie jar（`reqwest-0.12.28` + `cookie_store-0.22.1`）对任何 Set-Cookie **被动存取**，不认识任何具体 cookie 名 → 三个 cookie 都是**服务端/Cloudflare 边缘下发**，客户端只是回带。
2. `__cf_bm` 是 **Cloudflare Bot Management**（反爬/机器识别）——住宅代理轮换 + cookie 不一致时，`__cf_bm` 与边缘 IP/指纹绑定会失效。
3. 打票在 `/responses` 抓 Set-Cookie 只能抓到 `__cf_bm`（`__oailb` 只从 /models 来）→ 「cookie-only 假票」。
4. 住宅代理 + 全站共享 cookie 的**可识破点**：cookie（`__cf_bm`/`__oailb`）绑定账号+IP+边缘节点，跨 IP 复用会与边缘粘性矛盾（推断，二手 notes-2-identity §5.2 一致）。

---

## 7. 风控错误码 / 文案（Q7）

### 7.1 错误枚举（观察）

`CodexErrorInfo`（RVA `0xf7e6db0`，蛇形；camelCase 在 `0xf7f578e` 区）：
`context_window_exceeded, session_budget_exceeded, usage_limit_exceeded, rate_limit_exceeded, flex_unavailable, server_overloaded, cyber_policy, bio_policy, misalignment_policy_violation, too_many_denials, http_connection_failed, http_status_code, response_stream_connection_failed, internal_server_error, unauthorized, bad_request, invalid_prompt, sandbox_error, response_stream_disconnected, response_too_…`

另有第二套 session/turn 错误枚举（`0xf8a7161` 区）含 `thread_not_found, agent_limit_reached, session_configured_not_first_event, request_timeout, spawn_interrupted, unexpected_status, invalid_request, invalid_prompt, tool_collision, invalid_image_request, usage_limit_reached, server_overloaded, flex_unavailable, cyber_policy, bio_policy, misalignment_policy_violation, response_stream_failed, connection_failed, quota_exceeded, usage_not_included, internal_server_error, retry_limit, internal_agent_died, landlock_sandbox_executable_not_provided, unsupported…`

### 7.2 拒绝文案（观察）

| 文案 | RVA | 对应 |
|---|---|---|
| `This request has been flagged for possible cybersecurity risk.` | `0xf8beb0e`（+`0xf8c3d24`） | `cyber_policy` |
| `This content was flagged for possible biological risk.` | `0xf8beb0e` 邻 | `bio_policy` |
| `The image data you provided does not represent a valid image` | 同上簇 | 图像校验 |
| `This request was blocked due to a misalignment policy violation.` | `0xf8bdf99`（+`0xf8c3134`） | `misalignment_policy_violation` |
| `MisalignmentErrorDetails`（struct，4 元素）+ `x-oai-request-id` + `cf-ray` + `reason` + `incomplete_details` | `0xf8bdf30` / `0xf977fe8` 区 | misalignment 错误详情结构（含 Cloudflare `cf-ray`、服务端 `x-oai-request-id`） |

### 7.3 是否实锤「触发过」

**否。** 48,579 行日志里 `cyber_policy / bio_policy / misalignment_policy_violation / too_many_denials / flagged for possible cybersecurity / x-openai-authorization-error / organization_spend_limit_exceeded / server_overloaded / MisalignmentErrorDetails / Luna classification` **全部 0 次**。`401/403/429` 的 1764/488/381 次命中全是 UUID/base64/时间戳片段（二手 client-evidence §5.1 一致）。

→ 这些错误码是**客户端已知的「OpenAI 风控词表」**（客户端知道如何解读/展示），但**本机窗口内官方客户端一次都没命中**。它们的存在证明 OpenAI 有这套 enforcement 能力，但触发条件是「误用/策略违规/额度超限」，合法官方客户端很少命中。

---

## 8. 官方反制机制清单（结论表）

| # | 机制 | 触发条件 | 可观测性 | 能否安全绕过 | 证据 |
|---|---|---|---|---|---|
| 1 | **`x-oai-attestation`**（WS 握手专属，设备级 attestation） | WS 握手（续传/回带票）时校验 | 客户端不落日志；值由 app-server `attestation/generate` 现场生成 | **否（WS 路径）/ 未知（HTTP 首请求不需要）** | RVA `0xfadadd8/0xfb1d990/…`；`attestation.rs`；`device_key_bindings`；`attestation generation is not supported in exec mode` |
| 2 | **turn-state 反重放**（Fernet：版本+时间戳+IV+密文+HMAC） | 同 turn 续传、跨 turn/跨账号回放 | 响应头 `x-codex-turn-state`（780 字符，`gAAAAA`） | **否（不可伪造）/ 跨账号回放可检测** | 585B 结构；时间戳=铸造时刻；网关 `guardOpenAICodexTurnStateEcho` |
| 3 | **身份链自洽**（installation_id/session/thread/turn/window + 头体一致） | 每次 /responses 请求 | 请求头 + body `client_metadata` + `x-codex-turn-metadata` 32 字段 | **否（伪造易露馅）** | `.codex/installation_id`(UUIDv4)；`x-codex-turn-metadata` 头名 `0xf722f73` |
| 4 | **UA/originator/version 门槛** | 每次请求 | 请求头 | **否（低于 0.144.0 直接 404）** | 网关 `openai_codex_identity.go`（issue #3901） |
| 5 | **降智/换模**（safety-buffering + 流内换模） | 模型拥塞/降载 | 响应头 `x-codex-safety-buffering-*`（本机 209/347）+ 流内 `response.created.response.model`（本机不可得） | **未知（服务端路由，本地票据不可覆盖）** | `0x3d56430`；`0xf8c2408` |
| 6 | **账号分类/额度**（Luna + plan_type + window + credits + org spend） | 超限/降档 | 响应头 `x-codex-active-limit/credits-*/rate-limit-reached-type` + `x-openai-authorization-error` | **否（服务端判定，客户端只读）** | `0xfcc0c08`(Luna)；`0xf727098`(plan)；`0xf8be828`(org spend) |
| 7 | **边缘反爬**（`__cf_bm` Cloudflare Bot Mgmt + `__oailb`/`__cflb` 粘性） | IP/边缘粘性 + cookie 一致 | Set-Cookie（`__cf_bm` 在 /responses 15/347；`__oailb` 只在 /models） | **否（跨 IP 复用会露馅）** | 二进制 0 次（客户端不认识）；日志 cookie 分布 |
| 8 | **风控错误码/文案**（cyber/bio/misalignment/too_many_denials） | 策略违规/denial 过多 | 客户端错误枚举 + 拒绝文案 | **未知（本机从未触发）** | `CodexErrorInfo` `0xf7e6db0`；文案 `0xf8beb0e/0xf8bdf99` |

---

## 9. 未知项 / 边界

1. **`x-oai-attestation` 的精确算法**（Ed25519 还是 P-256 签名、签什么 claims、服务端是否强制校验）——本机只能证明「app-server 生成 + 设备密钥表 + 密码学件」，签名算法与校验是**未知**。
2. **服务端铸票准入函数**（是否必须 attestation、是否校验 installation/thread_source、是否受 org/workspace 限制）——服务端策略，**未知**。
3. **turn-state 的 HMAC 密钥分段粒度**（按账号/org/installation？）——从密文结构不可判定，**未知**。
4. **Fernet 的 TTL 精确值**——内嵌时间戳可做 TTL，但具体过期时长**未知**（网关默认 3600 只是我们侧镜像）。
5. **`server_is_overloaded` vs `server_overloaded`**：二进制里只有 `server_overloaded`（错误枚举），`server_is_overloaded` 字面量 **0 次**；网关注释里的「流内 server_is_overloaded」与客户端枚举名略有出入（可能是服务端 SSE 事件名，客户端不落日志故不可见）——**未知**。
6. **401/403/429 触发面**：本机窗口无任何 HTTP 拒绝，故「官方客户端何时会被风控拒绝」**未知**（打票探针的形状差异可能在别处命中，本机不可观测）。
7. 原生 REA provider 不可用 → 所有函数级证据停在「字符串引用 + 头名/长度」粒度，未做伪代码还原。

---

## 10. 复现（只读）

```powershell
cd D:\projects\sub4api\scratch\tmp\binre
python counter_scan.py          # 反制字符串 RVA + 上下文 -> counter_scan.json
python attest_scan.py           # attestation/device-key/密码学件
python dump_devkey.py           # device_key_bindings 建表
python dump_attest.py           # attestation.rs 生成链路字符串
cd D:\projects\sub4api
python scratch\tmp\binre\log_counter_scan.py   # 日志交叉验证（反制 token 全 0）
```

---

## 11. 从角 A 出发的方案建议（1 最推荐 + 2 次选）

### 最推荐：**打票探针补齐「app-server attestation」不可行 → 改走「HTTP 首请求形状对齐」，不碰 attestation**

- 依据：真实客户端拿票是「HTTP 首请求（不带 attestation、不带 OpenAI-Beta）→ 读响应头 `x-codex-turn-state`」；attestation 只在 **WS 续传/回带**才需要。探针当前拿不到票的最可疑原因不是缺 attestation，而是**身份/形状不自洽**（`OpenAI-Beta: responses=experimental` 自造头 + 0.146.0 旧版 + `codex-tui` 头 + `Codex Desktop` body 矛盾 + 无 tools + 7 字段 turn-metadata）。
- 做法：删自造 `OpenAI-Beta`；版本对齐 0.163.0；originator/UA/body `session_meta.originator` 同源；turn-metadata 补全 32 字段；body 加真实 tools；`installation_id` 用真实 `.codex/installation_id`（设备级）而非收敛值。
- 可行性：**高**（只改探针构造）；风险：**中**（更真实 = 更接近「模拟官方客户端」，有封号面）；成本：**低-中**（需 A/B 验证「带 tools 的真实回合」能否稳定铸票）。

### 次选 A：**把「降智检测」判据从票形换成流内实际模型（safety-buffering 头 + `response.created.response.model`）**

- 依据：唯一实锤在跑的反制/换模信号是 `x-codex-safety-buffering-enabled: true + faster-model: gpt-6-luna`（209/347），而「请求模型 == 实际被服务模型」的权威判据是流内 `response.created.response.model`（本机日志拿不到，网关读 SSE/WS body 可拿）。
- 做法：网关读 SSE/WS `response.created.response.model` + `response.metadata.openai-model` 与请求模型比对，命中即标记「疑似降智」并冷却/换号；不必再依赖 turn-state。
- 可行性：**高**；风险：**低-中**；成本：**低-中**。

### 次选 B：**turn-state 从「全站共享」改为「per-account/per-turn 透传 + 跨账号剥离」，且只在 WS 握手注入**

- 依据：官方契约是 per-turn sticky token（同回合 WS 回带、跨回合/跨账号清空）；网关 `guardOpenAICodexTurnStateEcho`/`relayOpenAICodexTurnState` 已实现溯源与剥离骨架，缺的只是「注入点改到 WS 握手 + 去掉 292 长度假设」。
- 可行性：**中**（需接通 WS 转发路径 + 去掉 292/780 长度假设）；风险：**低**（不伪造、不跨账号）；成本：**中**。

# 角 2：`x-codex-turn-state` 身份链与票据语义（证据版）

- 作者：`agent2-identity`。只读，未执行 codex.exe。
- 数据源：`C:\Users\zfx\.codex\`（只读）+ `scratch/sqlite-copy*/` 副本 + `backend/internal/service/openai_codex_ticket*.go` 等网关源码。
- 纪律：token / cookie / turn-state **只报长度、前缀、结构、时间、位置，不贴明文**；email/sub 脱敏。
- 结论分级：**观察** = 本机直接读到（文件/DB/源码/二进制字符串）；**推断** = 由观察组合；**未知** = 本机无法回答。二手线索（其他 teammate 笔记 / 社区）一律标注来源与可信度。

---

## 0. 一句话结论

`x-codex-turn-state` **不是「VIP/防降智凭证」，而是服务端按“账号 + 安装 + 会话/回合”铸造的、per-turn 的 Fernet 加密不透明能力令牌**（sticky routing token）。它绑定的是「同账号 × 同 installation × 同 turn」；跨账号、跨回合复用与客户端用法相矛盾，会被识别/忽略。全站共享一张票 + 注入 HTTP 业务请求，是「票据语义错配」，不是单纯的长度/配置 bug。

---

## 1. 身份链（Q1）

### 1.1 六个标识的来源 / 生成时机 / 相互关系

| 标识 | 格式 | 来源与生成时机 | 稳定度 | 是否账号派生 |
|---|---|---|---|---|
| `installation_id` | **UUIDv4**（随机，122bit） | 本机 `.codex/installation_id`（36B，版本 nibble=4，变体=a），安装/首次启动生成一次 | 跨会话/跨线程/跨账号**稳定**（设备级） | **否**（纯随机） |
| `session_id` | **UUIDv7**（时间有序） | 每次应用/桌面会话启动生成 | 进程/会话级 | 否 |
| `thread_id` | **UUIDv7** | 每个对话线程；**新会话首线程复用 session_id**；fork 时生成新 UUID | 线程级 | 否 |
| `turn_id` | **UUIDv7** | 每一条用户消息（每 turn）生成 | turn 级 | 否 |
| `window_id` | `thread_id:ordinal`（推断） | 每窗口 | 线程级 | 否 |
| `root_turn_id` | UUID | turn 树的根（子代理 fork 场景），见 `forked_from_thread_id/forked_from_ordinal_exclusive/parent_thread_id/parent_turn_id` | turn 树级 | 否 |

**证据（观察，第一手）**：
- `.codex/installation_id` = `d1e66d9f-…`（36 字节，第三组 `47d2` 首字符 4=版本4，第四组 `a1cd` 首字符 a=变体1）→ 随机 UUIDv4，**与账号无关**（122bit 随机，无法从 account/sub/org 派生；已测 sha256/sha1/md5 对 account/user/org/sub 及常见拼接均无匹配）。
- rollout 转录（`archived_sessions/sessions` 199 个 `.jsonl`）实测：`session_id`=`01a13a22-a452-77a0-a591-a34f47884a44`、`thread_id` 同值（**首线程复用 session_id**）、`turn_id`=`01a13a22-a6d6-7730-a3af-0fe7700c5c89`（不同），三者**全是 UUIDv7**（版本 nibble=7）。
- 双 UUID 文件名 `rollout-…_01a1423e-…_01a148a2-….jsonl` = 父线程 + fork 线程 → 印证 `parent_thread_id`/`forked_from_thread_id` 关系。
- `window_id`/`root_turn_id` 在 rollout 里 **0 次**出现（它们只在请求头 / turn-metadata JSON 里，不出现在服务端回显的 response 事件中）。

**关键判断（Q1 的“installation_id 是不是账号派生的稳定值”）**：**不是账号派生**。它是设备级随机 UUIDv4，是「客户端身份链里唯一长期稳定的标识」，也是 turn-state 与“原生客户端”之间的**设备绑定锚点**。账号绑定走的是 `Authorization: Bearer <id_token>` + `chatgpt-account-id`（见 §4），不是 installation_id。

> 补充（观察+未知）：`models_cache.json` 顶层 `identity = 75dd36e1…`（64 hex = 32B）。已测非 sha256/sha1/md5(installation_id 或 account/user/org/sub 常见拼接) → **服务端侧不透明指纹**，与 installation_id 是两回事；其生成式未知（可能 = hash(installation + account + salt)）。

### 1.2 网关侧如何“伪造”这条链（源码观察，非真实客户端）

- `codex_probe_template.go buildCodexProbeRequest()`：installation 由 `scopeCodexAccountIdentityValue(credential, 0, "installation", "codex-probe-v1")` 派生；`newCodexReplayIdentity()` 给 session/window/turn 各发**新 UUIDv7**。
- `codex_replay_request.go`：合成 body 里 `thread_id = session_id`，`turn_started_at_unix_ms`、`request_kind="turn"`；头带 `session_id/session-id/thread-id/x-client-request-id/x-codex-installation-id/x-codex-window-id/x-codex-turn-metadata`。
- `openai_codex_fingerprint.go`：网关的「收敛模式」（off/device/session/full）可把 installation/session/thread 改写成**账号级恒定值**（`deriveStableUUIDv4("sub2api:codex-install-id:v2:"+seed)`）。这**不是**真实客户端的 installation_id——它只为“多人共享一号时收敛设备/会话指纹”服务，`#5553→#5610` 注释明说收敛会改变上游可见的设备/会话数，**历史上曾导致额度缩水**。

→ 含义：真实客户端的 installation_id 是「设备」粒度；网关若为打票强行收敛成“账号级恒定 installation_id”，会制造「1 账号 = 1 设备 = 恒定会话」的指纹，与真实桌面客户端「1 设备 + 每会话新 session + 每 turn 新 turn」的形态**不一致**，这正是打票探针身份不被认可的高概率原因之一（推断）。

---

## 2. `x-codex-turn-metadata` 字段全集与含义（Q2）

**第一手验证**：`scratch/tmp/binre/region.py 0xf722ec7` 打印 `.rdata` 连续字符串簇（608B），得到 serde 序列化字段表。**比 notes-binary §4.2 多了 3 个 JSON 字段**（`guardian_credits_requested`、`mcp_attribution`、`compaction`），并含 5 个相邻的 HTTP 头名常量。

### 2.1 JSON 字段全集（32 个，`x-codex-turn-metadata` 头的值）

身份链：`installation_id, session_id, thread_id, turn_id, window_id, window_number, context_window_id, parent_thread_id, parent_turn_id, root_turn_id, forked_from_thread_id, forked_from_ordinal_exclusive`
会话/来源：`agent_name, request_kind, compaction, thread_source, turn_trigger, subagent_kind`
工具/环境：`code_mode_tool_names, tool_namespaces_info, sandbox, sandbox_mode, workspaces`
时序/历史：`turn_started_at_unix_ms, history_ingest_requested, history_initialization`
审核/权限：`auto_review_enabled, node_repl_auto_review_required, node_repl_disabled`
杂项：`analytics_enabled, guardian_credits_requested, mcp_attribution`

相邻的 HTTP 头名常量（同一簇）：`x-codex-installation-id, x-codex-window-id, x-codex-turn-metadata, x-codex-parent-thread-id, x-openai-subagent`

### 2.2 服务端最可能拿来判「原生客户端 / 同账号同回合」的字段（推断，按证据强度）

1. **身份链自洽**：`installation_id + session_id + thread_id + turn_id + window_id + root_turn_id` 与对应 HTTP 头（`x-codex-installation-id` / `x-codex-window-id` / `session_id` / `thread_id`）及 body `client_metadata` 里的 `x-codex-installation-id`/`x-codex-window-id`/`turn_id` **必须一致**（网关 `applyCodexFingerprint*` 多处显式保证“头与体共享同一 ID 集合”）。
2. **原生客户端指纹**：`thread_source`（desktop/cli/vscode）、`request_kind`（turn/compaction）、`turn_trigger`、`agent_name`、`subagent_kind`、`sandbox`/`sandbox_mode` —— 这些是「官方客户端才会自洽填出的行为字段」，非官方 HTTP 打票请求最容易在这里露馅。
3. **时序**：`turn_started_at_unix_ms`（回合开始时间戳），可与 turn 内多请求的连续性交叉校验。
4. **`originator`/`User-Agent`/`version` 与 `thread_source` 一致**（notes-binary §4.3：originator 可被环境变量覆盖，但 version 低于 0.144.0 上游直接 404，issue #3901）。

→ **对打票的含义**：turn-metadata 是客户端「身份 + 会话上下文」的**自证 JSON**。打票探针只要能构造出「自洽的身份链 + 正确的 thread_source/request_kind/turn_trigger + 匹配的 UA/originator/version」，服务端才可能把它当“原生客户端首个非增量回合”来铸票。

---

## 3. 票据 blob 本身（Q3）—— 已证为 **Fernet 令牌**

### 3.1 结构（第一手，142 条样本）

| 属性 | 观测 |
|---|---|
| 条数 / 长度 | 142 条，**100% 恰好 780 字符** |
| 前缀 | `gAAAAA`（全部） |
| 字符集 | 64 种 base64url（`A-Za-z0-9-_`），无 `+ / =` |
| 解码后 | **585 字节** |
| 字节布局 | `1（版本 0x80）‖ 8（大端时间戳）‖ 16（IV）‖ 528（密文）‖ 32（HMAC）` |
| 密文块粒度 | 528 = **33 × 16**，`%16==0`（CBC 块对齐） |
| 时间戳 | 末样本 `1792683611` → **2026-10-22T15:40:11Z**（= 票据铸造时刻，与日志 ts 吻合） |

### 3.2 判定：Fernet（AES-128-CBC + HMAC-SHA256），**不是** AES-GCM / gzip

- 版本字节 `0x80` = Fernet 规范版本号（Python `cryptography.fernet` 的格式）。
- `16B IV + 32B HMAC + 块对齐密文` 与 Fernet 布局**逐字节吻合**；`1+8+16+528+32 == 585` 精确成立。
- 排除 AES-GCM：GCM 密文长 = 明文长（**非块对齐**），且 GCM 用 12B nonce + 16B tag，不是 16+32；实测密文 528 恰为 33 个 16B 块 → CBC。
- 排除 gzip：gzip 的 base64 头是 `H4sI`，不是 `gAAAAA`。
- 明文长度 ≈ **512–527 字节**（528 密文含 PKCS7 填充 1–16B）。

### 3.3 语义（推断，高置信）

- blob 是**服务端密钥加密 + HMAC 签名**的不透明能力令牌。**客户端无法伪造**（HMAC 密钥在服务端）；只能原样回带。
- 内嵌**铸造时间戳** → 服务端可做 TTL（社区口径 ~1h）与防重放。
- 因此它不是客户端能“自证 VIP”的标志位，而是服务端发的“本回合可粘性路由”凭据。

### 3.4 与「292/332 个人/Team」的历史关系（二手，标注可信度）

- **来源（二手）**：sub2api issue #8104 + linux.do/macos.do/locdd 社区 + sub4api 旧代码 `openAICodexTicketTargetLength`（notes-community.md C4/C5、notes-harvest-flow.md §3.3）。口径：个人 292/312（10→11 密文块）、Team 332/356（12→13 块），“20 字符差 = 多一个 16B 密文块”，2026-09-22 起变 ~780。
- **数学复核（第一手）**：
  - 780 = 585B = 57 + 528（33 块）→ 有效 Fernet，**无填充、块数 33（=3×11）**。
  - 332 = 249B = 57 + 192（12 块）→ **有效 Fernet**（192%16=0）。
  - 292 = 219B（无填充）→ 密文 162，**162%16≠0 → 不是无填充 base64url 下的合法 Fernet 长度**；若含 `==` 填充则为 217B=57+160（10 块）成立。
- **网关侧现状（第一手源码）**：`gateway.openai_codex_ticket.target_length` 默认 `292`，但当前 HEAD 的 `structurallyValid()` **只校验前缀 + 非空 + 元数据字段，根本不看长度**（`TargetLength` 仅进日志与状态展示）→ “292”是**过时默认值 + 展示字段**，不参与判定；≤1.1.2 旧构建才可能把 780 误杀。
- **可信度**：**低–中**。唯一铁证是“现在=780=33 块 Fernet”。292/332 只是社区启发式 + 旧配置残值；292 在无填充 base64url 下甚至不是一个自洽的 Fernet 长度。

---

## 4. 账号凭据面（Q4，只读脱敏，只看非秘密 claims）

`auth.json`（`tokens.id_token` 解 payload）：

| claim | 值 |
|---|---|
| `chatgpt_plan_type` | **`pro`**（Pro，非 Plus/Team/Business/Enterprise） |
| `chatgpt_account_id` | `22954eee-…94fe` |
| `organizations` | `[{id: org-nNW4…9VTe, is_default: true, role: owner, title: Personal}]` |
| `groups` | `[]` |
| `sub` | `google-oauth2|…`（google OAuth，`amr` 含 mfa/otp） |
| `iss` / `aud` | `https://auth.openai.com` / `app_EMoamEEZ73f0CkXaXp7hrann` |

响应侧（logs 全窗口）：`x-codex-plan-type: pro`、`x-codex-active-limit: premium`、`x-codex-credits-balance: 63000`、`has-credits: True`、`unlimited: False`，全程不变。

**结论**：本账号是 **Pro / 个人 org / owner**，能铸票（Pro 即“个人”档）。票在语义上绑定「account（Bearer + chatgpt-account-id）+ org（id_token 内 organizations）」，**不绑定 IP**（Fernet blob 密钥在服务端，与 IP 无关；IP/边缘粘性是 `__oailb`/`__cf_bm`/`__cflb` 那些 Cloudflare cookie 的职责，见 notes-client-runtime §4）。Pro 不是唯一能铸票的 plan——community 称 Plus/Team 也铸（长度不同），本机只有 pro 样本，**其它 plan 未验证**。

---

## 5. 结论：是不是“VIP/防降智凭证”？复用会被识别吗？（Q5）

### 5.1 票是什么（钉死）

**per-turn sticky routing token**（官方契约 openai/codex PR #9332 + 本机观测）：

1. 下发条件 = **一个 turn 的第一个非增量 /responses 请求**（139/141 turn 首请求带票，203 条同 turn 续请求 0 票）→ 每 turn 一张。
2. 客户端**只在下一次 WS 握手回带**，HTTP/SSE 路径无回带点（notes-binary §2.4，二手但 RVA 证据充分）。
3. 跨 turn 必须清空（PR #9332 注释 “must not be sent between different turns”）。
4. blob 是 Fernet 加密 + 服务端签名 → 客户端不能伪造，只能回带；内嵌时间戳 → TTL。

→ **它不是“防降智/VIP 凭证”**。“降智”在本机观测里对应的是 `x-codex-safety-buffering-enabled: true` + `faster-model: gpt-6-luna` 那条**独立的路由/安全缓冲信号**，与 turn-state 是两套机制。

### 5.2 三种复用的结局（证据 + 明确未知）

| 复用 | 判定 | 证据 |
|---|---|---|
| **跨账号** | **会被识别/矛盾** | 网关 `openai_codex_turn_state.go` 明写：turn-state 是“出站身份（含收敛后 installation/session/thread）下铸造”，**跨账号回放是“真实 Codex 永远不会产生的矛盾信号”**，`guardOpenAICodexTurnStateEcho` 会剥离已知异账号回带值。blob 加密+HMAC 亦以账号/安装上下文为密钥材料（推断）。 |
| **跨回合** | **会被忽略/拒绝** | per-turn 契约 + 客户端逐 turn 清空 + blob 内时间戳 TTL。把一张票复用进后续 turn 的 HTTP 请求，官方客户端自己都不这么做。 |
| **跨 IP** | **票本身不绑 IP，但边缘层绑** | Fernet 密钥在服务端、与 IP 无关（推断）；但 `__oailb/__cflb/__cf_bm` 是 Cloudflare 边缘粘性 cookie（IP/节点绑定），且 `__oailb` 只在 `GET /models` 下发、`/responses` 上只有 `__cf_bm`。跨 IP 复用 turn-state **在同账号同回合内**可能不因 IP 被拒，但**跨账号/跨回合/超 TTL** 会被拒。 |

### 5.3 打票失效的机制性解释（从角 2 出发）

“抓一张 `x-codex-turn-state` + 全站共享 `__oailb` 注入、号称 VIP 防降智”之所以失效，是**三重语义错配**，不是单一长度 bug：

1. **获取错配**：票只在“turn 首个非增量请求 + 被判定为原生客户端”时才铸；探针的合成身份（收敛 installation + 新 session/turn + 可能缺 `x-oai-attestation`/WS beta 头）不满足铸票准入。
2. **使用错配**：票只在 WS 握手回带、且 per-turn per-account；注入 HTTP 业务请求 + 全站共享，客户端/上游都不认。
3. **形态误判**：旧校验按 292 判长度 → 现在 780（33 块 Fernet）；`__oailb` 只在 /models 下发，/responses 上抓 Set-Cookie 只会拿到 `__cf_bm`（cookie-only 假票）。

---

## 6. 未知项 / 局限

1. 服务端铸票的**准入函数**（是否必须 attestation、是否校验 installation/thread_source、是否受 org/workspace 限制）——属服务端策略，本机不可观测。
2. Fernet 密钥是否**按账号/org/installation 分段**——无法从密文结构判定（只能证“加密+签名”，不能证密钥粒度）。
3. 292/332 的历史真实性（9/22 前个人/Team 长度）——客户端日志只留 33h，无法证伪；292 在无填充 base64url 下不自洽，可信度低。
4. `window_id`/`root_turn_id` 的确切生成式——只从 turn-metadata 字段名 + 网关收敛代码推断，未在客户端响应回显里直接观测到值。
5. 其它 plan（Plus/Team/Business）是否同样铸票、长度几何——本机只有 pro 样本。

---

## 7. 从角 2 出发的方案建议（1 最推荐 + 2 次选）

### 最推荐：放弃“全站共享一张票”，改“同账号同回合 WS 续传票据透传”（可行性高 / 风险低 / 成本中）
- 做法：把 turn-state 定位为**会话内粘性路由令牌**——在网关侧按 `(API-key + 客户端原始 session-id)` 记录上游铸造的票（`openai_codex_turn_state.go` 已有这套溯源表），**只对同一下游会话的后续请求原样回带**，且**只在 WS 握手路径注入**；不做跨账号/跨会话共享。
- 依据：这正是官方客户端用法（PR #9332 per-turn sticky），也是网关源码里已经实现的语义（`relayOpenAICodexTurnState` / `guardOpenAICodexTurnStateEcho`）。
- 可行性：高——网关已具备溯源与剥离机制，只需把“注入点”从 HTTP 业务请求改到 WS 握手，并去掉 292 长度假设（当前 HEAD 已不校验）。
- 风险：低（不伪造、不跨账号）；成本：中（要接通 WS 转发路径的票据注入）。

### 次选 A：把“身份链自洽”做成探针准入的前置校验（可行性中 / 风险中 / 成本低）
- 做法：打票探针的 `x-codex-turn-metadata` 必须**逐字段自洽**——installation_id 用真实 `.codex/installation_id`（而非收敛值）、session=thread=同一 UUIDv7、每 probe 新 turn_id、补 `thread_source/request_kind/turn_trigger` 与 UA/originator/version 配套；命中“首请求才铸票”的时序。
- 依据：§1.2 收敛模式历史上曾致额度缩水（#5553→#5610），说明上游**对设备/会话指纹敏感**。
- 风险：中（更真实的指纹 = 更接近“模拟官方客户端”，有被识别为绕过的封号面）；成本：低（改探针请求构造）。

### 次选 B：改盯“降智”本身的信号（`x-codex-safety-buffering-*` / `service_tier`），不再盯 turn-state（可行性中 / 风险中 / 成本低）
- 做法：本机观测里“降智”直接对应 `x-codex-safety-buffering-enabled: true + faster-model: gpt-6-luna`，且 Fast 档是 `service_tier: priority` 且**只在 WS 上生效**（HTTP 直打 0.97× 无效，community C6）。把目标从“铸票”改为“识别/避免 safety-buffering 与 tier 降级”。
- 风险：中（同样是服务端策略，可能只是把“打票失败”换成“降智检测失败”）；成本：低（复用现有日志/头解析）。

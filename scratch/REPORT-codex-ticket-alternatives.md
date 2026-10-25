# Codex「打票」失效诊断 与 替代方案

日期：2026-10-23 JST ｜ 目标仓库：D:\projects\sub4api ｜ 证据目录：`scratch/notes-*.md`

---

## 0. 结论（TL;DR）

1. **上游没有停止下发 `x-codex-turn-state`**。本机官方客户端在 2026-10-20 21:10 → 10-23 01:52（33h）共收到 **142 条**，最近一条 **10-23 00:40:34 JST**，且 **100% 是 780 字符**（前缀 `gAAAAA`，base64url，解码 585B，首字节 `0x80`，**不是 gzip**）。窗口内从未出现 292/308/332。
2. **票的语义不是「VIP 凭证」，而是「per-turn 粘性路由令牌」**。官方契约（openai/codex PR #9332）：同回合回带、跨回合必须清空、与铸造它的账号/凭据绑定；客户端唯一回带点只在 **WS 握手**，HTTP/SSE 路径没有回带点。所以「打一张票全站共享、注入到所有 HTTP 业务请求」从协议上就不成立，修好也只能拿到「同账号同回合的路由连续性」，拿不到「防降智」。
3. **真正导致「打不出来」的，最可能是探针请求形状/身份不被认可**（200 但无票，或 401/403），其次是「假票」判定（把 `__cf_bm` 当票）。票长 292 的假设只在 ≤1.1.2 的旧构建上会误杀 780 票。
4. **降智是服务端路由行为，有公开可读信号**：`x-codex-safety-buffering-enabled: true` + `x-codex-safety-buffering-faster-model: gpt-6-luna`，本机 **209/347** 条 `/responses` 响应都有（全部 `gpt-6-astra` / `gpt-6.1-sol` 的 HTTP 响应），`codex-auto-review` 从不出现。权威判据是「请求模型 == 流内 served model」，不是票长。

**替代方案优先级**：
- 立即可做：**A. 被动采集 turn-state（按账号+回合，只回传不共享）** + **B. 修掉假票/长度假设**
- 想恢复「能打票」：**C. 把探针改成当前客户端的形状**（GET /models 拿 cookie + 身份同源 + 双通道读票）
- 想要「真 VIP/速度」：**D. Fast/priority（1.1.13 已实现）+ 实测**；**E. 降智判据换成 safety-buffering/served-model + 账号运维**
- 旁路兜底：**F. BPS/Excel**（1.1.5 已内置）
- **不建议**：继续扩「全站共享一张票 + 注入 HTTP」——协议错、风控风险高、社区已有封号报告。

---

## 1. 证据：现在到底发生了什么

### 1.1 官方客户端（本机，直连 chatgpt.com）

| 事实 | 数据 |
|---|---|
| `/responses` 请求 | 347 条 HTTP 完成（全 200）+ 108 条 WS 成功连接 |
| `x-codex-turn-state` | 142 条，**全部 780 字符**，全部在 **HTTP 响应头** |
| 下发条件 | **每个 turn 的首个非增量请求**：首请求 139/141 带票，续请求 **0/203** |
| `__oailb` | 只在 `GET /backend-api/codex/models` 下发（128 次，值长 317，与 `__cflb` 成对）；**`/responses` 0 次** |
| `/responses` 的 Set-Cookie | 只有 Cloudflare `__cf_bm`（15/347） |
| 401/403/429 | **0 条**；19 条失败全是网络层 `error sending request` |
| 账号态 | `plan-type: pro`、`active-limit: premium`、credits 63000 全程不变 |
| 模型目录 | etag 与 10/21 完全一致，仍 9 个模型 |
| 降智信号 | `x-codex-safety-buffering-enabled: true` / `-faster-model: gpt-6-luna`，209/347（全部 astra/6.1sol） |
| 客户端版本 | Desktop 26.1016.61520；库 0.163.0；10/22 起 `service_tier=priority` |

> 来源：`scratch/notes-client-runtime.md`（含可复现 SQL）

### 1.2 codex.exe 静态逆向（SHA256 E757246B…，336MB，0.163.x）

- `x-codex-turn-state` 读取点只有两处：`codex-api/src/sse/responses.rs` 的响应头 map；`endpoint/responses_websocket/connector.rs` 的 **WS 握手响应头**。
- **写回（回带）只有一处**：函数 `0x78719e0`，把 `x-codex-beta-features` + `x-codex-turn-state` 一起 insert；10 个调用者全是 **WS turn 构造器**。**纯 HTTP/SSE 请求路径没有回带点。**
- WS 握手额外头：`OpenAI-Beta: responses_websockets=2026-02-06`、`x-oai-attestation`、`x-codex-routing-hint`、`x-codex-beta-features`、`x-openai-internal-codex-responses-lite`、`traceparent`。
- `x-codex-turn-metadata` 是 28 字段的身份/会话自证 JSON（installation/session/thread/turn/window/request_kind/thread_source/turn_trigger/sandbox…），服务端很可能用它做「是否原生客户端」判定。
- `__oailb` 在二进制里 **0 次出现** → 客户端不读不写，只是 reqwest cookie jar 被动存取；它不该被当票。
- 降级相关串：`fast_mode` / `ultrafast_mode` / `step_model_switching` / `x-codex-safety-buffering-*` / `Luna classification`。
- 证据边界：本机 **REA 已绑定目标（SHA 校验通过），但没有原生 provider**（Ghidra 未装、无 JDK、Hopper 仅 macOS、IDA/Kuna 未配置），`binary_overview`/`search_strings` 返回 `provider_unavailable`。上述结论来自全 `.text` RIP 相对 xref 精确扫描 + 自写 x86 解码器的静态分析，已标注「观察/推断/未知」。要升级到伪代码级需装 Ghidra 12.1.x + JDK 并设 `GHIDRA_INSTALL_DIR`。

> 来源：`scratch/notes-binary.md`

### 1.3 sub4api 打票探针（当前代码）

- `POST https://chatgpt.com/backend-api/codex/responses`，HTTP/1.1 + `Connection: close`，只走「打票代理池」，不走账号自己的住宅代理。
- header：`Bearer` + `Accept: text/event-stream` + `OpenAI-Beta: responses=experimental` + `session_id/thread-id/x-client-request-id`（新 UUIDv7）+ `x-codex-installation-id` + `x-codex-window-id` + `x-codex-turn-metadata` + `chatgpt-account-id`；UA/originator/version 默认 `codex-tui/0.146.0`。
- body：Codex Desktop 初始上下文（7 条记录、`session_meta.originator="Codex Desktop"`、通用 instructions、**无 tools**、**无历史**）+ ModelTrace 挑战。
- **不带任何 cookie、不带旧票、不带 `x-oai-attestation`、不是 WS。**
- 只从 **HTTP 响应头** 读票（不读 SSE `response.metadata`，不读 WS 握手）。
- 有效票判定：200 → 数字数 ≥ max(80, 55%·N) → 指纹模型 == 请求模型 → **turn-state 或「任意一个 Set-Cookie」非空** 即落库。
- 失败原因码：`http_status`(401/403/404/429) / `missing_credentials` / `insufficient_numbers` / `fingerprint_mismatch` / `timeout` / `proxy_unavailable`。

> 来源：`scratch/notes-harvest-flow.md`（含请求规格、失败面、排查 SQL）

### 1.4 与官方客户端的关键差异（= 最可能的失败原因）

| 维度 | 官方客户端 | sub4api 探针 |
|---|---|---|
| 传输 | HTTP/SSE **和** WS 都用；WS 是主路径 | 只有 HTTP/1.1 |
| 票长 | **780** | 配置/展示仍假设 292/332 |
| Cookie | `__oailb`+`__cflb` 来自 `GET /models`，jar 内自动回带 | 不发 cookie、不调 /models |
| 身份 | body 的 `session_meta.originator` 与 header `originator/UA` 同源 | **Desktop body + `codex-tui/0.146.0` header**，且版本落后 0.163.0 |
| attestation | WS 带 `x-oai-attestation` | 没有 |
| 读票 | 响应头 + SSE metadata + WS 握手 | 只有响应头 |
| 票用途 | 同账号同回合续传 | **全站共享、注入任意账号的 HTTP 请求** |
| 假票防护 | — | 会把 `__cf_bm` 当票 → cookie-only 假票 |

---

## 2. 替代方案（按性价比排序）

### A. 被动采集（推荐立刻做，零额外请求、协议正确）
现在网关在业务请求上**已经能看到**上游返回的 turn-state：
- HTTP 响应头：`observeCodexTicketHeaders`（只用来判失效）
- WS 握手：`openai_ws_forwarder_v2.go:315` 的 `handshakeTurnState`

做法：
1. 在 `codex_ticket_observation.go` 增加「存储」分支：按 **(account_id, model, turn/thread)** 保存最新 turn-state + 相关 cookie；**不再全站共享**。
2. 注入改为「同账号 + 同模型 + 同回合」回带；跨账号一律剥离（保留现有 `guardOpenAICodexTurnStateEcho`）。
3. 业务侧本来就有票可用，**不需要打票探针**，也就不存在 401/403/空票。

代价：不能跨账号「借用」票；但这本来就是无效且高风险的。收益：0 额外上游请求、0 新失败面。

### B. 修掉假票与长度假设（1 小时级）
- `extractOpenAICodexResponseCookies`（`openai_codex_ticket.go:466`）：只接受 `__oailb` / `__cflb`，**明确排除 `__cf_bm`/`_cfuvid` 等 CF cookie**；不允许「只有 cookie 没有 turn-state」落库（把 `missing_credentials` 判死）。
- 删除/放宽 292 长度假设：`target_length` 只做展示（当前 HEAD 已不校验，但 ≤1.1.2 构建会误杀 780 票）；面板显示「实际长度」。
- `gAAAAA` 不要当 gzip 解（首字节 `0x80`）。

### C. 让探针变成「当前客户端」（想恢复打票才做）
1. **先 `GET https://chatgpt.com/backend-api/codex/models?client_version=<真实版本>`**（同一代理、同一 cookie jar）→ 拿 `__oailb` + `__cflb`，并校准模型目录。
2. 探针与业务使用**同一套身份**：originator/UA/version 与 body 的 `session_meta.originator` / `turn-metadata.thread_source` / `request_kind` / `turn_trigger` 保持一致；版本用真实 0.163.0（或后台同步值），Astra 最低 0.153.4 的下限已过时。
3. 请求体补上真实客户端的形态：`tools`（至少 `shell`/`apply_patch` 一类）、`reasoning`、`include`、`client_metadata` 全字段；`thread_id` 不要复用 `session_id`。
4. **双通道读票**：HTTP 响应头 + SSE `response.metadata` 的 `headers` 字段（`fireOpenAICodexTicketProbe` 的扫描循环 `openai_codex_ticket.go:428-456` 目前只认 `output_text.delta` / `completed`）。
5. 想要 WS 通道：复用现有 `openai_ws_forwarder_v2.go` 的握手，带 `OpenAI-Beta: responses_websockets=2026-02-06` + `x-oai-attestation`（后者能否拿到取决于客户端，属未知）。
6. 投产前用 `backend/cmd/codex-ticket-hypothesis` 做 A/B（它目前只走 HTTP，需要扩展成 WS/身份变体）。

### D. Fast/priority 作为「速度 VIP」（1.1.13 已有）
- `openai_fast_ws_bridge.go` 已把 HTTP 入口的 priority 请求桥到 upstream WSv2；后台开关「Fast WS 桥接」。
- 但：客户端证据显示 **10/22 起已在用 `service_tier=priority`**，同期仍有 safety-buffering → **Fast ≠ 防降智**。定位为「速度/体验」而非「解除降智」。
- 必须 A/B：同账号同模型，HTTP/SSE vs upstream-WS，比 TPS + `x-codex-primary-used-percent` 消耗；**不要用响应回显 `default` 判成败**。

### E. 降智判据与账号运维（长期主方向）
- 读 `x-codex-safety-buffering-enabled` / `-faster-model`，以及流内 `response.created.response.model`，与请求模型比对；命中即标记「疑似降智」。
- 可选：**剥离**这两个头（避免客户端 UI 引导用户切弱模型），注意 WS metadata 与 `/responses/compact` 是漏点。
- 账号冷却/轮换：把「降智账号」从门控模型分组里摘掉，而不是给它打票。
- 把现有「降智检测」从 ModelTrace 指纹改为 served-model 判据（`README` 里的检测链路已有骨架）。

### F. BPS / Excel 旁路（兜底，中高风险）
- 社区口径：`bps.openai.com/basispoints/api/responses` + `x-basispoints-auth-mode: chatgpt` 不走普通降智路由。
- sub4api 1.1.5 已内置 BPS 平台；限制：**无 WS、无 max effort、约 600k 强制压缩、Token 手动续期、工具需转换**。
- 默认关闭 + 明确风险提示。

### G. 时区/环境上下文（零成本，保留）
- 已实现每账号 `Asia/Singapore` 时区替换；保持开启即可，别当主方案。

---

## 3. 远端服务器排查（定位死在哪一关）

```sql
-- 1) 失败原因分布（近 3 天）
SELECT outcome, reason_code, http_status, count(*), max(occurred_at)
FROM codex_ticket_attempts
WHERE occurred_at > now() - interval '3 days'
GROUP BY 1,2,3 ORDER BY 4 DESC;

-- 2) 假票检测（cookie-only）
SELECT account_id, model, occurred_at, http_status, ticket_length
FROM codex_ticket_attempts
WHERE outcome='success'
  AND coalesce(turn_state_present,false)=false
  AND coalesce(cookie_present,false)=true
ORDER BY occurred_at DESC LIMIT 50;

-- 3) 票长分布（确认是 780 还是 292）
SELECT ticket_length, count(*), min(occurred_at), max(occurred_at)
FROM codex_ticket_attempts
WHERE outcome='success' GROUP BY 1 ORDER BY 2 DESC;
```

```bash
# 日志锚点
grep -E 'openai_codex_ticket (probe miss|harvested|probe unavailable)' <日志>
grep -E 'proxy_unavailable|template_invalid|identity_resolution_failed' <日志>
```

判读：
- 全是 `http_status` 401/403 → 身份/代理/IP 被拒，走方案 C + 换打票出口（或改用账号自己的住宅代理）。
- 大量 `missing_credentials`（200 无票无 cookie）→ 请求形状不被认可，方案 C；同时先做方案 B 排除假票。
- 大量 `fingerprint_mismatch` → ModelTrace 指纹库过期/模型表漂移，先更新 bank，别把它当降智。
- `proxy_unavailable` → 代理池空/全过期（**日志是静默的，最容易被误判成上游拒**）。

---

## 4. 风险与边界

- 「全站共享一张票」违反官方 per-turn/账号绑定契约；社区有封号报告，且有「被 OpenAI 点名」的传闻（未证实）。建议**默认关闭**，不要作为卖点。
- 本机证据只覆盖 33h（日志按容量裁剪），不能证伪 9/22 的「292→780」变化；但可确认「现在=780」。
- WS 握手/SSE body 在客户端不落日志，故「WS 上是否发/回带票」仍为**未知**，需抓包（或装 Ghidra 后反编译 11 个已定位函数）。

---

## 5. 证据文件

- `scratch/notes-client-runtime.md`：本机客户端 33h 运行证据（142 张 780 票、`__oailb` 只在 /models、safety-buffering 209 条、SQL）
- `scratch/notes-binary.md`：codex.exe 静态逆向（turn-state 读写点、WS 头、turn-metadata 字段、降级/错误码、复现脚本）
- `scratch/notes-harvest-flow.md`：打票探针完整规格、失败面、假票路径、排查 SQL
- `scratch/notes-community.md`：官方文档/PR/issue + 社区方案（含 780 变化、per-turn 契约、Fast 传输结论反转、BPS）
- `scratch/notes-fast-path.md`、`scratch/notes-runtime.md`：前序 Fast/service_tier 与 10/21 运行时证据

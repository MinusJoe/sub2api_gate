# Codex 打票（turn-state ticket）请求契约与失败面

分析对象：`D:\projects\sub4api`，只读 tracked 文件。HEAD `c2b46b4f9`（`git describe` = `v1.1.11-13-gc2b46b4f9`，`backend/cmd/server/VERSION` = `1.1.13`）。
v1.1.11 之后打票相关代码**没有再改过**（`git log v1.1.11..HEAD -- "*ticket*"` 为空）；因此下文描述的 1.1.13 行为 == 1.1.11 行为。

> 本文不含任何 access token / turn-state / cookie 明文。所有值用 `<...>` 代替。

---

## 0. TL;DR（先看这里）

1. 打票 = 用账号 OAuth token，经**住宅代理池**向 `POST https://chatgpt.com/backend-api/codex/responses` 发**一次**带 ModelTrace 挑战的 Codex 回合；从**响应头**抓 `x-codex-turn-state`，从 `Set-Cookie` 抓 **全部** cookie。响应体必须能被 ModelTrace 指纹识别为「请求的那个模型」，否则不发票。
2. 当前「有效票」只判 4 件事：**HTTP 200** + **指纹命中** + **turn-state 或任一 Set-Cookie 非空** + 结构字段齐全。**不看长度、不看 `gAAAAA` 前缀、不接受 429、TTL 无实际作用**：长度/前缀/429 规则从 **1.1.3-rc1** 起就不再被调用，1.1.11 只是把这段死代码删掉（见 §3.3/§10）。
3. 「292 / 332」来源：v1.1.10 代码里残留（但自 1.1.3-rc1 起已无人调用）的 `openAICodexTicketTargetLength`——`plan_type` 为 `team`/`selfservebusinessprolite` 期望 332，其余期望配置值（默认 292）。**`308` 没有任何与票长度相关的代码/常量/规则（票相关文件与历史都查不到）**，只在 `codex_ticket_hypothesis_test.go:112` 作为随手写的挑战数示例出现过。
4. 探针最可能被上游识破/拒绝的点（按可疑度）：
   - **身份头与请求体自相矛盾**：header `originator: codex-tui` + UA `codex-tui/…`，但请求体初始上下文是 `session_meta.originator = "Codex Desktop"` 的桌面端录制模板，且**完全没有 `tools`**、`instructions` 是一句通用英文（§9）。
   - **版本落后**：默认声明 `0.146.0`，真实客户端库是 `0.163.0`、桌面壳 `26.1016.51333`；只靠 6h 自动同步追赶（§7）。Astra/gpt-6 系列另有 0.153.4 的下限抬升逻辑。
   - **传输面不同**：探针强制 HTTP/1.1 + `Connection: close` + 每次新建 TLS（代理轮换），真实客户端主要是 HTTP/2 / WebSocket 长连接（§2.1/§9）。
   - **代理/出口与账号地理不一致**：每次打票都可能换住宅 IP，且时区固定按账号 `Asia/Singapore` 渲染（§2.4）。
   - **票的语义**：探针是「无历史、无工具、无 ticket 回带」的裸回合；如果上游改成只在真实回合（或 WS、或带 tools 的回合）铸造 turn-state，这个形状就永远拿不到票（症状 = `missing_credentials`）。
5. 最危险的假成功：`Set-Cookie` 里**任意** cookie（例如 Cloudflare 的 `__cf_bm`）都会让代码判 success 并落一张「cookie-only 票」——`applyOpenAICodexTicket` 会注入 Cookie、并**删掉** `x-codex-turn-state`。这类票很可能就是「看着打成功了，但 Codex 仍降智/空票」的来源（§3.2、§5）。

---

## 1. 打票链路（谁在什么时候发这个请求）

| 入口 | 代码 | 说明 |
| --- | --- | --- |
| 自动打票 | `openai_codex_ticket.go:502-670` | `StartOpenAICodexTicketHarvester` 启动常驻 loop；`openAICodexTicketHarvestLoop` 每 `harvest_probe_interval_seconds`（默认 6s，可被更早的重试点提前唤醒）扫一轮；`refreshOpenAICodexTickets` 遍历 `platform=openai` 账号 × 运行时模型表；每个 (账号,模型) 并发一次 `probeOnceOpenAICodexTicket` |
| 手动打票 | `openai_codex_ticket_harvest.go:291-324` | `ManualCodexTicketHarvest` → 同一条 `runCodexTicketAttempt`（trigger=`manual`） |
| 降智检测（diagnostic） | `codex_probe_template.go:291-297` + `handler/admin/codex_ticket_diagnostic_handler.go:181-192` | 复用**同一个模板 body**，但走正常网关 ingress（`WithCodexTicketDiagnostic`），不是本文的打票探针；不要混为一谈 |
| 离线假设工具 | `cmd/codex-ticket-hypothesis/*` + `codex_ticket_hypothesis.go` | 独立 CLI，3 个挑战串行、把 UA 全局钉在 `codexHypothesisClientVersion = 0.156.1`（`codex_ticket_hypothesis.go:24,119`）；**不在服务器热路径上** |

探针前置门控（`refreshOpenAICodexTickets` → `probeOnceOpenAICodexTicket` → `runCodexTicketAttempt`）：

1. 全局开关 `openai_codex_ticket_enabled`（DB 设置优先，回退 YAML `gateway.openai_codex_ticket.enabled`，默认 **false**）；
2. 账号是 `IsOpenAIOAuthLike() && !IsShadow()`（OAuth/SetupToken，凭据影子账号不参与，`openai_codex_ticket.go:781-783`）；
3. 账号 `status=active`，`extra.codex_ticket_harvest_enabled != false`（默认参与）；
4. 模型参与：`extra.codex_ticket_harvest_models[model] != false`（默认参与，`openai_codex_ticket_harvest.go:36-51`）；
5. 模型在**运行时模型表**里，且账号/模型未被限流（`openAICodexTicketQuota` / `openAICodexTicketHarvestLimited`，`openai_codex_ticket_rules.go:15-43`）；
6. 到了重试点：成功票按 `harvest_refresh_seconds`（默认 1800s）刷新；失败重试 `harvest_retry_min..max`（默认 10–30s，带 jitter，`openai_codex_ticket_harvest.go:93-101,188-193`）；
7. 进程内 + PG advisory lock 双重去重，同一 (账号,模型) 同时只有一发（`openai_codex_ticket_harvest.go:145-159`、`repository/codex_ticket_attempt_repo.go:225-249`）。

---

## 2. 打票探针的精确请求（`fireOpenAICodexTicketProbe`，`openai_codex_ticket.go:387-464`）

### 2.1 目标 / 方法 / 传输

| 项 | 值 | 依据 |
| --- | --- | --- |
| URL | `https://chatgpt.com/backend-api/codex/responses` | `openai_gateway_service.go:31` `chatgptCodexURL` |
| Host 头 | 强制 `chatgpt.com`（无端口） | `openai_codex_ticket.go:400` `req.Host = "chatgpt.com"` |
| Method | `POST` | `:394` |
| 传输 profile | `HTTPUpstreamProfileOpenAIHarvest` | `:398` |
| 协议 | **HTTP/1.1，禁用 keep-alive，每次新建连接**（`ForceAttemptHTTP2=false`、`DisableKeepAlives=true`、idle conn=0） | `repository/http_upstream.go:1070-1072,1405-1411`；目的注释：让代理每次重新 CONNECT 以轮换出口 IP |
| `req.Close` | `true` → 线上带 `Connection: close` | `openai_codex_ticket.go:399` |
| 压缩 | Go transport 自动 `Accept-Encoding: gzip`（未显式关闭） | 标准库默认 |
| 重定向 | 不特殊处理（该 profile 只改协议/连接池） | — |
| TLS | Go 默认 crypto/tls（未套 uTLS 指纹） | 请求未用 `DoWithTLS` |

### 2.2 请求头（最终发出的集合）

> 注意：Go `net/http` 会对 header key 做 MIME canonicalize 后再写线上，所以实际字节大小写是 `User-Agent` / `Session_id` / `Openai-Beta` / `X-codex-*` 这种，不是下面表里的手写形态；HTTP 语义上大小写不敏感。

| Header | 值 | 设置点 |
| --- | --- | --- |
| `Host` | `chatgpt.com` | `:400` |
| `Authorization` | `Bearer <access_token>` | `:401`；token 来自 `GetAccessToken`（OAuth 走 TokenProvider，可能触发刷新；`openai_gateway_service.go:1222-1269`） |
| `Accept` | `text/event-stream` | `:402` |
| `Content-Type` | `application/json` | `:403` |
| `OpenAI-Beta` | `responses=experimental` | `:404`，且 `ensureCodexIdentityHeaders` 会再写一次（`openai_codex_identity.go:190`） |
| `session_id` | `<session UUIDv7>` | 回放头，`codex_replay_request.go:61-63` |
| `session-id` | 同上 | 同上 |
| `thread-id` | 同上（session 与 thread 同值） | 同上 |
| `x-client-request-id` | 同上 | 同上 |
| `x-codex-installation-id` | `<stable UUIDv4>`，按 ChatGPT 账号凭据命名空间派生（同账号所有本地行共享） | `openai_codex_ticket.go:411`、`codex_probe_template.go:266-275`、`openai_codex_account_identity.go:93-107` |
| `x-codex-window-id` | `<UUIDv7>` | 同上 |
| `x-codex-turn-metadata` | JSON 字符串，7 个键：`installation_id`、`session_id`、`thread_id`、`turn_id`、`window_id`、`turn_started_at_unix_ms`、`request_kind:"turn"` | `codex_replay_request.go:32-37,66` |
| `chatgpt-account-id` | 账号凭据里的 ChatGPT account id | `resolveAndSetOpenAIChatGPTAccountHeaders`，`openai_chatgpt_headers.go:8-20` |
| `x-openai-fedramp` | 仅 FedRAMP 账号 `true`；否则显式删除 | 同上 |
| `User-Agent` | 规范身份重建：默认 `codex-tui/0.146.0 (Ubuntu 22.4.0; x86_64) xterm-256color`；若模型含 `gpt-6`/`astra` 且版本 <0.153.4 则 `codex-tui/0.153.4 (Ubuntu 22.4.0; x86_64) xterm-256color` | `applyOpenAICodexTicketHarvestIdentity`，`:486-500`；常量 `openai_gateway_service.go:40-44,64` |
| `originator` | 默认 `codex-tui`；若面板 UA 配成其它官方客户端，则按该 UA 首段配对（如 `Codex Desktop` / `codex_chatgpt_desktop`） | 同上 |
| `version` | 当前生效版本（默认 `0.146.0`；Astra 时不低于 `0.153.4`） | 同上 |
| （缺失） | 无 `x-codex-turn-state`、无 `Cookie`、无 `conversation_id`、无 `x-codex-beta-features`、无 `x-codex-routing-hint`、无 `Accept-Language`、无 `OpenAI-Organization` | 代码事实；测试 `openai_codex_ticket_lifecycle_test.go:98-113` 断言探针不携带旧票 |

探针**故意不回带**任何已有票据/指纹（`TestCodexTicketProbeDoesNotCarryExistingTicket`），它要的是「干净回合」抽出新票。

### 2.3 请求体（`buildCodexReplayRequest`，`codex_replay_request.go:32-68`）

模板：`codex_probe_template.jsonl`（7 行，嵌入二进制；渲染后 JSON 化）。解析规则极严：必须 7 条、固定 type 顺序 `session_meta, response_item×4, turn_context, response_item`；角色/分片数量、wrapper 标签、environment XML 层级都必须保留；`{{MODELTRACE_PROMPT}}` 恰好出现 1 次且必须是最后一条 user 消息全部内容（`codex_probe_template.go:127-237`）。

渲染后 body：

```json
{
  "model": "<账号实际出站模型，如 gpt-6-astra>",
  "store": false,
  "stream": true,
  "instructions": "<session_meta.base_instructions.text>",
  "input": [
    {"role":"developer","content":[5×{"type":"input_text","text":"…"}]},
    {"role":"developer","content":[1×{"type":"input_text","text":"<multi_agent_role>…</multi_agent_role>"}]},
    {"role":"developer","content":[1×{"type":"input_text","text":"<multi_agent_mode>…</multi_agent_mode>"}]},
    {"role":"user","content":[1×{"type":"input_text","text":"<environment_context>…</environment_context>"}]},
    {"role":"user","content":[1×{"type":"input_text","text":"<MODELTRACE 挑战全文>"}]}
  ],
  "prompt_cache_key": "<session UUIDv7>",
  "client_metadata": {
    "session_id": "<session UUIDv7>",
    "thread_id": "<同 session>",
    "turn_id": "<turn UUIDv7>",
    "x-codex-installation-id": "<stable UUIDv4>",
    "x-codex-window-id": "<UUIDv7>",
    "x-codex-turn-metadata": "<与 header 完全相同的 JSON 字符串>"
  }
}
```

要点：

- **没有 `tools` / `tool_choice` / `parallel_tool_calls` / `reasoning` / `include` / `max_output_tokens` / `temperature`**，也**没有** `system` 参数（探针固定传 `system=""`，`codex_probe_template.go:284`）。
- `instructions` 是模板里的通用英文一句话：`You are Codex, an AI assistant. Complete the current task directly and follow the requested output format.`（`codex_probe_template.jsonl:1`）。
- `input` 的 5 条来自模板第 2–5、7 行：3 条 developer（分别 5/1/1 个 `input_text`）+ 2 条 user（环境 XML、挑战）。
- 环境 XML 里固定 `cwd=/workspace`、`shell=zsh`、3 个 workspace root、18 条 permission entry；`{{CURRENT_DATE}}`/`{{TIMEZONE}}` 用账号时区渲染（`codex_probe_template.go:82-122,278-287`）。
- 身份：installation id 稳定（按 `chatgpt:<account>:user:<user>` 派生 UUIDv4），session/thread/window/turn 每次新 UUIDv7，`turn_started_at_unix_ms` = 请求时间（`codex_replay_request.go:16-28`）。
- 模板非法（<7 条/占位符错/角色错）→ 不请求上游，直接 `template_invalid`（`openai_codex_ticket_harvest.go:196-203`）。

**ModelTrace 挑战**（`modeltrace_challenge.go:54-79`）：

- 期望数量 `N` 在 **[292, 332]** 均匀随机（同一次打票固定一个 N，记在 `challenge_expected_count`）。
- 文本结构（4 组随机句 + 2 段固定中文）：

```text
{句A}。{句B} {N} 个 1 到 355（含端点）的整数。
每个位置都要单独选择；不要从 1 开始计数，不要连续递增或递减，也不要采用等差、循环、重复区块或其他规则化模式。
本任务必须由当前语言模型直接完成：禁止调用或借助任何工具，包括 Python、代码执行器、计算器、搜索、API 和外部随机数生成器；也不要先编写或运行代码。
{句C}{句D}直接从第一个取值开始输出，不要在序列前重复数量、范围或任务说明。
```

- A/B/C/D 各自从 5/5/5/4 条候选句里抽（`modeltrace_challenge.go:18-46`）。
- 目的：让当前模型凭「第一反应」吐一串数字，ModelTrace 用分布+顺序指纹判断这串数到底是哪个模型生成的（用于识别上游偷偷换模型/降智）。

### 2.4 代理 / 超时 / 重试 / 并发

| 项 | 行为 | 依据 |
| --- | --- | --- |
| 代理 | **只从「Codex 打票代理池」取**：代理表 `ListActive()` 中未过期、且池 mode=`all` 或命中自定义 `proxy_ids`；按 `fnv(account\0model)` 播种轮转，每次尝试换一个 | `openai_codex_ticket_harvest.go:62-85`、`codex_ticket_pool.go:80-107` |
| 无可用代理 | **不直连**，直接 `ErrCodexTicketNoProxy` → outcome=error / reason=`proxy_unavailable`，不发请求 | `openai_codex_ticket_harvest.go:70-72,204-208` |
| 单次总超时 | `harvest_attempt_timeout_seconds`：`<=0` 时默认 90s；`deploy/config.example.yaml` 写 25s。context deadline 覆盖连接+响应+读流 | `openai_codex_ticket.go:94-99,388-389` |
| 传输层 | 默认 dial timeout 10s、TLS 握手 10s、`ResponseHeaderTimeout` 默认 300s（profile 未覆盖）；实际先被单次总超时截断 | `repository/http_upstream.go:55-69,971-988,1320-1362` |
| 读流上限 | 2 MiB body、scanner 单行 1 MiB | `openai_codex_ticket.go:428-430` |
| 并发 | 账号级 provider 并发参数 `account.Concurrency` 传入连接池 | `:412` |
| 历史记录 | 每次尝试写 `codex_ticket_attempts`（含 reason_code、http_status、ticket_length、turn_state_present、cookie_present、parsed_number_count、fingerprint_*），保留 90 天 | `openai_codex_ticket_harvest.go:176-195`、`repository/codex_ticket_attempt_repo.go:36-112,188-205` |

---

## 3. 期望响应与「有效票」判定

### 3.1 期望的上游响应（真实客户端侧观测，作为对照）

- `HTTP/1.1 200`，`transfer-encoding: chunked`，`content-type: text/event-stream`；
- 响应头里出现（真实客户端 /responses 观测）：`x-codex-turn-state`（`gAAAAA…` 长 blob）、`x-codex-plan-type`、`x-codex-active-limit`、`x-codex-primary/secondary-used-percent`、`x-codex-*-reset-at`、`x-codex-credits-*`、`x-models-etag`、`x-oai-request-id`、`x-openai-proxy-wasm`、Cloudflare 系列；
- `Set-Cookie` 里出现 `__oailb`（路由票 cookie），通常还带 `__cf_bm` 等 Cloudflare cookie；
- body 是 SSE：`response.output_text.delta` 若干 + `response.completed`（探针只认这两种事件，`openai_codex_ticket.go:432-456`）。

### 3.2 代码的判定顺序（`openai_codex_ticket_harvest.go:225-287`）

按顺序逐关，任何一关不过就落对应 reason_code：

| # | 关卡 | 通过条件 | 失败 reason |
| --- | --- | --- | --- |
| 1 | 请求错误 | `probeErr == nil`（含 timeout/identity/template 分类） | `request` / `timeout` / `identity_resolution_failed` / `template_invalid` |
| 2 | HTTP 状态 | `status == 200` | `http_status`（401/403/404/429 全走这里） |
| 3 | 指纹可解析 | 抽取到的数字个数 ≥ `max(80, ceil(N*0.55))`（N=292→161，N=332→183） | `insufficient_numbers` |
| 4 | 指纹命中 | `ModelTracePredict(...).Model == 请求 model`（整串精确相等） | `fingerprint_mismatch` |
| 5 | 凭证存在 | `state != ""` **或** `cookie != ""`（**或**，不是且） | `missing_credentials` |
| 6 | 落库 | `accountRepo.UpdateExtra` 成功 | `persistence` |

成功票对象（`openai_codex_ticket_harvest.go:271-279`）：

```go
&openAICodexTicket{
  AccountID, Model, State: <header 值>, Length: len(state),
  GenerationID: uuid.NewString(), VerificationMethod: "modeltrace_v1",
  FingerprintCommit: <当前 ModelTrace bank commit>, CapturedAt: now,
  Attempts: 1, HTTPStatus: 200, Cookie: <所有 Set-Cookie 的 name=value 拼接>,
}
```

**「票有效」在注入侧的定义**（`openai_codex_ticket.go:192-199` `structurallyValid`/`usable`）：

- `GenerationID != ""`
- `VerificationMethod == "modeltrace_v1"`
- `FingerprintCommit != ""`
- `State` 或 `Cookie` 非空

**不校验**：长度、`gAAAAA` 前缀、有效期、账号/模型之外的一致性。`usable()` 直接返回 `structurallyValid()`（TTL 参数被忽略）。

### 3.3 292 / 308 / 332 的真相，以及长度/前缀规则何时失效

`git show v1.1.10:backend/internal/service/openai_codex_ticket_rules.go`（v1.1.10 及以前留下的死代码；下为节选/伪代码）：

```go
func openAICodexTicketTargetLength(account, configured) int {
    // plan_type 归一化后 == "team" 或 "selfservebusinessprolite" → 332
    // 否则 configured（默认 292）
}
func openAICodexTicketResponseValid(account, configured, status, state) bool {
    return (status == 200 || status == 429) &&
        len(state) == openAICodexTicketTargetLength(account, configured) &&
        strings.HasPrefix(state, "gAAAAA")
}
```

时间线（`git log -S` 核对过）：

- **1.1.0–1.1.2**：`openAICodexTicketResponseValid` 有 4 处调用，长度/前缀/429 规则**真实生效**。
- **1.1.3-rc1（`8d583e5c0`）**：打票子系统重写成 `structurallyValid`（`GenerationID` + `modeltrace_v1` + `FingerprintCommit` + state/cookie），长度/前缀/429 **不再被调用**；注释 `// structurallyValid 只校验票据形状（长度规则与 gAAAAA 前缀）` 与实现不符（实际没查长度/前缀）。
- **1.1.11（`bfe7d53dc`，lint 清理）**：把已经没人调用的两个函数、TTL/reuse-window 归一化等死代码删掉；`ff59be9ac` 是该版本的 upstream-sync 发布提交。**行为上没有新变化**，只是代码与注释对齐了一半。

含义：

- **292**：个人/默认 plan 的票长度（配置默认值 `gateway.openai_codex_ticket.target_length: 292`）；同时是 ModelTrace 挑战数量下界。
- **332**：Team / Business Premium（`plan_type` = `team` / `selfservebusinessprolite`）的票长度；同时是挑战数量上界。v1.1.1 曾专门修「332 被误判无效」（commit `6486bc9db`）。
- **308**：**票相关代码/历史/测试里没有任何 308 规则**（`git log -S "308" -- "*codex*ticket*"` 为空）；唯一出现是 `codex_ticket_hypothesis_test.go:112` 把 308 当一个示例挑战数。若线上真的观测到 308，只能说明「票长度因 plan 而异」，但**当前代码不会因为 308 而拒票**。
- `gAAAAA`：所有真实 turn-state 的公共前缀（base64 解码后前 5 字节为 `80 00 00 00 00`，其余是上游不透明载荷）。旧代码拿它当「这确实是上游铸的 blob」的廉价形状校验；**现在只是常量 `openAICodexTicketStatePrefix`（`openai_codex_ticket.go:30`），没有强制校验**。
- 旧注释还写着「HTTP 200/429 携带符合长度规则、gAAAAA 前缀的票即落库」（`openai_codex_ticket.go:672-674`）——**已与实现不一致**（429 现在必然 `http_status`）。

### 3.4 `__oailb` 与 turn-state 的提取、失效判定

- **提取**（打票时）：`extractOpenAICodexResponseCookies`（`openai_codex_ticket.go:466-484`）把**所有** `Set-Cookie` 解析成 `name=value`，按 name 去重，用 `; ` 拼接。**不要求 `__oailb`**；`__cf_bm`、`_cfuvid` 也算「cookie 票」。
- **提取**（turn-state）：只读响应头 `x-codex-turn-state`，`TrimSpace`（`openai_codex_turn_state.go:95-100`）；body 里出现不算。
- **失效判定**（`codex_ticket_observation.go:149-204`）：只有当**同一账号、同一 generation、确实把这张票发出去**的请求，上游同时满足
  1. 返回了**不同**的 `x-codex-turn-state`，且
  2. 返回的 `__oailb` 与原 cookie 中的 `__oailb` **值不同**（`codexOAILBValue` 要求唯一且解析成功）
  
  才记录 `codex_ticket_invalidations`（reason `upstream_turn_state_and_oailb_changed`），并从 `accounts.extra` 用事务删除当次 generation；随后清内存缓存、清重试点、通知 harvester 立刻重打。
- 只会「替换/失效」，不会因为上游**没给** turn-state 就判失效。
- `__oailb` 的比对严格走 `http.ParseCookie`；多个同名不同值视为无效解析（不触发失效）。

---

## 4. outcome / reason_code 全表

`outcome ∈ {success, miss, error}`；`miss` = 请求成功但没拿到可用票；`error` = 本地/传输层失败（多数还会把 err 向上抛给日志）。

| outcome | reason_code | 触发条件 | 代码 |
| --- | --- | --- | --- |
| error | `template_unavailable` | 读模板失败（非 invalid 类） | `openai_codex_ticket_harvest.go:197-201` |
| error | `template_invalid` | 模板 <7 条/顺序错/占位符错/角色错（含 `PrepareCodexProbeContext` 内 invalid） | `:199-201,236-238` |
| error | `proxy_unavailable` | 打票代理池为空 / 设置不可用 → 不发请求 | `:204-208` |
| error | `upstream_unavailable` | `httpUpstream == nil`（装配异常） | `:210-213` |
| error | `credentials` | `GetAccessToken` 失败或 token 为空（含刷新失败） | `:214-218` |
| error | `challenge_generation_failed` | `crypto/rand` 失败 | `:219-223` |
| error | `request` | 其他 `probeErr`（DNS/连接/TLS/代理 5xx 之前的错误、扫描错误等） | `:234-245` |
| error | `timeout` | `errors.Is(probeErr, context.DeadlineExceeded)`（单次总超时） | `:242-244` |
| error | `identity_resolution_failed` | `ErrCodexProbeIdentity`（账号身份命名空间/时区/UUID 生成失败） | `:239-241` |
| miss | `http_status` | `status != 200`（**401 / 403 / 404 / 429 都在这里**；429 不再接受） | `:247-250` |
| miss | `insufficient_numbers` | HTTP 200 但模型没吐出足够数字（< 55%·N，且至少 80 个），或 body 不是可解析 SSE（含 200 + 错误 JSON / `response.failed` / 拒答） | `:251-258` |
| miss | `fingerprint_mismatch` | 数字够，但 ModelTrace 预测的模型 ≠ 请求模型（换模型/降智/上游路由错） | `:259-266` |
| miss | `missing_credentials` | 指纹通过，但响应既无 `x-codex-turn-state` 也无任何 `Set-Cookie` | `:267-270` |
| error | `persistence` | 写 `accounts.extra` 失败（DB） | `:276-279` |
| success | —（`TicketGenerationID` 有值） | 200 + 指纹命中 + (state 或 cookie) + 落库成功 | `:271-287` |

没有 reason_code（在写 attempt 之前就返回，通常只进 `Warn "probe unavailable"` 日志）的情况：

- `ErrCodexTicketBusy`（同 (账号,模型) 已有尝试在跑，`openai_codex_ticket_harvest.go:146-159`）
- `ErrCodexTicketRateLimited`（账号/模型限流窗口内，`:167-169`）
- `ErrCodexTicketUnavailable`（账号非 active / 参与关闭，`:160-166`）
- `ErrCodexTicketModel`（手动打票传了不支持模型，`:293-295`）
- `ErrCodexTicketNoProxy`（会先把 outcome 写成 error/`proxy_unavailable` 再向上抛，日志被静默）

日志锚点：`openai_codex_ticket harvested` / `openai_codex_ticket probe miss`（`openai_codex_ticket.go:692-696`）、`openai_codex_ticket probe unavailable`（`:686-690`）、`openai_codex_ticket probe cycle`（`:667-669`）、`openai_codex_ticket history persist failed`（`:182-184`）。

---

## 5. 「打不出来」时最可能命中哪几个

按当前代码的判定顺序和已知症状（上游不下发 turn-state / 401 / 403 / 空票）排序：

1. **`http_status`**（401/403/404/429）——如果上游已经能识别探针身份（UA/版本/无工具/请求形状），最直接的拒绝就是 401/403；版本过低是 404（`codexUpstreamMinVersion = 0.144.0`，注释称低于即 404，`openai_codex_identity.go:14-16`）。**只要非 200，就不看票、不看指纹**。
2. **`missing_credentials`**——HTTP 200、指纹也过了，但响应里既没有 `x-codex-turn-state` 也没有任何 `Set-Cookie`。这正是「上游不下发 turn-state」的字面症状（也可能是 Cloudflare/边缘直接返回了干净 200）。注意探针**不会**识别 body 里的失败事件；200 + `response.failed` 会先落到 `insufficient_numbers`。
3. **`insufficient_numbers`**——200 但模型没按挑战输出（拒答、只回一句、返回错误 JSON、SSE 空、或 `response.completed` 里没有文本）。如果上游对「无 tools 的桌面上下文」做了降级/拒绝，常表现为这个。
4. **`fingerprint_mismatch`**——上游确实回了数字，但 ModelTrace 判成别的模型：`gpt-6-astra` 请求被路由到别的模型（降智）、或 ModelTrace bank 对新模型不认（注意 bank 只认 `modeltrace_unified_bank.json` 里的 8 个 gpt 模型，见 §8）。
5. **`timeout`**——代理慢/被墙/住宅 IP 质量差；单次总超时（25s 或 90s）先于 `ResponseHeaderTimeout` 生效。
6. **`proxy_unavailable`**——代理池空（或自定义池所选代理全部过期），此时**根本不会发请求**，但日志只在 `Warn "probe unavailable"`（`ErrCodexTicketNoProxy` 被显式静默，`openai_codex_ticket.go:686-690`），很容易被误读成「上游拒了」。

**最危险的假成功（不是 miss，但等于没票）**：HTTP 200 + 指纹命中 + 只有一个 Cloudflare cookie（如 `__cf_bm`）→ 判 `success`，落一张 **cookie-only** 票；注入时 `applyOpenAICodexTicket` 会 `h.Del(x-codex-turn-state)` 只发 Cookie（`openai_codex_ticket.go:298-306`、测试 `openai_codex_ticket_test.go:49-66`）。这类票在业务侧等价于「没有 turn-state」，但面板显示「已就绪」，会掩盖问题。

---

## 6. 请求头 / 响应头对照表

### 6.1 请求头：探针 vs 业务转发 vs 真实客户端

| Header | 打票探针 | 业务转发（OAuth /responses） | 真实 Codex（0.163.0 / Desktop 26.1016.x） |
| --- | --- | --- | --- |
| `Authorization` | `Bearer <token>` | 同 | 同 |
| `Host` | `chatgpt.com` | 同 | 同 |
| `Content-Type` / `Accept` | `application/json` / `text/event-stream` | 同（流式） | 同 |
| `OpenAI-Beta` | `responses=experimental`（固定） | 透传/补注 | 通常 `responses=experimental` |
| `User-Agent` | 强制规范身份（默认 `codex-tui/0.146.0 (Ubuntu 22.4.0; x86_64) xterm-256color`） | 账号级自定义 UA 可覆盖（`enforceCodexIdentityHeadersWithUA`，`openai_gateway_forward.go:1545`） | `codex-tui/0.163.0 …` 或 `Codex Desktop/…` / `codex_chatgpt_desktop/…` |
| `originator` | `codex-tui` | 可被账号 UA 配套重建 | 与 UA 首段配套（`codex-tui` / `Codex Desktop` / `codex_chatgpt_desktop`） |
| `version` | 规范版本（默认 0.146.0，Astra≥0.153.4） | 同源重建 | 0.163.0（库版本） |
| `session_id` / `session-id` / `thread-id` / `x-client-request-id` | 每次新 UUIDv7 | 来自客户端/隔离派生 | 客户端真实会话标识 |
| `x-codex-installation-id` | 稳定派生 UUIDv4 | 透传/隔离派生 | 安装级 UUID |
| `x-codex-window-id` | 新 UUIDv7 | 透传 | 窗口级 UUID |
| `x-codex-turn-metadata` | 自造 7 字段 JSON | 透传/改写 | 客户端真实 turn 元数据 |
| `x-codex-turn-state` | **不发**（干净回合） | 有票则注入（`applyOpenAICodexTicketWithGeneration`） | 第二回合起回带 |
| `Cookie` | **不发** | 有票则注入捕获的 cookie 串 | 浏览器/客户端 cookie（含 `__oailb`） |
| `x-codex-beta-features` | **不发** | 按真实会话补注（`openai_gateway_forward.go:1562`） | 按功能发 |
| `conversation_id` / `x-codex-routing-hint` | **不发** | 可能透传/派生（`setOpenAICodexRoutingHintFromBody`） | 真实会话会有/可能有 |
| `chatgpt-account-id` | 有 | 有 | 有 |
| `Connection` | `close`（HTTP/1.1） | keep-alive / H2 | keep-alive / H2 / WS |

### 6.2 响应头：上游会给 vs 代码实际消费

| 响应头 / cookie | 上游是否给（真实观测） | 探针代码是否消费 | 用途 |
| --- | --- | --- | --- |
| `x-codex-turn-state` | 是（200 时几乎必有，`gAAAAA…`） | **是**（唯一票据来源） | 存 `State`，注入业务请求 |
| `Set-Cookie: __oailb` | 是（路由票） | 是（但**不区分**，所有 cookie 都收） | 存 `Cookie`，注入业务请求；也是失效判定的比对键 |
| `Set-Cookie: __cf_bm` / `_cfuvid` | 是（Cloudflare） | **是**（会被当成凭证） | 假成功风险源 |
| `x-codex-plan-type` / `x-codex-active-limit` / `x-codex-*-used-percent` / `x-codex-*-reset-at` / `x-codex-credits-*` | 是（额度/套餐） | 否（打票路径不读） | 业务路径用于额度快照 |
| `x-models-etag` | 是 | 否（打票路径不读） | 模型列表缓存 |
| `x-oai-request-id` / `cf-ray` / `server` | 是 | 否 | 排查用 |
| `Retry-After` | 429 时可能有 | 否（hypothesis runner 才读） | — |
| body SSE `response.output_text.delta` / `response.completed` | 是 | 是（拼文本喂指纹） | 指纹输入 |
| body `response.failed` / `error` 事件 | 可能 | **否**（不识别，退化成 insufficient_numbers） | 失败面盲区 |

---

## 7. 客户端版本 / UA 常量、自动同步、与真实客户端的差异

### 7.1 常量与解析链

| 常量 / 设置 | 值 / 作用 | 位置 |
| --- | --- | --- |
| `codexCLIVersion` | **`0.146.0`**（编译期兜底，UA 与 version 头的版本来源） | `openai_gateway_service.go:64` |
| `codexCLIUserAgentSuffix` | `" (Ubuntu 22.4.0; x86_64) xterm-256color"`（注意前导空格） | `:40` |
| `codexCLIUserAgent` / `DefaultOpenAICodexUserAgent` | `codex-tui/0.146.0 (Ubuntu 22.4.0; x86_64) xterm-256color` | `:44`、`setting_gateway_runtime.go:100-102` |
| `codexUpstreamMinVersion` | `0.144.0`，低于该值的 `version` 上游 404（issue #3901 注释） | `openai_codex_identity.go:14-16` |
| `openAICodexAstraMinVersion` | `0.153.4`；模型名含 `gpt-6`/`astra` 且版本低于它时，探针强制抬到 0.153.4（UA+version+originator 一起） | `openai_codex_ticket.go:29,490-500` |
| `codexHypothesisClientVersion` | `0.156.1`，**仅**离线 hypothesis CLI 用；`NewCodexHypothesisRunner` 会改**进程级** UA resolver | `codex_ticket_hypothesis.go:24,119` |
| `openai_codex_client_version` | 管理员覆写版本（优先） | `domain_constants.go:704-711`、`setting_gateway_runtime.go:483-538` |
| `openai_codex_client_version_synced` | 自动同步写入（管理员覆写为空时生效） | 同上 |
| `openai_codex_version_auto_sync_enabled` | 默认 true；6h 一次，GitHub `openai/codex` `/releases/latest`，只前进不后退 | `openai_codex_version_sync_service.go:11-27,121-145` |
| `openai_codex_user_agent` | 可选 UA 指纹；**只贡献客户端名/OS/架构/终端，版本段一律用生效版本重建** | `setting_gateway_runtime.go:433-481,645-665` |
| `gateway.disable_codex_identity_enforcement` | 关闭后仅做「UA↔originator 配对 + 版本下限」，不再强制统一身份 | `openai_codex_identity.go:44-60,222-249` |

生效身份（探针走的路径）：`ensureCodexIdentityHeaders` → `enforceCodexIdentityHeaders`（强制统一，用**全局**规范 UA，**忽略账号级 `user_agent` 凭据**）→ Astra 版本抬升。业务转发则是 `enforceCodexIdentityHeadersWithUA(req.Header, s.codexIdentityOverrideUA(account))`，**账号级自定义 UA 生效**（`openai_gateway_forward.go:1545,1575-1580`）。→ **同一账号的打票请求与业务请求可能是两个不同的 UA 身份**，这是探针独有的可识别点。

### 7.2 与真实客户端（runtime 取证，仓库外证据）的差异

真实客户端事实（来自队友 runtime 取证，已记录在 `scratch/notes-runtime.md` / `scratch/logs_ctx_out.txt`，非本仓库代码）：

- 库版本 `client_version = 0.163.0`；模型列表请求 `GET …/codex/models?client_version=0.163.0`；
- Desktop 壳的 app-server 身份：`client_name="Codex Desktop"`、`client_version="26.1016.51333"`（与库版本是两个维度）；
- 真实推理既走 `responses_http`（HTTP）也大量走 `responses_websocket`（`wss://chatgpt.com/backend-api/codex/responses`）；
- 真实 `/responses` 响应带 `x-codex-plan-type`、`x-codex-active-limit`、`x-codex-credits-*`、`x-models-etag`、`x-codex-turn-state`。

差异清单：

1. **版本**：默认 `0.146.0` vs 真实 `0.163.0`。自动同步正常时会被抬到最新稳定版；若 `openai_codex_client_version` 被手工钉死、或同步被关/GitHub 不可达，就会长期落后（落后身份是被上游优先降载/拒绝的一侧，注释明说）。
2. **UA 家族**：探针是 `codex-tui`；真实客户端在桌面场景是 `Codex Desktop/…` 或 `codex_chatgpt_desktop/…`（仓库的官方 UA 白名单里两种都认，`pkg/openai/request.go:26-60`）。探针把自己伪装成 TUI，却发桌面端的初始上下文（矛盾）。
3. **body 初始上下文来源**：模板 `session_meta.originator = "Codex Desktop"`（`codex_probe_template.go:164` 强制校验），但 header originator 是 `codex-tui`；这个「body 说是 Desktop、header 说是 TUI」的组合真实客户端不会产生。
4. **没有 tools**：真实 Codex 回合几乎总带工具定义（shell/apply_patch 等）；探针一个都没有，且 `instructions` 只有一句通用英文（不是真实 Codex 系统提示）。
5. **没有 `x-codex-beta-features` / `conversation_id` / `x-codex-routing-hint`**：业务转发会按真实会话补 `x-codex-beta-features`，探针不会。
6. **传输**：探针 HTTP/1.1 + `Connection: close` + 每次新建 TLS；真实客户端 H2 长连接 / WS。TLS/ALPN/连接复用行为本身可被指纹。
7. **出口 IP**：探针每次可能换住宅代理 IP（轮转），真实客户端是稳定出口；且探针时区按账号渲染（默认 `Asia/Singapore`），不随出口 IP 变化。
8. **会话**：探针永远是新 session/thread/turn（UUIDv7），无历史、无 `x-codex-turn-state` 回带；真实回合有线程历史。
9. **安装身份**：探针的 installation id 是按 ChatGPT 账号派生的稳定 UUIDv4（同账号多本地行共享），真实客户端是真实安装 UUID。
10. **工具/推理字段**：探针无 `tools`、`reasoning`、`include`、`tool_choice`、`parallel_tool_calls`、`max_output_tokens` 等真实请求常见字段。

---

## 8. 可调开关总表（哪些真的生效）

### 8.1 全局（设置键 > YAML/env > 内建默认）

| 开关 | 默认 | 作用 | 生效点 |
| --- | --- | --- | --- |
| `openai_codex_ticket_enabled` / `gateway.openai_codex_ticket.enabled` | **false**（1.1.4 起 opt-in，迁移 244 会关一次） | 打票总开关；关 = 不打票、不注入、不 gate | `setting_gateway_runtime.go:299-351` |
| `openai_codex_ticket_allow_without_ticket`（DB）/ `gateway.openai_codex_ticket.fail_closed`（YAML） | allow=true / fail_closed=false（允许无票转发） | 无票时是否放行；账号级 `extra.codex_allow_without_ticket` 优先 | `openai_codex_ticket.go:805-822` |
| `openai_codex_ticket_prompt_template` | 内置 7 行模板 | 打票/降智检测共用的初始上下文；非法则 `template_invalid` | `setting_codex_probe_template.go` |
| `openai_codex_ticket_proxy_pool` | `{"mode":"all","proxy_ids":[]}` | 打票代理池（all / custom） | `codex_ticket_pool.go` |
| `openai_codex_ticket_harvest_proxy_url` / `gateway...harvest_proxy_url` | 空 | **打票路径已不再读取**（仅面板展示/校验；grep 只有 handler & 测试） | `setting_gateway_runtime.go:368-431`（无调用方） |
| `openai_codex_ticket_cadence` | retry 10–30s，refresh 1800s | 失败重试抖动 + 成功后刷新周期（refresh=0 关闭周期刷新） | `codex_ticket_cadence.go` |
| `openai_codex_client_version` / `_synced` / `openai_codex_user_agent` / `openai_codex_version_auto_sync_enabled` | 见 §7 | 出站身份 | §7 |
| `gateway.disable_codex_identity_enforcement` | false | 关闭强制统一身份 | §7 |

### 8.2 账号级（`accounts.extra`）

| 键 | 默认 | 作用 |
| --- | --- | --- |
| `codex_ticket_harvest_enabled` | 未设 = 参与 | false 时该账号完全不打票 |
| `codex_ticket_harvest_models` | `{model: bool}`，缺省 true | 按模型退出/参与 |
| `codex_allow_without_ticket` | 未设 = 跟全局 | 无票时是否放行（优先于全局） |
| `codex_turn_ticket:<model>` | — | 服务端管理的票据 JSON（**不要手工编辑**，会被 harvester 覆盖；导出时被 redact） |
| `codex_ticket_ready_models` | — | 调度快照投影（`SchedulerTicketProjection` 时用于调度门控） |

> 注意：`applyOpenAICodexTicketWithGeneration` **先注入已有可用票，再判断「是否允许无票」**（`openai_codex_ticket.go:298-310`）。因此把账号/模型参与关掉只会**停止打票、解除无票拦截**，不会撤销 `accounts.extra` 里已存在的票——旧票仍会被注入，直到被新 generation 覆盖或被失效事件删除。要彻底停用需同时清掉该账号 `codex_turn_ticket:<model>`（或全局关闸会跳过注入）。

### 8.3 模型参与（关键坑）

- 打票与注入的模型集合**不是** YAML `gateway.openai_codex_ticket.models`：`openAICodexTicketConfig()` 每次都把 `cfg.Models` 覆盖成 `ModelTraceTicketModels()`（`openai_codex_ticket.go:109`）。
- 运行时集合 = ModelTrace bank 内 `family=gpt` 且版本 `>5.6`（即 major>5 或 5.x 且 minor≥6）的模型。当前内置 bank（`modeltrace_unified_bank.json`）算出 **6 个**：`gpt-5.6-sol`、`gpt-5.6-terra`、`gpt-5.6-luna`、`gpt-6-astra`、`gpt-6-sol`、`gpt-6-luna`（`openai_codex_ticket_test.go:59` 断言 6 个，吻合）。
- bank 会每小时从 `github.com/xqy2006/ModelTrace` 刷新（`codex_fingerprint_update.go:50-114`），所以这个集合会随上游漂移：**新模型加入前不会被打票，也不会被 gate**（静默「不需要票」）。
- YAML 里的 `models: [gpt-6-astra, gpt-5.6-sol]`、`openAICodexTicketDefaultModel`/`DefaultSolModel` 常量、`target_length`、`ttl_seconds`、`refresh_before_seconds` 在当前代码里**基本是死配置**（只出现在日志/结构体，`openai_codex_ticket.go:521`）。

### 8.4 批量 / 并发

- 没有「批量打票」接口；自动打票按账号×模型并发（`refreshOpenAICodexTickets` 的 goroutine），受账号级 PG advisory lock 与进程内 in-flight 去重限制。
- 每次尝试换一个池内代理（按 (账号,模型) 轮转计数）；`account.Concurrency` 传给连接池。

---

## 9. 与真实客户端差异清单（按「被识破」可疑度排序）

| # | 差异 | 为什么可疑 | 建议验证方向 |
| --- | --- | --- | --- |
| 1 | body 是 Desktop 录制上下文，header 是 `codex-tui` | 真实客户端 header 与初始上下文同源；两者矛盾是强信号 | 抓真实 Desktop 的 `originator`/UA/body 对齐关系 |
| 2 | 无 `tools`、`instructions` 只有一句 | 真实 Codex 回合几乎必带工具；「无工具 + 桌面上下文 + 要求纯数字」不像真实用户 | 对比真实请求 body 的工具/指令形状 |
| 3 | 版本 0.146.0 | 落后 0.163.0 十几个版本；上游注释明确「陈旧身份优先降载/丢弃」 | 检查 `openai_codex_client_version(_synced)` 实际值 |
| 4 | 探针忽略账号级 UA | 打票身份 ≠ 业务身份，同账号两种指纹 | 看账号 `credentials.user_agent` 是否非空 |
| 5 | HTTP/1.1 + close + 每发新 TLS | 真实是 H2/WS 长连接；Cloudflare 侧 TLS/ALPN/连接行为不同 | 若怀疑 TLS 指纹，需对比 uTLS/ALPN |
| 6 | 代理轮换 + 时区固定 | 出口 IP 与账号地理/时区不一致 | 对比代理国家 vs 账号 timezone |
| 7 | 新 session/thread/turn | 没有「真实回合的前置上下文」；若上游要求 turn-state 只发给带历史的回合，永远抽不到 | 用带 ticket 回带/带工具的探针做 A/B（只读分析，不改代码） |
| 8 | 请求跳过 `x-codex-beta-features` | 真实会话按功能带 | 对比真实请求头集合 |
| 9 | 只用响应头抓 turn-state | 若上游改从 SSE/WS 消息或 body 下发，探针读不到 | 检查真实客户端 `codex-api/src/sse/responses.rs` 行为（上游仓库） |
| 10 | 无 `__oailb` 专属校验 | 任何 cookie 都收，容易「假成功」 | 检查 attempt 表的 `turn_state_present` / `cookie_present` 组合 |

---

## 10. 代码事实核查（容易搞错的点）

1. **打票不现场注入旧票**：探针请求无 `x-codex-turn-state`、无 `Cookie`（测试断言）。
2. **不再接受 429**：旧注释仍写 200/429，但实现只认 200。
3. **不校验长度 / 不校验 `gAAAAA` / 不校验 TTL**：`structurallyValid` 只查 4 个字段；`ExpiresAt` 从未被赋值；`RemainingSeconds`、`ExpectedTicketLength` 从不填充；`usable()` 忽略时间和配置参数。测试明确覆盖「7 天前捕获 + 已过期仍可用」（`openai_codex_ticket_test.go:68-78`）。
4. **`harvest_proxy_url` 已是死配置**：打票只走代理池；池空 → `proxy_unavailable`，不会直连。
5. **YAML `models` 被运行时模型表覆盖**：配置值不生效，见 §8.3。
6. **成功后刷新周期**：默认 1800s 重打；`harvest_refresh_seconds=0` 表示成功后不再自动刷新（只依赖失效事件），不是「永不过期地可用」——票本身确实无 TTL。
7. **票是「全站共享」的**：落 `accounts.extra` 的 `codex_turn_ticket:<model>`（DB JSONB），所有实例/重启都读同一份（`repository/codex_ticket_invalidation_repo.go:14-22`），进程内 `openaiCodexTickets` 只是缓存。DB 共享与「结构化票」判定都引入于 **1.1.3-rc1 / 1.1.4**（`8d583e5c0`、`4dbf98c97`），**不是 1.1.11**；1.1.11 只是删死代码（该版 CHANGELOG 也未提票据共享，只有 upstream sync/打包）。这两件事不要混。
8. **失效只在「票确实发出去了且上游换票」时触发**：请求侧会先做 snapshot（同 generation、state 一致、`__oailb` 一致），不满足就静默返回，避免误失效（`codex_ticket_observation.go:36-89`）。
9. **`codex_ticket_hypothesis.go` 不是生产打票路径**：它只服务 `cmd/codex-ticket-hypothesis`，还会改全局 UA（0.156.1），不要在服务器进程里调用它做诊断。
10. **降智检测 ≠ 打票**：诊断走正常网关 ingress（120s 超时），会受正常票据策略/限流影响，但仍用同一模板和 ModelTrace 校验（`docs/CODEX_PROBE_TEMPLATE.md:43-53`）。

---

## 11. 只读排查锚点（给有 DB/日志权限的人）

**attempt 表**（最直接的「打票到底死在哪一关」）：

```sql
SELECT occurred_at, account_id, model, outcome, reason_code, http_status,
       ticket_length, turn_state_present, cookie_present,
       challenge_expected_count, parsed_number_count,
       fingerprint_matched, fingerprint_predicted_model,
       proxy_name, duration_ms
FROM codex_ticket_attempts
ORDER BY occurred_at DESC
LIMIT 50;
```

判读：

- `http_status IN (401,403,404,429)` → 身份/权限/版本问题；
- `http_status=200 AND turn_state_present=false AND cookie_present=false` → **`missing_credentials`：上游确实不发票**；
- `http_status=200 AND turn_state_present=false AND cookie_present=true` → 假成功（大概率只有 `__cf_bm`）；
- `parsed_number_count` 远小于 `challenge_expected_count*0.55` → `insufficient_numbers`；
- `fingerprint_matched=false` + `fingerprint_predicted_model` → 降智实锤。

**设置 / 账号 / 代理池**：

```sql
SELECT key, value FROM settings WHERE key IN (
 'openai_codex_ticket_enabled','openai_codex_ticket_allow_without_ticket',
 'openai_codex_ticket_prompt_template','openai_codex_ticket_proxy_pool',
 'openai_codex_client_version',
 'openai_codex_client_version_synced','openai_codex_user_agent',
 'openai_codex_version_auto_sync_enabled');

SELECT id, extra->'codex_ticket_harvest_enabled' AS harvest_enabled,
       extra->'codex_ticket_harvest_models' AS harvest_models,
       extra->'codex_allow_without_ticket' AS allow_no_ticket,
       extra->'codex_turn_ticket:gpt-6-astra'->>'length' AS len_astra,
       extra->'codex_turn_ticket:gpt-6-astra'->>'generation_id' AS gen_astra
FROM accounts WHERE platform='openai';

SELECT id, name, host, port, expires_at FROM proxies WHERE status='active';
```

**日志 grep**：`openai_codex_ticket probe miss` 看 `reason` 分布；`openai_codex_ticket probe unavailable` 看 `ErrCodexTicketNoProxy`（池空）与 `ErrCodexTicketBusy`；`codex ticket invalidation` 看上游换票频率。

**结论性判断**：如果 attempt 里大量 `200 + turn_state_present=false + cookie_present=true/false`，问题在**上游不再为这个探针形状铸造票**（形状/身份/传输），而不是本地门控；如果大量 401/403/404，则是身份/版本/权限被拒；如果 `insufficient_numbers`/`fingerprint_mismatch`，则请求被接受但模型行为被改变（降智/换模型）。

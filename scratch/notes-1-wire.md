# 角 1：Codex 线上传输与协议（notes-1-wire）

> 角色：teammate `agent1-wire`。目标：把「一条真实 /responses 请求到底长什么样」钉死。
> 只读目标，未执行 codex.exe，未联网下载。token/cookie/turn-state 只报长度/前缀/是否出现/时间/位置，不贴明文。
> 坐标系：RVA = 相对 image_base `0x140000000`；`.text` RVA `0x1000`，`.rdata` RVA `0xf621000`。所有 0x 地址 = RVA（文件偏移 = RVA − 0x1800，见 notes-binary §0）。
> 结论分级：**[观察]** = 二进制指令/字符串或日志直接看到；**[推断]** = 由观察组合出的行为判断；**[未知]** = 静态/日志无法回答。

---

## 0. 一句话结论（先看）

**上一轮「拿票必须走 WS 升级」的结论是错的。** 事实是：

1. `x-codex-turn-state` 由**服务端在 `POST /responses`（HTTP/SSE）的响应头里下发**，客户端在 HTTP 流处理路径（`codex-api/src/sse/responses.rs`）里读取它；**本机日志独立复现：347 条 HTTP /responses 完成里 142 条响应头带票，全部 780 字符**。WS 只是客户端**回带**（续传）票的通道，不是「唯一拿票通道」。
2. sub4api 探针「用 HTTP POST 读响应头拿票」的方向**是对的**；它失败的根因是**请求形状/身份头**，不是传输选错。
3. 探针有一个**硬错误**：它发 `OpenAI-Beta: responses=experimental`，而真实 0.163.0 客户端**全二进制 0 次出现**该值——真实客户端的 `OpenAI-Beta` 只有 `responses_websockets=2026-02-06`，且**只在 WS 握手**时带；HTTP 路径**根本不发 `OpenAI-Beta`**。

---

## 1. 两种传输：HTTP/SSE 与 responses_websocket

### 1.1 HTTP/SSE（`POST /responses`）

| 项 | 值 | 证据 |
|---|---|---|
| URL | `https://chatgpt.com/backend-api/codex/responses` | 二进制 base `https://chatgpt.com/backend-api/codex`（`0xf804e58`/`0xfd97f68`）+ `/responses` 路径；`codex-api\src\endpoint\responses.rs`；日志 `url=https://chatgpt.com/backend-api/codex/responses` [观察] |
| Method | `POST` | 日志 `method=POST` [观察] |
| Accept | `text/event-stream` | 与 `x-client-request-id`/`x-openai-subagent` 连排于 responses.rs 簇（`0xfad8790`、`0xfb143e1`、`0xfb6c359`、`0xfbb8328`、`0xfccbc11`、`0xfd7b709`）[观察] |
| Content-Type | `application/json` | 同簇上下文 + sub4api 探针对照 [观察/推断] |
| OpenAI-Beta | **无**（HTTP 路径不发） | 见 §4.3 [观察] |

HTTP 请求头全集（responses.rs 端点 + core 共享头构建，[观察+推断]）：
- 传输/身份：`Authorization: Bearer <token>`、`originator`、`User-Agent`、`version`、`chatgpt-account-id`
- 会话：`session_id`、`session-id`、`thread-id`、`x-client-request-id`、`x-codex-installation-id`、`x-codex-window-id`
- 私有：`x-codex-turn-metadata`（JSON）、`x-openai-subagent`（子 agent 时）、`x-codex-beta-features`（按功能，见 §2 回带点）
- 明确**没有**：`OpenAI-Beta`、`x-oai-attestation`、`x-codex-turn-state`（首请求）、`x-codex-routing-hint`（HTTP 路径未见其插入点，仅在 WS 簇）

Body 关键字段（Responses API 形状）：`model`、`instructions`、`input[]`（developer/user 的 `input_text` 分片）、`tools[]`、`stream: true`、`store: false`、`prompt_cache_key`、`client_metadata`（含 `session_id`/`thread_id`/`turn_id`/`x-codex-installation-id`/`x-codex-window-id`/`x-codex-turn-metadata`）、`reasoning`、`include`、`max_output_tokens` 等。[观察（字符串+sub4api 模板）+推断（完整 body 需抓包）]

SSE 事件名全集（事件名表 `0xf8bcd80` 密集区，[观察]）：
```
response.created
response.in_progress
response.output_item.added / response.output_item.done
response.content_part.added / response.content_part.done
response.output_text.delta / response.output_text.done
response.reasoning_text.delta（+ reasoning_summary_text.delta/done、reasoning_summary_part.added/done）
response.custom_tool_call_input.delta/done
response.function_call_arguments.delta/done
response.metadata            （§2 读票点 B 所在）
codex.response.*             （codex 私有事件，前缀可见，全名被截断）
response.incomplete
response.completed
response.failed
```
另有 WS 侧事件（`websocket_timing` 等遥测字段）与 `response.interrupt`/`discard_partial_items`（流中断处理，`0xfbb5ddb` 等）。[观察]

### 1.2 responses_websocket

| 项 | 值 | 证据 |
|---|---|---|
| URL | `wss://chatgpt.com/backend-api/codex/responses` | 日志 `path="/responses"}: connecting to websocket: wss://chatgpt.com/backend-api/codex/responses` [观察] |
| 库 | tungstenite | `tungstenite::handshake::client` + `Sec-WebSocket-Key`/`Sec-WebSocket-Version 13`/`Sec-WebSocket-Accept`/`Sec-WebSocket-Protocol`（`0x105b4419`/`0x105b2f50`）[观察] |
| 子协议 | **未发现子协议字面量** | `Sec-WebSocket-Protocol` 仅 tungstenite 内部 1 处；未找到 codex 传入的子协议值 [未知] |

WS 握手头（四元组连排于 `0xfadadd8`/`0xfb1d990`/`0xfb74d28`/`0xfbc04c8`/`0xfd7d0c8`，[观察]）：
```
Authorization: Bearer <token>
OpenAI-Beta: responses_websockets=2026-02-06
x-oai-attestation: <运行时计算，无字面量>
x-responsesapi-include-timing-metrics: <值>
```
再加 codex 私有（WS 请求头结构体字段，`codex-api\src\endpoint\responses_websocket.rs` 簇 `0xfad67c2`/`0xfb13b7d`/`0xfb6b7d5`，[观察]）：
```
x-codex-turn-state        （§2 回带点，仅此处）
x-codex-beta-features     （与 turn-state 同一 builder）
x-codex-routing-hint
x-codex-turn-metadata
x-codex-installation-id / x-codex-window-id
x-codex-ws-stream-request-start-ms
traceparent / tracestate  （W3C）
session_id / thread_id / turn_id / root_turn_id / parent_response_id
originator / User-Agent / version
```

---

## 2. `x-codex-turn-state` 读取点 / 回带点 / 时序

字符串在 `.rdata` 共 **9 份副本**（rustc CGU 复制）：`0xf7afd60, 0xf8be008, 0xfad8f98, 0xfb170f8, 0xfb6ed28, 0xfbc8898, 0xfcd3e20, 0xfd7d1e0, 0xfd91308` [观察，与本轮 `wire_scan` 一致]。

### 2.1 读取点 A —— WS 握手响应头（`connector.rs`）
函数 `0x27d2490`（+ 5 个单态化副本 `0x89d4830`/`0x9c142b0`/`0xa9f0180`/`0x816a940`/`0xa93a7d0`），引用点 `0x27d3c70`：
```
0x27d3c70  lea rdx, "x-codex-turn-state"   ; 名
0x27d3c85  mov r8, 0x12                    ; len=18
0x27d3c8b  call fn_0x1399bd0               ; get_header(响应头map, name)
```
指纹含 `successfully connected to websocket` / `failed to connect to websocket` / `/responses` → WS 升级响应头读取 [观察]。
（该副本的调用者是 `0x3676be0`＝WS 可达性自检函数；其余 5 个单态化副本由 WS turn 构建器调用。[观察+推断]）

### 2.2 读取点 B —— HTTP/SSE 流处理（`sse/responses.rs`）
函数 `0x3d135c0`（size 3310，指纹 `codex-api\src\sse\responses.rs`），引用点 `0x3d13e3c`：
```
0x3d13e3c  lea rdx, "x-codex-turn-state"
0x3d13e4a  mov r8, 0x12
0x3d13e50  mov r9, [rbp+0xd88]             ; 头 map
0x3d13e57  call fn_0x3d404c0               ; get(map, name)
```
同一函数从**同一张 map** 还读 `X-Models-Etag`(`0x3d13b17`)、`openai-model`(`0x3d13bf5`)、`x-reasoning-included`(`0x3d13cc5`)、`x-request-id`(`0x3d13d0e`) 与 rate-limit 百分比。→ 这张 map 是「HTTP 响应头 + `response.metadata` 内容」的合并/头 map；**本机日志独立证明票就出现在 HTTP 响应头**（见 §2.4）。[观察]
调用者：`0x6e81c80`/`0x745ac00`/`0x7d191a0`/`0x8618ef0`/`0x9c11b40`/`0xa8acc70`——其中 `0x7d191a0`/`0x9c11b40`/`0xa8acc70` 不在 WS 回带 builder 调用者名单里 → **存在纯 HTTP 回合执行路径**。[观察]

### 2.3 回带点（唯一写点）
函数 `0x78719e0`（size 0x337）：
```
0x7871b0d  lea rdx, "x-codex-beta-features"   ; len 0x15
0x7871b22  call fn_0x79d9530                  ; insert(name, value)
...
0x7871c5d  lea rdx, "x-codex-turn-state"      ; len 0x12
0x7871c72  call fn_0x79d9530                  ; insert
```
（含 HeaderValue 合法性校验：逐字节 `>0x1f 或 ==0x09`。）
调用者（10 处，**全部是 WS turn 构建器**）：`0x6e81c80, 0x6e896a0, 0x745ac00, 0x74633f0, 0x7d5c6f0, 0x7d60560, 0x8618ef0, 0x872dc30, 0xa8b04c0, 0xa8b4780`。[观察]
→ **客户端只在 WS 握手头里回带 turn-state；纯 HTTP 路径不回带。** 因此「把票塞进 HTTP 业务请求头」官方客户端自己不会做（与 notes-binary 一致），但「从 HTTP 响应头拿票」完全成立。

### 2.4 时序（同一次 turn）
1. turn 开始 → 第一次 `/responses` 请求（HTTP 或 WS）。
2. 服务端在**响应头**下发 `x-codex-turn-state`（HTTP 响应头由日志坐实；WS 握手响应头由读取点 A 坐实但客户端不落日志）。
3. 客户端读票（HTTP 走读取点 B，WS 走读取点 A）。
4. turn 续传（continuation）时，客户端在**下一次 WS 握手**回带（读取点→写点 `0x78719e0`）。

**本机日志独立复现**（合并 `scratch/sqlite-copy` + `scratch/sqlite-copy-client`，35,810 行去重）：
- HTTP `/responses` 完成 **347** 条，全部 status=200；
- 其中 **142** 条响应头带 `x-codex-turn-state`，**长度 100% = 780**，时间 2026-10-21 19:57:23 → 2026-10-23 00:40:34 JST；
- 位置：`codex_http_client::client` 的 ` Request completed ... headers={...} `（reqwest 响应头 dump）→ **HTTP 响应头，不是 SSE body**；
- 按 turn 看（二手，来自 client-evidence 笔记 §3.2）：首请求 139/141 带票，续请求 0/203 → **「每 turn 一张、首个非增量请求下发」**。

---

## 3. Cookie：`__oailb` / `__cflb` / `__cf_bm`

| Cookie | 二进制中出现 | 出现端点（运行时） | 值长 | 属性/备注 |
|---|---|---|---|---|
| `__oailb` | **0 次**（ASCII/UTF16/`oailb` 子串全无） | **仅 `GET /backend-api/codex/models`**（128/1642） | 317 | `Path=/; HttpOnly; Secure; SameSite=Lax`；与 `__cflb` 成对（二手：client-evidence §4） |
| `__cflb` | 0 次 | 仅 `/models`（与 `__oailb` 成对） | 43 | 二手：client-evidence §4 |
| `__cf_bm` | 0 次 | `/models`（多）+ **`/responses` 15/347** | 197–199 | `/responses` 上**唯一**的 Set-Cookie |

- **`/responses` 从不发 `__oailb`/`__cflb`**；它只发 Cloudflare `__cf_bm`（15 次，约 4.3%）。→ 打票若「抓 /responses 任意 Set-Cookie 当票」只能得到 `__cf_bm` = **cookie-only 假票**（与 Lead / client-evidence 结论一致）。[观察]
- 客户端 cookie jar：`reqwest-0.12.28` + `cookie_store-0.22.1`，字符串 `inserting Set-Cookie '` / `unable to store Set-Cookie`（`0x1043ba59..` 文件偏移区），`SetCookie`/`ReadCookie` 导出符号。→ 客户端对任何 `Set-Cookie` **被动存取**，同域后续请求自动回带；它不认识任何具体 cookie 名，`__oailb` 只是服务端/边缘下发的路由 cookie。[观察 + 推断]

---

## 4. 版本 / User-Agent / originator / OpenAI-Beta / x-oai-attestation

### 4.1 版本值域
- **库版本 `0.163.0`**：`models_cache.json` `client_version="0.163.0"`（fetched_at 2026-10-22T17:29:18Z，etag 与 10/21 相同）；二进制内版本字面量 `0.163.0-alpha.11.1`（`0xf7f6c98`，紧邻 `codex-tui`）。[观察]
- **桌面壳版本**：`client_name="Codex Desktop"` + `client_version="26.1016.51333"`→`"26.1016.61520"`（二手：client-evidence §6）。[二手]
- sub4api 探针默认 `0.146.0`（`codexCLIVersion`，`openai_gateway_service.go:64`），Astra/gpt-6 抬到 `0.153.4`。[观察]

### 4.2 User-Agent / originator 值域（常量，[观察]）
`0xfd0891e`/`0xfe74ab8`/`0xfe788cd` 等簇：`codex-tui`、`codex_cli_rs`、`codex_vscode`、`codex_cli_ide`、`codex-app-server`、`codex_desktop`、`Codex Desktop`、`codex_mcp_server`、`codex_exec`、`codex-cli`、`codex_sdk_ts`。另有 `CODEX_INTERNAL_ORIGINATOR_OVERRIDE`（环境变量可覆盖 originator）、`Falling back to default Codex originator because base user agent string is invalid`、UA 后缀 sanitize 逻辑。
→ `originator` 与 UA 首段配对：`codex-tui` / `codex_cli_rs` / `codex_vscode` / `codex_desktop`（`Codex Desktop`）。真实 Desktop 场景 originator=客户端家族名，**不是固定 `codex-tui`**。[观察+推断]

### 4.3 OpenAI-Beta（关键，纠正上一轮）
- `responses=experimental`：**全二进制 0 次**。[观察]
- `OpenAI-Beta` 字符串**仅 6 次**，全部与 `responses_websockets=2026-02-06` 连排（`0xf836107`、`0xfad9608`、`0xfb1c1c0`、`0xfb73558`、`0xfbbecf8`、`0xfd7d0f8`）。[观察]
- → **真实客户端只在 WS 握手发 `OpenAI-Beta: responses_websockets=2026-02-06`；HTTP 路径不发任何 OpenAI-Beta。** sub4api 探针在 HTTP 上发 `responses=experimental` 是**自造头，真实客户端从不发**。[观察+推断]

### 4.4 x-oai-attestation
- 5 份副本，**全部**与 `responses_websockets=2026-02-06`/`OpenAI-Beta`/`x-responsesapi-include-timing-metrics` 连排（`0xfadadd8`/`0xfb1d990`/`0xfb74d28`/`0xfbc04c8`/`0xfd7d0c8`）；HTTP responses.rs 头簇中**不出现**。[观察]
- → 推断为 **WS 握手专属头**，值在运行时计算（无字面量）。HTTP 路径不带。[推断]

---

## 5. 结论：最小正确请求（HTTP 版 / WS 版）+ 与 sub4api 探针差异

### 5.1 想「从上游拿到 turn-state」的最小正确请求 —— HTTP 版
```
POST https://chatgpt.com/backend-api/codex/responses   （HTTP/2 keep-alive；别用 Connection: close + 每次新建 TLS）
Host: chatgpt.com
Authorization: Bearer <oauth token>
Content-Type: application/json
Accept: text/event-stream
originator: <与 UA 首段一致：codex-tui 或 Codex Desktop>
User-Agent: <同一家族>/<0.163.0> ...
version: 0.163.0
session_id / session-id / thread-id / x-client-request-id: <uuidv7>
x-codex-installation-id: <稳定 uuid>
x-codex-window-id: <uuidv7>
x-codex-turn-metadata: {完整 ~30 字段 JSON：installation_id, session_id, thread_id, agent_name, turn_id,
                        window_id, window_number, context_window_id, request_kind, code_mode_tool_names,
                        tool_namespaces_info, turn_started_at_unix_ms, history_ingest_requested,
                        analytics_enabled, forked_from_thread_id, parent_thread_id, parent_turn_id,
                        root_turn_id, subagent_kind, thread_source, turn_trigger, sandbox, ...}   ← 探针只有 7 字段
x-openai-subagent: <子 agent 标记，适用时>
chatgpt-account-id: <account id>
（不带 OpenAI-Beta；不带 x-codex-turn-state；不带 x-oai-attestation）
Body: 真实 Responses API（model、instructions=真实 Codex 系统提示、input=真实环境上下文、tools[]、
      stream:true、store:false、prompt_cache_key、client_metadata、reasoning 等）
```
读票：`x-codex-turn-state` 在 **HTTP 响应头**（不是 SSE body、不是 Set-Cookie）。

### 5.2 想「回带/续传 turn-state」的最小正确请求 —— WS 版
```
GET wss://chatgpt.com/backend-api/codex/responses   （Upgrade: websocket）
Authorization: Bearer <token>
OpenAI-Beta: responses_websockets=2026-02-06          ← 唯一正确的 beta 值
x-oai-attestation: <运行时计算>
x-responsesapi-include-timing-metrics: <值>
x-codex-turn-state: <上一次拿到的 780 字符票>            ← 仅此处回带
x-codex-beta-features: <已启用特性列表>
x-codex-routing-hint: <…>
x-codex-turn-metadata: {…}
x-codex-installation-id / x-codex-window-id
x-codex-ws-stream-request-start-ms / traceparent / tracestate
originator / User-Agent / version
```

### 5.3 sub4api 探针（`fireOpenAICodexTicketProbe`）差在哪
探针「HTTP POST 读 `resp.Header` 拿 `x-codex-turn-state`」**方向正确**（读票位置对）。真实差异（按可疑度）：

| # | 差异 | 探针 | 真实 0.163.0 | 级别 |
|---|---|---|---|---|
| 1 | **`OpenAI-Beta`** | `responses=experimental`（`:404`） | HTTP **不发**；只有 WS 发 `responses_websockets=2026-02-06` | 观察（硬错误） |
| 2 | `version` | 0.146.0（Astra≥0.153.4） | 0.163.0 | 观察 |
| 3 | originator/UA 与 body 自相矛盾 | 头 `codex-tui`，body `session_meta.originator="Codex Desktop"` | 头/body 同源 | 观察（二手，harvest-flow §2.3/§9） |
| 4 | `x-codex-turn-metadata` | 7 字段 | ~30 字段 | 观察 |
| 5 | body 无 `tools`、`instructions` 一句通用英文 | 无 tools | 真实回合几乎必带工具 | 观察 |
| 6 | 传输 | HTTP/1.1 + `Connection: close` + 每次新 TLS | H2 keep-alive / WS 长连接 | 观察 |
| 7 | **抓 cookie 当票** | 所有 Set-Cookie 都收 → `/responses` 只会给 `__cf_bm` = 假票 | `__oailb` 只从 `/models` 来 | 观察 |
| 8 | 缺 `x-codex-beta-features`/`x-codex-routing-hint`/`conversation_id` | 不发 | 真实会话按功能带 | 观察（次要） |

**最可能的失败面**（角 1 视角）：探针在 HTTP 上发了一个真实客户端**从不发**的 `OpenAI-Beta: responses=experimental`，加上 0.146.0 旧版本 + 「TUI 头 + Desktop body」矛盾 + 无 tools 的裸回合。若上游按「身份自洽 + 真实回合形状」才铸票，这个形状拿不到票（症状 = `missing_credentials` / `http_status`）。**读票位置（HTTP 响应头）本身没有错。**

---

## 6. 证据边界 / 未知

1. `x-codex-turn-state` 在 **WS 101 握手响应头**上是否也下发：客户端不落 WS 握手头日志，[未知]；静态读取点 A 证明客户端「会读」，但不能证明「服务端会发」。
2. 铸票准入（是否必须 attestation/真实工具/带历史/同 installation）：服务端策略，[未知]。
3. `x-codex-routing-hint` 值来源、`x-oai-attestation` 具体算法/是否可选：未定位到写入点，[未知]。
4. WS 子协议值：[未知]（未发现字面量）。
5. `__oailb`/`__cflb`/`__cf_bm` 精确长度与成对关系：`__cf_bm` 15 次 / `__oailb` 128 次为我本轮独立复现；`__oailb`=317 / `__cflb`=43 为二手（client-evidence §4）。
6. 「每 turn 首请求 139/141 带票、续请求 0/203」为二手（client-evidence §3.2），与我的 142/780 计数一致但未逐 turn 重算。

---

## 7. 复现（只读）

```powershell
cd D:\projects\sub4api\scratch\tmp\binre
python wire_scan.py            # 单遍扫协议 needle -> wire_scan_out.txt（本轮新增）
python region.py 0xf8bcd80 0xf8bd700   # SSE 事件名表
python x86.py 0x3d13e20 0x3d13e70      # HTTP/SSE 读票点 B
python x86.py 0x27d3c40 0x27d3cb0      # WS 握手读票点 A
python x86.py 0x7871b00 0x7871c90      # 回带(insert)点
python callers.py 0x78719e0 0x3d135c0  # 写点调用者(10 WS builders) / 读点B调用者(含纯HTTP)
python runtime_verify.py               # 日志复现：347 响应 / 142 票(780) / __cf_bm 15 / __oailb 128
```

---

## 8. 从角 1 出发的方案建议（供集体评审）

**最推荐（1 条）**：**放弃「WS 才能拿票」的旧结论，把打票探针改造成「HTTP 形状对齐」**——保持 HTTP POST + 读响应头拿 `x-codex-turn-state`（已证明方向正确），但修正身份/形状：(a) 删掉 `OpenAI-Beta: responses=experimental`（或仅对 WS 用 `responses_websockets=2026-02-06`）；(b) 版本对齐 0.163.0；(c) originator/UA 与 body `session_meta.originator` 同源（要么全 TUI、要么全 Desktop）；(d) `x-codex-turn-metadata` 补全 ~30 字段；(e) 给 body 加真实 tools。可行性高（只改探针构造，不碰架构），风险低（不引入 WS 复杂度），成本中等（需验证「带 tools 的真实回合」能否稳定铸票）。

**次选 A**：**A/B 对照**——同一账号同时发「当前形状」与「形状对齐」两路探针，对比 turn-state 是否下发，用 `codex_ticket_attempts` 的 `turn_state_present`/`reason_code` 定位到具体哪一关（http_status / missing_credentials / fingerprint_mismatch）。可行性高、成本低，但需要先确认无代理池轮换带来的出口 IP 干扰。

**次选 B**：**补 WS 探针**——若 A/B 证明「HTTP 形状对齐后仍不铸票」，再按 §5.2 的 WS 清单实现 `wss://` + `responses_websockets=2026-02-06` + `x-oai-attestation` 的握手拿票/续传。可行性中（WS 握手+attestation 需逆向其生成算法），风险中高（attestation 未还原、依赖未验证），成本高。仅在 A 失败后再投入。

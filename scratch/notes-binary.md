# codex.exe 逆向：turn-state / 传输 / 身份头（证据版）

- 目标：`C:\Users\zfx\AppData\Local\OpenAI\Codex\bin\a96ca4c1eee0cf35\codex.exe`
- 大小 336,011,568 B；SHA256 `E757246B4DE87359E660647FA9514955A0971FF6A30AFFDD8C145D1D5ECE7B84`（已用 `open_binary` 复核一致）
- PE x86-64，image_base `0x140000000`，节：`.text` RVA `0x1000`（~258MB）、`.rdata` RVA `0xf621000`（~65MB）、`.pdata` 977,462 个函数区间
- 内部版本线索：`codex-api\src\...`、`tui\src\...`、crates 路径（`reqwest-0.12.28`、`cookie_store-0.22.1`、`http-1.4.0`、`tokio-1.52.3`）；rustc `1.99.1-x86_64-pc-windows-msvc`
- 只读分析，未执行目标；文中 **未出现任何 token / cookie / turn-state 明文**（只有名字、长度、前缀判断）。

## 0. 方法与证据边界（先读）

REA MCP 可用，但**本机没有可用的原生分析 provider**，因此没有 REA Evidence ID：

- `binary_session`: ghidra `not_configured`（`GHIDRA_INSTALL_DIR` 未设置，`java` 也不存在）、hopper `unsupported_host`（macOS only）、ida/kuna `not_configured`，kuna 还明确 `pe` 格式不支持
- `open_binary` 成功绑定目标（sha256 相符），但 `binary_overview` / `search_strings` 均返回 `provider_unavailable`
- 本机也没有 `objdump/rizin/radare2/java/ghidra`；python 只有 numpy（无 capstone/pefile）

因此证据来自我自己写的纯静态工具（可复现，均在 `scratch/tmp/binre/`）：

| 脚本 | 作用 |
|---|---|
| `scan_kw.py` / `pe_dump.py` / `pe_extras.py` | ASCII+UTF16 关键字扫描、PE 节/导出/重定位/PDB 目录解析 |
| `disasm.py` / `x86.py` | 最小 x86-64 解码器（长度+常用助记符+RIP 相对解析） |
| `xref.py` / `scan3.py` | 全 `.text`（258MB）RIP 相对 disp32 精确 xref 扫描（numpy + 逐条回读校验，无假阳性） |
| `fninfo.py` / `fnstr.py` / `annot.py` | 函数→字符串/调用/IAT 指纹、带注解反汇编 |
| `callers.py` | `E8 rel32` 直接调用者扫描 |
| `region.py` / `g.py` | .rdata 字符串簇（RVA 级）打印 / 正则检索 |

坐标系：下文所有 `0x...` 地址一律为 **RVA**（相对 image base `0x140000000` 的偏移）；文件偏移 = RVA − 节 RVA + 节 rawptr（`.text` 起始 RVA `0x1000`/rawptr `0x400`，`.rdata` 起始 RVA `0xf621000`/rawptr `0xf61f800`）。可用 `address_to_file_offset` 或脚本里的 `rva2off()` 换算。

> 结论分级：**观察** = 二进制里直接看到的字符串/指令；**推断** = 由观察组合出的行为判断；**未知** = 二进制无法回答。

---

## 1. 结论表（六问速答）

| # | 问题 | 结论 | 级别 |
|---|---|---|---|
| 1 | `x-codex-turn-state` 从哪里读 | 仅两处：**(a) `codex-api/src/sse/responses.rs` 的流处理函数**（从"响应头 map"里取，读 map 里还有 `x-request-id`/`X-Models-Etag`/`openai-model`/`x-reasoning-included`/rate-limit 百分比）/ **(b) `codex-api/src/endpoint/responses_websocket/connector.rs` 的 WS 握手响应头**（6 个单态化副本）。**没有** "普通 HTTP 响应头→业务请求" 之外的第三条读取路径 | 观察 |
| 1b | 什么时候回带 | 只在**下一次 `responses_websocket` 握手请求头**里回带：由函数 `0x78719e0` 把 `x-codex-beta-features`(len 0x15) 与 `x-codex-turn-state`(len 0x12) 一起 insert 进 header 集合；调用者是 WS turn 构建函数族 `0x6e81c80/0x6e896a0/0x745ac00/0x74633f0/0x7d5c6f0/0x7d60560/0x8618ef0/0x872dc30/0xa8b04c0/0xa8b4780`。**纯 HTTP/SSE 请求路径没有回带点** | 观察（insert 指令）+ 推断（只在 WS 路径被调用） |
| 2 | 走 HTTP/SSE 还是 `responses_websocket` | 两者代码都在，**由 provider 配置决定**：`wire_api`（只允许 `"responses"`，`"chat"` 已废弃并报错）+ `supports_websockets` 布尔；另有 `websocket_connect_timeout_ms`、`codex.websocket.continuation` 特性开关与 `network.websocket_reachability` 探测。没有看到按账号类型/tier 选择的证据 | 观察（配置键/错误串）+ 推断（选择在 provider 层） |
| 3 | 发哪些 `x-codex-*` 身份头 | WS turn 构建函数可见：`x-codex-installation-id`、`x-codex-window-id`、`x-codex-turn-metadata`、`x-codex-parent-thread-id`、`x-codex-routing-hint`、`x-codex-guardian`、`x-codex-beta-features`、`x-codex-turn-state`、`x-codex-ws-stream-request-start-ms`；另有 `x-oai-attestation`、`OpenAI-Beta: responses_websockets=2026-02-06`、`x-responsesapi-include-timing-metrics`、`x-openai-internal-codex-responses-lite`、`x-openai-subagent`、`x-client-request-id`、`traceparent`、`originator`、`User-Agent`、`session_id`/`thread_id`/`turn_id`/`root_turn_id`/`parent_response_id` | 观察 |
| 3b | 服务端最可能校验什么 | `OpenAI-Beta: responses_websockets=2026-02-06`（WS 握手必须）、`Authorization`/`originator`/`User-Agent`/`session_id`、`x-oai-attestation`、`x-codex-installation-id`、`x-codex-turn-metadata` 内部一致性。**turn-state 本身应当只能来自服务端下发**，客户端不做自造 | 推断 |
| 4 | `__oailb` 是什么 | 二进制里 **0 次出现**（ASCII/UTF16/大小写/`oailb` 子串全无）。客户端从不读它、从不写它；`reqwest` 带 `cookie_store`（`cookie_store-0.22.1` + `inserting Set-Cookie '` / `unable to store Set-Cookie` 串）→ 任何 `Set-Cookie` 被 jar 自动存取。所以它是**服务端/边缘下发的路由 cookie**，客户端只是被动保存；与 turn-state 无代码耦合 | 观察（缺失）+ 推断（语义） |
| 5 | 还有哪些"可移植状态 / VIP 信号" | `x-codex-routing-hint`（客户端会发也会读）、`x-codex-beta-features`、`x-openai-internal-codex-responses-lite`、`x-openai-internal-codex-residency`、`x-oai-attestation`、`x-codex-credits-*`(响应头)、`x-codex-active-limit`、`x-codex-safety-buffering-*`、`x-code-mode-route`（含 `session routing token is too long`）、`installation_id`（在 turn-metadata JSON 里）、plan/limit：`plan_type`+`rate_limit_reached_type`(`primary_window`/`secondary_window`/`credits_depleted`…) | 观察 |
| 6 | 降级/替换/错误码 | `wire_api = "chat"` 不再支持（含修复提示）；模型开关特性 `fast_mode`/`ultrafast_mode`/`step_model_switching`；`x-codex-safety-buffering-enabled`+`x-codex-safety-buffering-faster-model`；错误枚举 `context_window_exceeded/session_budget_exceeded/usage_limit_exceeded/rate_limit_exceeded/flex_unavailable/server_overloaded/cyber_policy/bio_policy/misalignment_policy_violation/too_many_denials/http_connection_failed/response_stream_connection_failed`；`organization_spend_limit_exceeded`、`x-openai-authorization-error`、`This request has been flagged for possible cybersecurity risk.`、"Luna classification"（额度分类） | 观察 |

**最关键判断（回答"turn-state 现在什么条件下才下发"）**
> 按 0.163.x 客户端代码，`x-codex-turn-state` 是**服务端在 `responses_websocket` 通道上铸的票据**：客户端只在 WS 升级响应头（connector.rs）或流式响应的 metadata/headers map（sse/responses.rs）里"接住"它，并且**只在下一次 WS 握手时回带**。因此：
> 1) 用普通 `POST /responses`（HTTP/SSE）打票，客户端/上游都不会把它当成需要回带的东西——**这正是打票拿不到票的直接机制性原因**；
> 2) 拿票的探针必须走 **WS 升级**（`OpenAI-Beta: responses_websockets=2026-02-06` + `x-oai-attestation` 等），并且服务端要判定该请求"够格"（同一账号、同一 workspace/plan、attestation 通过）才铸；
> 3) 票据的"复用"语义是 **WS 会话续传（continuation）**，把它塞进 HTTP 业务请求头大概率不被认；它可能只对同一条 WS 连接的续传有效。

---

## 2. 问题 1：turn-state 的捕获与回带（详细证据）

### 2.1 字符串分布（9 份副本，RVA）

`x-codex-turn-state` 在 `.rdata` 出现 9 次（rustc 按 CGU 复制）：
`0xf7afd60, 0xf8be008, 0xfad8f98, 0xfb170f8, 0xfb6ed28, 0xfbc8898, 0xfcd3e20, 0xfd7d1e0, 0xfd91308`
（均在拼接字符串簇里，例如 `0xf7afd40` 处是 `x-reasoning-included`+`openai-model`+`x-codex-turn-state`+` failed to connect to websocket: ` 连排）

全 `.text` 精确 xref 扫描（回读校验）共 **13 个引用点 / 11 个函数**：

| RVA(引用点) | 函数 | 函数字符串指纹（关键） | 性质 |
|---|---|---|---|
| `0x27d3c73` | `0x27d2490`(11,217B) | `/responses`、`failed to configure websocket TLS`、`successfully connected to websocket`、`failed to build websocket request`、`codex-api\src\endpoint\responses_websocket\connector.rs` | **WS 握手响应头读取** |
| `0x89d678d` | `0x89d4830`(11,471B) | 同上（另一单态化） | WS 握手读取 |
| `0x9c16b07` | `0x9c142b0`(13,478B) | 同上 | WS 握手读取 |
| `0xa9f3448` | `0xa9f0180`(16,548B) | 同上 | WS 握手读取 |
| `0x816bfcd` | `0x816a940`(8,882B) | 同上 | WS 握手读取 |
| `0xa93cf20` | `0xa93a7d0`(13,317B) | 同上 | WS 握手读取 |
| `0x3d13e3f` | `0x3d135c0`(3,310B) | `codex-api\src\sse\responses.rs`、`x-request-id`、`X-Models-Etag`、`x-reasoning-included`、`openai-model`、`primary-/secondary-used-percent` | **SSE/HTTP 流处理里从头 map 读取** |
| `0x7871c60` | `0x78719e0`(823B) | `x-codex-beta-features` + `x-codex-turn-state` | **写（insert）** |
| `0x6e92210` / `0x7469478` / `0x7d68f3f` / `0xa8bd552` | `0x6e896a0`(44KB)/`0x74633f0`(44KB)/`0x7d65d70`(26KB)/`0xa8b9e60`(26KB) | `responses_websockets=2026-02-06`、`OpenAI-Beta`、`x-oai-attestation`、`x-codex-routing-hint`、`codex.websocket.continuation`、`restored_history`、`no_previous_request`、`x-codex-ws-stream-request-start-ms` | WS turn 构建（读缓存值→交给 builder） |

### 2.2 捕获点 A：WS 握手响应头（connector.rs）

`0x27d2490` 内（RVA 原文）：

```
0x27d3c5b  mov rcx, [rbp+0x1b00]
0x27d3c62  mov byte [rcx+0xf9b], 0x0          ; 复位某标志
0x27d3c70  lea rdx, "x-codex-turn-state"      ; 名字(18)
0x27d3c77  lea rcx, [rbp+0xcd0]               ; sret 输出位
0x27d3c7e  lea r9,  [rbp+0x18b0]              ; 响应头 map
0x27d3c85  mov r8,  0x12                      ; 长度 = 18
0x27d3c8b  call fn_0x1399bd0                  ; get_header(map, name) -> Option<..>
0x27d3c91  cmp [rbp+0xcd0], 0x0               ; Option tag
0x27d3c98  je  ...
```
同一函数此前（`0x27d3b20/0x27d3b52`）以同样方式读 `x-reasoning-included`、`openai-model`；随后是空白裁剪循环（`[rdx+rax]` 逐字节 `<0x20 且 !=0x09` 判断）——即把头部值 trim 后存起来。

### 2.3 捕获点 B：SSE/HTTP 流处理（sse/responses.rs）

`0x3d13e3c`：
```
0x3d13e3c  lea rdx, "x-codex-turn-state"
0x3d13e43  lea rcx, [rbp+0xa0]
0x3d13e4a  mov r8, 0x12
0x3d13e50  mov r9, [rbp+0xd88]        ; 头 map
0x3d13e57  call fn_0x3d404c0
0x3d13e5d  cmp [rbp+0xa0], 0x0
```
同函数还读 `openai-model`(`0x3d13bf5`)、`x-reasoning-included`(`0x3d13cc5`)、`X-Models-Etag`(`0x3d13b17`)、`x-request-id`(`0x3d13d0e`) 与 rate-limit 百分比串。
> 观察：这是"从 map 里按 HTTP 头名取值"。无法仅凭静态区分该 map 是 **HTTP 响应头** 还是 **`response.metadata` 事件的 headers 字段**（两者字段集合一致：`X-Models-Etag`/`x-request-id` 是响应头，rate-limit 百分比是 metadata 内容）。社区说法"只在 `response.metadata.headers` 里"与"HTTP 响应头"在此实现里是**同一段取值代码/同一张 map**，所以两者都可能命中——真正的限制是**服务端是否下发**，不是客户端读哪里。

### 2.4 回带（唯一写点）

`0x78719e0`（823B，双 header 构建器，含 `HeaderValue` 合法性校验：逐字节 `>0x1f` 或 `==0x09`）：
```
0x7871b0d  lea rdx, "x-codex-beta-features"      ; len r8=0x15
0x7871b22  call fn_0x79d9530                     ; insert(name, len, value)
...
0x7871c5d  lea rdx, "x-codex-turn-state"         ; len r8=0x12
0x7871c72  call fn_0x79d9530
```
调用者（10 处）全是上面那批 22–44KB 的 **WS turn 构建函数**，没有一个是纯 HTTP 请求构建器。
→ **推断**：客户端把 turn-state 当"WS 会话续传票据"，只在 WS 握手里回带。这也解释了为什么网关把它注入到 HTTP 业务请求可能毫无效果。

---

## 3. 问题 2：传输选择

观察到的配置与开关（`.rdata` 字符串）

- provider 配置键（`0xf7228da` 起）：`name base_url model_catalog_url env_key env_key_instructions experimental_bearer_token auth gateway_oauth aws wire_api capabilities query_params http_headers env_http_headers request_max_retries stream_max_retries stream_idle_timeout_ms ...`
- WS 相关键（`0xf718120` / `0xf7181a0` 簇）：`supports_websockets`、`websocket_connect_timeout_ms`、`connect_timeout_ms`、`idle_timeout_ms`、`stream_idle_timeout_ms`、`request_max_retries`、`stream_max_retries`、`experimental_bearer_token`
- `wire_api` 只接受 responses：`0xf731220` = `` `wire_api = "chat"` is no longer supported. ``，`0xf73124c` = `` How to fix: set `wire_api = "responses"` in your provider config. ``
- WS 门禁错误串：`0xf835fc8` = `Responses WebSocket is not enabled for the active provider`（唯一引用点 `0x3676f83`，函数 `0x3676be0`）
- 可达性探测（`0x3676be0`，9309B，调用者 `0x1a96d40`）字符串簇 `0xf835f00..0xf8361f5`：
  `model provider: ` / `provider name: ` / `wire API: ` / `supports websockets: ` / `network.websocket_reachability` / `connect timeout: `…` ms` / `auth mode: ` / `endpoint: ` / `endpoint build failed: ` / `provider setup failed: ` / `auth resolution failed: ` / `handshake result: HTTP ` / `reasoning header: ` / `server model present: ` / `immediate close code: ` / `immediate close reason: `
  → 客户端内置一个 **WS 可达性自检**，把 `handshake result / immediate close code` 上报为指标；`supports_websockets` 为假时直接产出 "not enabled for the active provider"。
- 传输遥测：`codex_http_client::transport`、`codex_api::responses_websocket_timing`（`0xf71a838`）
- WS 续传状态机：`codex.websocket.continuation`（特性开关名）+ 枚举字面量 `incremental` / `no_previous_request` / `restored_history` / `non-incremental request` / `previous_item_type` / `current_item_type` / `generation` / `warmup` / `after_prewarm`
- WS 握手 beta 值：`responses_websockets=2026-02-06`（7 份副本，唯一引用形态都是 `OpenAI-Beta` 相邻插入，见 §4）

选择逻辑（推断，按证据强度排序）
1. **provider 级**：`wire_api="responses"` 恒定；`supports_websockets` 决定 WS 是否可用（错误串直说 "for the active provider"）。
2. **特性开关**：`codex.websocket.continuation` 控制 continuation（warmup/restored_history 等）行为；`featureOverrides`/statsig 列表里还有 `api_key_cyber_access_programs`、`api_key_model_discovery`、`codex_apps_mcp_2026_07_28`、`remote_control`、`windows_sandbox_service` 等。
3. **失败回退**：turn 构建函数里有 `websocket connection is unavailable`（`0xfad8052`），说明 WS 不可用时回退（大概率回 HTTP/SSE）。
4. 没有找到"按账号类型/套餐选 transport"的证据；`plan_type` 只出现在限流/额度语义里。

---

## 4. 问题 3：请求头与身份构造

### 4.1 一次 /responses 请求会出现的头（观察，来自 WS turn 构建函数族）

标准/传输层：`Authorization`、`originator`、`User-Agent`、`session_id`、`thread_id`、`turn_id`、`root_turn_id`、`parent_response_id`、`traceparent`、`x-client-request-id`

Codex 私有：
| 头 | 证据 RVA / 说明 |
|---|---|
| `OpenAI-Beta: responses_websockets=2026-02-06` | `0xf8360e8` 等 7 副本；插入点 `0x3678115`(probe) / `0x6e8f2b5` / `0x7466de6` / `0x7d5d706` / `0x873080c` / `0x97b380e` / `0xa8b1a59` |
| `x-oai-attestation` | `0xfadadd8` 等 5 副本；12 个引用点（`0x6e82b6d`…） |
| `x-codex-routing-hint` | `0xfad8e5a` 等 5 副本；10 个引用点，均为 WS turn 构建函数 |
| `x-codex-beta-features` | `0xfb3b2a8`；与 turn-state 同一 builder `0x78719e0` 插入 |
| `x-codex-turn-state` | 见 §2.4 |
| `x-codex-turn-metadata` | `0xfb23b50` 等；builder `0x78675e0`（903B，match 分派）；调用者 `0x750cd50/0x7da75a0/0x7fd3490/0x8681170/0x993e610` |
| `x-codex-installation-id` | `0xfa1b188`；remote-control WS 里插入（`0x53f714f`），turn-metadata JSON 里也有 `installation_id` 键 |
| `x-codex-window-id` | `0xfcc19a8`；引用 `0x9af4691`（函数 `0x9aee2e0`，WS 会话/头构建，30KB） |
| `x-codex-parent-thread-id` | `0xfcc19d3` 附近；`x-openai-subagent` 同簇 |
| `x-codex-guardian` | `0xfb3b2a8` 簇；guardian(审核) 相关 |
| `x-codex-ws-stream-request-start-ms` | `0xfcc0ab4` |
| `x-responsesapi-include-timing-metrics` | `0xfad95d8` 簇 |
| `x-openai-internal-codex-responses-lite` | `0xfb3b2a8` 簇（"responses-lite" 内部通道标记） |
| `x-openai-internal-codex-residency` | `0xfbdbaeb`；引用 `0x9fb76e8`（UA/头构造） |
| `x-openai-subagent` / `x-client-request-id` | 多处簇内 |
| `x-codex-websocket-max-unfragmented-message-bytes` | `0xf738c48`（exec-server WS，不是 /responses） |

### 4.1b HTTP 与 WS 的头集合差异（观察+推断）

`x-codex-turn-metadata` 的 builder `0x78675e0` 的 5 个调用者是 **codex-core 的回合执行层**（`0x993e610` 30KB / `0x750cd50` 19KB / `0x7da75a0` 5.8KB 等，字符串指纹是 `record_completed_response_item`、`failed to flush rollout…`、`Interrupt hook`、`bypassPermissions/PreToolUse/PostToolUse/PreCompact/SessionStart` 等 hook/rollout 逻辑，与 websocket 无关）。
→ **推断**：header map 在 core 层构建一次，再交给具体 transport；因此
- **HTTP/SSE 请求**：会带 `originator`/`User-Agent`/`session_id`/`thread_id`/`x-codex-turn-metadata`(+`x-codex-installation-id`/`x-codex-window-id` 等)，**但不带** `x-codex-turn-state`（唯一写点 `0x78719e0` 只在 WS builder 里被调用）；
- **WS 握手**：在上述基础上再加 `OpenAI-Beta: responses_websockets=2026-02-06`、`x-oai-attestation`、`x-codex-routing-hint`、`x-codex-beta-features`、`x-codex-turn-state`、`x-codex-ws-stream-request-start-ms`、`x-openai-internal-codex-responses-lite`、`traceparent`。

### 4.2 `x-codex-turn-metadata` 的字段集合（观察）

字段名常量簇在 `0xf722ec7`（608B 连续区）与 `0xfcc19be`：
`installation_id`, `session_id`, `thread_id`, `agent_name`, `turn_id`, `window_id`, `window_number`, `context_window_id`,
`request_kind`, `code_mode_tool_names`, `tool_namespaces_info`, `turn_started_at_unix_ms`, `history_ingest_requested`, `analytics_enabled`, `history_initialization`,
`forked_from_thread_id`, `forked_from_ordinal_exclusive`, `parent_thread_id`, `parent_turn_id`, `root_turn_id`, `subagent_kind`, `thread_source`, `turn_trigger`, `sandbox`, `sandbox_mode`, `auto_review_enabled`, `node_repl_auto_review_required`, `node_repl_disabled`, `workspaces`
→ 值就是一个 JSON 对象（名字成簇 = serde/Serialize 字段表），是**客户端身份+会话上下文的自证**，很可能是服务端判断"这请求是不是原生 Codex 客户端"的关键之一。

### 4.3 originator / User-Agent（观察）

常量簇 `0xfd09f60..0xfd0a220`：
- `CODEX_INTERNAL_ORIGINATOR_OVERRIDE`（**可用环境变量覆盖 originator**）、`CODEX_SANDBOX`
- `codex_cli_rs`、`codex-tui`、`codex_vscode`、`Codex `
- `x-openai-internal-codex-residency`
- "Sanitized Codex user agent because provided suffix contained invalid header characters"
- "Falling back to default Codex originator because base user agent string is invalid"
- "Falling back to base Codex user agent because provided suffix could not be sanitized"
- `#Unable to turn originator override ` + ` into header value: `
- `ignoring invalid thread originator header value: `
- UA 构造相关函数：`0x7dcee30`(codex_cli_rs)、`0x9fb7150`(UA/`x-openai-internal-codex-residency`)、`0x9fb9870`(thread originator 校验)、`0xb2a7aa0`

> 与团队线索 2 相关：`originator: codex-tui/0.146.0` 这种组合**在客户端里是合法可构造的**（originator 可被环境变量覆盖，UA 由 base+suffix 拼），所以"Desktop 的 body + tui 的 header"客户端层面不会自相矛盾；真正可能被服务端比对的是 `x-codex-turn-metadata`（含 `thread_source`/`request_kind`/`turn_trigger`/`sandbox`）与 UA/originator 的一致性。

### 4.4 服务端最可能校验什么（推断）

1. `Authorization` + 账号/workspace 一致（`chatgpt-account-id`/`account_id` 参与；有 "ChatGPT account ID not available" / "ChatGPT backend requests require Codex backend auth"）
2. **`OpenAI-Beta: responses_websockets=2026-02-06`**（WS 握手门）
3. **`x-oai-attestation`**（客户端会带；缺失很可能导致不铸票）
4. `x-codex-turn-metadata` 字段自洽（installation_id/session_id/thread_id/turn_id 链条）
5. `originator` + `User-Agent` 与 `thread_source` 一致
6. `x-codex-installation-id`（远端控制 WS 与 turn-metadata 共用）

---

## 5. 问题 4：`__oailb` 与 Cookie

- **`__oailb` 在二进制中 0 次出现**：`scan_kw.py` 对 ASCII/UTF-16LE 全文件扫描，`__oailb`/`oailb`/`__oai` 全 0 命中。
- 客户端带完整 cookie 栈：`cookie-0.18.1`、`cookie_store-0.22.1`、`reqwest-0.12.28/src/cookie.rs`，字符串 `inserting secure cookie '`、`inserting Set-Cookie '`、`unable to store Set-Cookie: `（`0x1043ba59..0x1043bb1e` 文件偏移区），`SetCookie`/`ReadCookie` 导出符号。
- 推断：`__oailb` 是**服务端/Cloudflare 边缘下发的路由/粘性 cookie**（名字像 oai + lb=load balancer）；客户端不解析其语义，只是被 reqwest cookie jar 自动保存并在同域后续请求自动回带。它与 turn-state **没有代码层关系**，不能作为"VIP 票"的证据。
- 是否存在第二个路由 cookie：**二进制无法回答**（客户端不认识任何具体 cookie 名）。HTTP 200 是否仍 `Set-Cookie` 也是服务端行为，静态分析不可判定（未知）。
- 对网关的实操含义：如果目标是"模拟官方客户端"，cookie 只需在**同一 reqwest 客户端实例（同一 jar/同一连接池）**内自动回带即可；把 cookie 抽出来全站共享很可能反而破坏一致性（cookie 通常绑定账号+IP+边缘节点）。

---

## 6. 问题 5：其它可移植/可复用的状态与 VIP 信号

| 信号 | 位置/证据 | 说明 |
|---|---|---|
| `x-codex-routing-hint` | 5 副本，10 引用点（WS turn 构建）；紧随 `/responses`(`api.path` + `/responses`) | 路由提示，客户端**会发**；值来源未见（推断来自服务端上次响应或本地策略） |
| `x-codex-beta-features` | `0xfb3b2a8` 簇；builder `0x78719e0` | 与 turn-state 一起发；应为"已启用 beta 特性列表" |
| `x-openai-internal-codex-responses-lite` | `0xfb3b2a8` 簇；引用点含 `0x9cca2e0`、0x6e81c80 等 | "responses-lite" 内部通道（VIP/实验通道信号之一） |
| `x-openai-internal-codex-residency` | `0xfbdbaeb`；`0x9fb76e8` | 数据驻留区域内部头 |
| `x-oai-attestation` | 5 副本，12 引用点 | 客户端证明（attestation），**打票请求最可能缺的就是它** |
| `x-codex-guardian` / `x-codex-guardian_classifier` | `0xfb3b2a8` / `0xfcc1960` | 审核分类器通道 |
| `x-code-mode-route` | `0xfa7ca85` 等 4 副本；7 个引用点(`0x5f07c90/0x5f07e30/0x5f07fa0/0x5f08150/0x5f082f0` 小函数 + `0x5f36f20/0x5f5dbc0`)；邻串 `session routing token is too long` | 另一条"路由 token"机制（code-mode 宿主），可留意但非 /responses |
| `x-codex-installation-id` | `0xfa1b188`，插入点 `0x53f714f` | 安装标识（可复用性高，但要与 turn-metadata 里 `installation_id` 一致） |
| 响应侧额度头 | `x-codex-active-limit`(0xf8bd218)、`x-codex-credits-has-credits`/`-unlimited`/`-balance`(0xf8bd726)、`x-codex-rate-limit-reached-type`(0xf8bd82b)、`x-codex-safety-buffering-enabled`/`-faster-model`(0xf8c2408)、`x-codex-promo-message`、`x-codex-imagegen-request-id`、`x-codex-server-id`/`-name`/`-protocol-version`/`-subscribe-cursor` | 服务端下发的额度/计划/促销信号，**只读**；客户端据此调整行为（不是它自己伪造） |
| plan/限流模型 | `0xf727078` = `plan_type rate_limit spend_control rate_limit_reached_type`；`0xf780ea0` = `allowed limit_reached primary_window secondary_window rate_limit_reached workspace_owner_credits_depleted … used_percent`；计划枚举 `free/go/plus/pro/prolite/promax/team/self_serve_business_prolite/self_serve_business_usage_based/business/enterprise/enterprise_cbp_automation/enterprise_cbp_usage_based` | 说明服务端确实按 plan/window 计费与判定 |
| "Luna classification" | `0xfcc0c08` = `account changed during Luna classification`；`0xfb348d8` 起 `standard_user_acl`、`…grams are disabled for this API-key session`、`Cyber access programs are disabled for this API-key session` | 服务端侧存在账号分类（Luna），API-key 会话限制访问 program |

---

## 7. 问题 6：降级 / 模型替换 / 错误码

- **wire API 降级**：`wire_api = "chat"` 已不再支持，直接报错并提示改成 `"responses"`（`0xf731220/0xf73124c`）。
- **模型切换 / 加速档**：特性与配置名 `fast_mode`、`ultrafast_mode`、`step_model_switching`（`0xf6ed1b0` 附近的特性名簇；`0xf7b3121` 附近重复出现）；这些与 "降智" 体验直接相关。
- **安全缓冲降级**：`x-codex-safety-buffering-enabled`、`x-codex-safety-buffering-faster-model`（响应头，`0xf8c2408`），即服务端可把请求切到"faster model"。
- **错误枚举（`CodexErrorInfo`）**：`0xf7e55b0` = `context_window_exceeded session_budget_exceeded usage_limit_exceeded rate_limit_exceeded flex_unavailable server_overloaded cyber_policy bio_policy misalignment_policy_violation too_many_denials http_connection_failed response_stream_connection_failed internal…`；camelCase 版本在 `0xf7e9ff8`。
- **HTTP 层错误/授权**：`x-openai-authorization-error`(`0xf8bd828`)、`organization_spend_limit_exceeded`/`organization_usage_limit_exceeded`(`0xf8be828` 区)、`x-error-json`(`0xf8bedd7`)、`MisalignmentErrorDetails`+`This request was blocked due to a misalignment policy violation.`(`0xf8bcc10` 区)、`missing`/`This request has been flagged for possible cybersecurity risk.`(`0xf8c24c8` 区)。
- **流中断**：`stream closed before response.completed`（多处）、`response stream dropped before provider terminal event`、`websocket closed by server before response.completed`、`unexpected binary websocket event`、`failed to parse websocket event: `、`response.interrupt`/`discard_partial_items`。
- **WS 侧**：`websocket connection is unavailable`、`invalid WebSocket URL`、`failed to configure websocket TLS`、`Responses WebSocket closed immediately`（`0xf8348e8` 区）。
- **压缩/上下文**：`response.completed usage.codex_rollout_budget_units must be finite and non-negative`、`model_auto_compact_token_limit`、`auto_compact_fallback_prompt`、`remote compaction v2`。
- **SSE 事件名**：`response.created / in_progress / output_item.* / content_part.* / output_text.delta / reasoning* / metadata / completed / incomplete / failed / interrupt`（`0xf8bcd80`、`0xfcc0190`、`0xfbb7586` 等簇），`response.metadata` 出现在其中。

---

## 8. 对"打票失败"的可执行判断（把上面串起来）

1. **拿票必须走 WS 升级**，而不是普通 `POST /responses`：
   - 需要 `OpenAI-Beta: responses_websockets=2026-02-06`（+ `x-oai-attestation` 等），否则服务端不会铸 `x-codex-turn-state`。
   - 客户端的 WS 门禁是 provider 配置 `supports_websockets`（"Responses WebSocket is not enabled for the active provider"），所以打票必须显式按 WS 方式建连（用 `wss://` + 正确的 beta 头）。
2. **回带也只在 WS 握手里**（builder `0x78719e0` 只被 WS turn 构建器调用）。把票塞进 HTTP 业务请求头，官方客户端自己都不会这么做 → 服务端大概率忽略。
3. **票有会话/账号/installation 绑定的可能**（turn-metadata 里有 `installation_id/session_id/thread_id/turn_id/root_turn_id`），"全站共享一张票"本身就与客户端的用法不符。
4. 如果只用 HTTP，最可能命中的是 `response.metadata` 事件里的 headers map（`sse/responses.rs` 的取值路径），但服务端当前是否在 HTTP 上还下发 **静态不可判定**——需要抓包确认（这是唯一能下定论的方式）。
5. `__oailb` 不是客户端语义票据；不要把它当作 VIP 证据，也不要跨账号共享。

---

## 9. 未知项 / 需要动态或服务端证据

1. 服务端在 **HTTP 200 / SSE `response.metadata`** 上到底还发不发 `x-codex-turn-state`、还发不发 `Set-Cookie: __oailb`（静态只能证明客户端"会读"，不能证明"服务端会给"）。
2. 铸票的**准入条件**（是否必须 attestation、是否必须同 installation_id、是否必须 WS 会话内首帧、是否受 plan/workspace 限制）——属服务端策略。
3. `x-codex-routing-hint` 的**值来源**（本地生成还是上次响应回传）未定位到写入点。
4. WS "continuation" 的完整状态机（`incremental/no_previous_request/restored_history/warmup/after_prewarm` 的转移条件）只看到字面量与特性开关名，未逐分支还原。
5. `0x78719e0` 的入参（turn-state 值来自哪个缓存字段）只追到"由 WS turn 构建器提供"，未把 `struct 偏移 0xf9b` 等字段名还原。
6. 若能提供 **Ghidra 12.1.x + JDK**（设 `GHIDRA_INSTALL_DIR`），可把上面 11 个函数反编译成伪代码，把"推断"升级为"观察"；当前环境不具备。

---

## 10. 复现步骤（只读）

```powershell
cd D:\projects\sub4api\scratch\tmp\binre
python scan_kw.py                      # 关键字/字符串位置
python scan3.py                        # 全 .text xref 扫描 -> xref_map2.json
python fnstr.py 0x27d2490              # WS connector 里 turn-state 读取的字符串指纹
python x86.py 0x27d3c40 0x27d3cc0      # 直接看读取指令
python x86.py 0x78719e0 0x7871c90      # 直接看回带(insert)指令
python region.py 0xf722ec7 0xf723260   # x-codex-turn-metadata 字段表
python region.py 0xf835f00 0xf836200   # WS 可达性/门禁字符串簇
```

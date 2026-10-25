# sub4api 网关「打票替代方案」决策报告（v2）

日期：2026-10-24（修补轮：同日，发布前全量亲核）｜ 决策问题：**用什么替代现行的"打票探针"（伪造 HTTP POST /responses 请求诱使上游铸 `x-codex-turn-state` 票）**

**证据基座**
- 三路侦查（WS会话 / 粘性机制 / 新面开关）共 **33 条发现**，每条均经独立复核员逐行亲核：**28 条通过**（holds=true），**5 条未通过**（见 §4，均不影响本报告推荐）。
- 官方客户端源码：`scratch/src/codex-rust-v0.164.0-alpha.11.1/codex-rs/`（下文 codex-rs 相对路径均指此树）。
- 网关源码：`backend/internal/service/`。
- 背景报告（本轮全文亲读）：`scratch/REPORT-codex-bug-disclosure.md`（机制地图）、`scratch/REPORT-codex-solution-final.md`（旧结论）。
- **修补轮全量亲核**（本会话逐条重验，覆盖上一轮汇总者 6 处抽查的超集；全部命令与 Read 均在本会话执行）：
  1. Read `backend/internal/service/openai_ws_forwarder_v2.go:55-104`：确认 `:69` 构建 payload 后 `:77` 仅 `setOpenAIWSTurnMetadata`，`:74` 读出的 turnState **从未写入 payload/client_metadata**，仅经 `:158-165` 传入 `buildOpenAIWSHeadersWithTicket`（只进握手头）；`:138-140` 复用 stateStore 存票（跨 turn 陈旧票路径）、`:341-349` dial 时握手头捕获 → BindSessionTurnState；`:923` BindResponseConn 续链。
  2. Read `backend/internal/service/openai_ws_forwarder_support.go:1-120`：确认 `:17-19` 读 `PrewarmGenerateEnabled`、`:21-23` `performOpenAIWSGeneratePrewarm` 已实现、`:78` `prewarmPayload["generate"]=false`、`:54-70` 幂等跳过（有 previous_response_id / 已预热 / 工具续传则跳过）、`:96-111` 预热读循环带 close_status/close_reason 日志。
  3. Read `backend/internal/service/openai_gateway_forward.go:1520-1560`：确认跨账号票剥离守卫 `guardOpenAICodexTurnStateEcho` 在 `:1531`（发现集引 1519-1522 略偏，机制属实）。
  4. grep `PrewarmGenerateEnabled`：确认 `backend/internal/config/config.go:1319-1320` 定义、注释"默认 false"；grep `openai_codex_ticket` 确认 `domain_constants.go:723-730` 已有热更开关：`openai_codex_ticket_enabled`（探针总开关，"关闭：不打票、不注入"）、`openai_codex_ticket_allow_without_ticket`（无票放行开关，P0 第 4 件可直接用配置实现）。
  5. grep `x-codex-turn-state`（backend/internal/service，排除测试）：22 处命中全部为 HTTP 头操作/注释/常量，**无任何 client_metadata 写入**（client_metadata×turn-state 组合 grep 0 命中）——与复核结论一致。
  6. grep `response.metadata`（openai_ws_forwarder_v2.go + openai_ws_forwarder_support.go）：**0 命中**，确认 v2 路径无事件级票捕获。全 service 目录另仅 2 处命中且均与票捕获无关：`openai_gateway_passthrough.go:1097`（事件种类白名单，判"不提交模型输出"）、`openai_gateway_response_handling.go:1960`（Responses API 字段名清单）。
  7. **官方契约锚点逐条亲核**（codex-rs 树）：`core/src/client.rs:291-296`（契约注释原文，含 "violates the client/server contract and can cause routing bugs"）、`:311`（`turn_state: Arc<OnceLock<String>>`）、`:694-707`（每 turn 新建 OnceLock，`:705`）、`:1725-1729`（换主清票）、`:2117-2119`（WS 帧级回带 client_metadata）、`:1421-1428`（握手头 `/*turn_state*/ None`，`:1427`）、`:2187`（`generate: if warmup { Some(false) }`）+ `:2152-2154`（"connection setup, not a model inference attempt"注释）、`:2495-2513`（HTTP 回带头）；三处握手捕获调用方全传 None（`client.rs:1360`、`responses_websocket.rs:477`、guardian `connection_pool.rs:496`）→ 101 握手头捕获为死代码；事件级捕获 `responses_websocket.rs:849-853`、HTTP 头捕获 `sse/responses.rs:66-72`、事件 JSON 头解析 `sse/responses.rs:211-219, 281-290`（大小写不敏感）。
  8. **官方测试锚点逐条亲核**：`turn_state.rs:92-204` WS 版断言序列逐字 `[null,null,"ts-1",null]`（`:147-153`）；`:207-262` 断言 `[null,"ts-1","ts-1"]`（同 turn 第 2 票 ts-2 被忽略，即 B1）；`agent_websocket.rs:560-645` 预热 `generate=false` + `response.metadata` 铸票 + 首轮 `client_metadata["x-codex-turn-state"]==warmup_turn_state`（`:639-642`）+ tier 变化仅降级续传；`client_websockets.rs:1006+`（换账号重连清票）、`:1232+`（预热复用 advisory model/tier）；`startup_prewarm.rs:358-396`（try_join! 并行握手+工具捕获）、`:436-448`（prewarm_websocket）；`tasks/regular.rs:77-79` + `session/turn.rs:194-195`（预热会话被首轮真实 turn 复用）；`client.rs:2344-2346`（预热幂等检查）。
  9. **P1-P4/判死项锚点抽核**：`backend-client/src/client.rs:400-410`（GET /wham/accounts/check 两种路径风格）、`types.rs:142-143`（origin/override 字段）、`model-provider/src/workspace_routing.rs:89-103`（ResponsesConnectionKey 三元组）、`client.rs:1304-1312`（带 routing 头拒绝重定向）、login `workspace_routing.rs:80-88`（"account changed during workspace routing discovery"）；`client.rs:680-681`（"ChatGPT derives cache affinity from the Responses session-id header"注释）、`requests/headers.rs:5-14`（build_session_headers）、`app-server-client/src/lib.rs:257`（`request_attestation: false`）；`chatgpt_cloudflare_cookies.rs:17-49`（allowlist 注释）、`websocket-client/src/lib.rs:172,191-194`（HTTP/WS 共用 jar）；`client.rs:163,1281-1297`（routing-hint `model={model};tier={tier}`）、`doctor.rs:2377-2396`（探针结果语义）；guardian `connection_pool.rs:400-432`（classifier/responses-lite 头）+ `:552`（turn_state None）；`features/src/lib.rs:2077-2092`（PreventIdleSleep 第 4 个 Experimental key）；`client.rs:1225-1235`（CodexResponsesHeaders 三重门控）+ `responses_headers.rs:1-9`；`client.rs:2504`（beta-features 无条件注入）；`codex-api/src/common.rs:31-47`（AccessPrograms/Daybreak）。
  10. **运行时证据核对**：Read `scratch/notes-client-runtime.md`（`:42,:44,:104,:111`）确认 142 张 780 字符票、首请求 139/141（98.6%）带票、203 条续请求 0 带票、§3.2 即"下发条件（核心）"。
  11. **行号修正**（修补轮发现并已改正正文）：桥调用 `forwardOpenAIWSV2` 实际在 `openai_gateway_forward.go:929`（旧稿引 921）；WS 分支条件在 `:819`（旧稿引 809）；`previous_response_not_found` 恢复在 `:971-973`（旧稿引 926-929）；指纹改写入口 `applyStagedCodexFingerprintClientMetadata` 在 `openai_codex_fingerprint.go:56`（旧稿只引改写核心 414-434）；v2 读循环 `for {` 起点 `:588`。

**硬约束（贯穿全文）**：方案不得增加账号安全风险（不封号）；上游可见行为变化逐项标注+灰度建议；**跨账号共享票类方案直接判死**。

---

## 1. 执行摘要

**替代方案是什么**：官方客户端从不"打票"。三路源码证据收敛于同一结论——**票必须由上游铸造，且上游按"每 turn 首个非增量请求"自然发票**（运行时证据：141 个 turn 中 139 个首条 HTTP 响应带票 = 98.6%，203 条续请求 0 条带票，`scratch/notes-client-runtime.md §3.2`，复核员亲开文件核对一致）。客户端侧只做两件事：
1. **事件级捕获**：WS 上从 `response.metadata` 事件的 headers 取票（`codex-api/src/endpoint/responses_websocket.rs:849-853` + `codex-api/src/sse/responses.rs:211-219,281-290`）；HTTP 上从响应头取（`codex-api/src/sse/responses.rs:66-72`）。101 握手头捕获在 0.164 是**死代码**（所有调用方传 `turn_state=None`，`core/src/client.rs:1360`、`responses_websocket.rs:477`、guardian `connection_pool.rs:496`）。
2. **契约通道回带**：WS 上写进**每条 response.create 帧的请求体** `client_metadata["x-codex-turn-state"]`（`core/src/client.rs:2117-2119`）；HTTP 上写请求头（`core/src/client.rs:2495-2513`）。**官方从不在 WS 握手头回带票**（`client.rs:1421-1428` 显式传 None）——这正是网关当前做反的地方。

官方还有"零成本铸票"形态：**线程启动预热**——后台建连 + 发一条 `generate=false` 的 WS response.create（"connection setup, not an inference request"，无生成 token、不进 rollout trace），预热响应 `response.metadata` 里铸出的票被第一轮真实请求原样回放（官方测试 `core/tests/suite/agent_websocket.rs:564-573, 611-612, 639-642` 逐项断言）。

**推荐**：**P0「官方契约对齐桥接」**——在现役"下游 HTTP → 上游 WS"桥（`forwardOpenAIWSV2`，`backend/internal/service/openai_gateway_forward.go:929`，WS 分支条件 `:819`）上补齐 4 个小件：
① **帧级回带**：把票写进每帧 `client_metadata["x-codex-turn-state"]`（现状只写握手头、且仅 dial 时——本轮亲核确认）；
② **事件级捕获**：从 `response.metadata` 事件 headers 取票（现状只读 101 握手头——官方恰恰不依赖它；本轮 grep 确认 v2 路径 0 处事件解析）；
③ **打开已实现的预热**：`prewarm_generate_enabled`（默认 false，`backend/internal/config/config.go:1319-1320`；`performOpenAIWSGeneratePrewarm` 代码已就绪且幂等语义与官方一致）；
④ **票生命周期对齐**：per-turn 语义（修正复用连接把陈旧握手票当本 turn 票的现状），跨账号剥离守卫保留，用现成配置 `openai_codex_ticket_allow_without_ticket` 降级 fail_closed。
验证通过后，用热更开关 `openai_codex_ticket_enabled=false` 下线打票探针。

**为什么满足"不增加账号风险"**：四件中三件让网关形状**更贴近**官方客户端（回带通道对齐、事件捕获纯观测、无票放行=官方每 turn 首请求本来无票）；唯一新增上游可见行为是预热帧，与官方客户端线程启动行为**同款同语义**（每连接一次、generate=false、无生成 token）。残余不确定性只有一个：**网关出站形状（非官方 UA/指纹）下，上游是否对 prewarm 响应铸票**——服务端策略，静态不可证，需一次单账号实测后灰度（§3.1.5）。

**直接判死**（源码否决 + 硬约束否决）：伪造票（全树 3 处写入点全是接上游值，无本地铸造路径）；**跨账号共享票（硬约束判死**，既有守卫 `guardOpenAICodexTurnStateEcho` 保留）；跨 turn 票硬塞（现状超契约行为，官方契约注释明言"violates the client/server contract and can cause routing bugs"，`core/src/client.rs:291-296`）；guardian 分类器免票通道（主模型不可用）；API-key 整体豁免（换计费世界非绕门禁）；Etag 上行/旁路新 headers/attestation 伪造/daybreak 当免票（非请求侧通道或非免票，见 §3.6）。

---

## 2. 方案总排序表

| 排序 | 方案 | 可行性 | 账号风险 | 实现工作量 | 定位 |
|---|---|---|---|---|---|
| **P0** | **官方契约对齐桥接**（预热铸票 + 帧级回带 + 事件级捕获 + 生命周期对齐） | **高**：桥与预热代码已存在，差 4 小件（本轮亲核确认）；残余不确定性仅 1 项需实测 | **低**：3/4 件让网关更贴近官方形状；唯一新增可见行为（预热帧）为官方同款 | **中**：2 个代码注入点 + 1 个事件解析 + 1 个生命周期修正；`allow_without_ticket` 与探针下线均为现成热更配置 | **打票探针的直接替代，主方案** |
| P1 | 工作区路由发现（GET /wham/accounts/check） | 高：普通认证 GET，一次拿全账号 origin+区域约束映射 | 低：官方路由发现原样；按凭据周期调用（非高频） | 小-中：新增一个账号级发现调用+缓存；网关侧现状未核对（§5） | 账号级粘路由的官方形态，补充项 |
| P2 | 缓存亲和 + 会话身份形状对齐（prompt_cache_key/session-id/身份头组/turn-metadata/不带 attestation） | 高：全部明文契约字段 | 低-无：既有请求字段取值对齐，无新增行为类型 | 小：网关已有指纹改写骨架（`applyStagedCodexFingerprintClientMetadata`） | 形状对齐，配合 P0 |
| P3 | 官方基础设施 cookie jar 对齐（__oailb 等，per-account） | 高：allowlist 与域规则源码明文 | 低：被动回带，官方单用户即此形态 | 小-中：连接池 per-account jar + HTTP/WS 共用 | 形状对齐，配合 P0/P1 |
| P4 | routing-hint 构造 + doctor 探针对照 | 高：本地拼装，格式源码明文 | 无 | 小 | 形状对齐 / 上游 WS 诊断参考 |
| ✗ | 伪造票 | **无**（feasibility=none） | — | — | 判死：客户端无铸造路径（§3.6-D1） |
| ✗ | **跨账号共享票** | — | **高（硬约束判死）** | — | 判死（§3.6-D2） |
| ✗ | 跨 turn 票硬塞（现状行为） | — | 中-高 | — | 判死：超契约，P0④中修正（§3.6-D3） |
| ✗ | guardian 免票通道 / API-key 豁免 / Etag 上行 / 旁路 headers / attestation 伪造 / daybreak 免票 | 低-无 | 高或无关 | — | 判死（§3.6-D4~D9） |

> 判死项不参与排序，单列于 §3.6。

---

## 3. 方案详解

### 3.1 P0 官方契约对齐桥接（推荐第一）

#### 3.1.1 机制原理

票（`x-codex-turn-state`）是 turn 级粘性路由令牌，**只能由上游铸造**：客户端全树 `turn_state.set(` 恰 3 处（HTTP 响应头 / WS response.metadata 事件头 / WS 握手头[死代码]），全部是接住上游值；无任何本地构造路径（`gAAAAA` 等铸造常量 0 命中）——「铸票三通道」发现（holds=true，复核员重跑 grep 确认）。

官方行为模型（多条发现交叉印证，全部 holds=true）：
- **每 turn 一张票**：turn 级 `Arc<OnceLock<String>>`，首请求无票 → 上游在首个非增量请求的响应上铸票 → 同 turn 后续请求回带 → 下一 turn 归零（`core/src/client.rs:311, 694-707`；官方测试 `core/tests/suite/turn_state.rs:92-204` 断言序列 `[null,null,"ts-1",null]`）；同 turn 内上游再发票被静默忽略（`turn_state.rs:207-262` 断言 `[null,"ts-1","ts-1"]`，即旧报告 B1）。
- **预热铸票**：线程启动后台预热（`codex-rs/core/src/session/startup_prewarm.rs:358-396` try_join! 并行握手+工具捕获；`:436-448` 发预热帧），`generate=false`（`core/src/client.rs:2187`），预热流与首个 regular turn **共用同一 ModelClientSession**（`core/src/tasks/regular.rs:78` + `core/src/session/turn.rs:194-195`），因此预热响应铸的票被首轮真实请求回放（官方测试 `core/tests/suite/agent_websocket.rs:639-642` 断言 `first_turn["client_metadata"]["x-codex-turn-state"]==warmup_turn_state`）。上游成本：1 次 WS dial + 1 个无生成 token 的往返。
- **WS 回带只在请求体**：每条 response.create 的 `client_metadata["x-codex-turn-state"]`（`core/src/client.rs:2117-2119`）；握手头从不带票（`client.rs:1421-1428` 传 None）。捕获只在事件流（`responses_websocket.rs:849-853`，仅认 `response.metadata` 事件 headers）。
- **增量续传**：同 session 下一条请求若 11 项字段（model/tools/tool_choice/parallel_tool_calls/reasoning/store/stream/include/service_tier/prompt_cache_key/text）全不变且 input 为延长，则只发增量 + `previous_response_id`（`core/src/client.rs:391-458, 1531-1596, 2166-2186`）；增量与否不影响票（票在每请求重建的 client_metadata 里）。
- **连接复用**：`ResponsesConnectionKey = base_url + x-openai-account-routing-override 头 + auth_revision`（`model-provider/src/workspace_routing.rs:89-103`）；重连丢增量基线但**票跨重连存活**（挂在 ModelClientSession 不在 WebsocketSession）；换账号强制清票+全量重发（官方测试 `core/tests/suite/client_websockets.rs:1006-1145`）。
- **预热后 tier/model 变化**：首轮降级 full create（`previous_response_id=None`）但**票回放不变**（`agent_websocket.rs:617-643`）——复核修正了粘性侦查员"放弃连接复用"的误述（见 §4-2），真正被放弃的只是增量续传，连接照旧复用。

网关现状（「桥接替代」「握手回带偏差」发现，holds=true，本轮亲核确认）：
- 桥已现役：`backend/internal/service/openai_gateway_forward.go:819,929` WS 分支 → `openai_ws_forwarder_v2.go`；HTTP body → response.create 转换已有（`openai_ws_forwarder_payload.go:221-240`）；previous_response_id 按连接续链（`openai_ws_state_store.go:289` BindResponseConn / `v2.go:145` GetResponseConn）+ `previous_response_not_found` 恢复逻辑（`forward.go:971-973`，触发点 `v2.go:792`）。
- 预热已实现、默认关：`openai_ws_forwarder_support.go:21-23`（`generate=false` 于 `:78`，幂等于 `:54-66`——有 previous_response_id / 已预热 / 工具续传则跳过，与官方"每连接一次"语义一致）；配置 `backend/internal/config/config.go:1319-1320` 默认 false。
- **缺口 1（帧级回带）**：`openai_ws_forwarder_v2.go:69-78` 构建 payload 后只 `setOpenAIWSTurnMetadata`（写的是 `x-codex-turn-metadata`，`openai_ws_forwarder_payload.go:242-267`）；turnState 只进 `buildOpenAIWSHeadersWithTicket`（`v2.go:158-165`）→ 握手头，且仅在 dial 时发送（`openai_ws_pool.go:2119-2135`，复用分支不重发任何头）。本轮亲核 + 复核 grep 双重确认：backend 全部 `x-codex-turn-state` 命中均为 HTTP 头操作，无 client_metadata 写入。
- **缺口 2（事件级捕获）**：v2 读循环（`v2.go:588-881`）与透传路径均不解析 `response.metadata` 事件 headers；唯一收票点是 dial 时握手头捕获（`v2.go:341-360`、`openai_ws_forwarder_ingress.go:932-941` → BindSessionTurnState）。修补轮 grep `response.metadata` 于 v2/support 两文件 0 命中（全 service 其余 2 处命中为事件种类白名单与字段清单，均非票捕获）。
- **缺口 3（预热未开）**：见上。
- **缺口 4（生命周期超契约）**：复用连接把 dial 时缓存的陈旧握手票再次当本 turn 票（`openai_ws_pool.go:171-183,315-320` + `v2.go:138-140, 341-349`）——跨 turn 复用，违反官方契约注释（`core/src/client.rs:291-296`）。fail_closed 现状：`openai_codex_ticket.go:35-37, 310-313`（`ErrOpenAICodexTicketUnavailable`）。

#### 3.1.2 源码位置（file:line）

**官方契约（codex-rs v0.164.0-alpha.11.1）**
| 机制 | 位置 |
|---|---|
| 票常量/契约注释/OnceLock | `core/src/client.rs:164` / `:291-296` / `:311` |
| 每 turn 新建（票归零点） | `core/src/client.rs:694-707`（`:705`） |
| 换主清票 | `core/src/client.rs:1725-1729` |
| WS 帧级回带 | `core/src/client.rs:2117-2119` |
| WS 握手不回带 | `core/src/client.rs:1421-1428`（传 None） |
| generate=false 预热 | `core/src/client.rs:2187, 2331-2375, 2152-2154` |
| 事件级捕获 | `codex-api/src/endpoint/responses_websocket.rs:849-853` + `codex-api/src/sse/responses.rs:211-219, 281-290` |
| HTTP 捕获/回带 | `codex-api/src/sse/responses.rs:66-72` + `core/src/client.rs:2495-2513` |
| 握手捕获=死代码 | `codex-api/src/endpoint/responses_websocket.rs:560-566`（调用方全传 None：`core/src/client.rs:1360`、`responses_websocket.rs:477`、`ext/guardian-v2/.../connection_pool.rs:496`） |
| 官方预热流程 | `core/src/session/startup_prewarm.rs:358-396, 436-448`；消费 `core/src/tasks/regular.rs:78`、`core/src/session/turn.rs:194-195` |
| 生命周期/换账号测试锚点 | `core/tests/suite/turn_state.rs:92-204, 207-262`；`core/tests/suite/client_websockets.rs:1006-1145`；`core/tests/suite/agent_websocket.rs:311, 356, 564-573, 611-612, 639-643` |
| 增量续传判定 | `core/src/client.rs:391-458, 1531-1596, 2166-2186` |

**网关（backend/internal/...）**
| 机制 | 位置 |
|---|---|
| 桥入口（WS 分支） | `service/openai_gateway_forward.go:819, 929`；恢复逻辑 `:971-973`（触发点 `openai_ws_forwarder_v2.go:792`） |
| payload 构建（无票注入） | `service/openai_ws_forwarder_v2.go:69-78`（本轮亲核） |
| 票只进握手头 | `service/openai_ws_forwarder_payload.go:159-161`；流向 `openai_ws_forwarder_v2.go:158-165`（本轮亲核） |
| 握手头仅 dial 发送 | `service/openai_ws_pool.go:2119-2135`（复用分支 `:1200-1210, 1286, 1315, 1343, 1518`）；陈旧票缓存 `:171-183, 315-320` |
| 事件解析缺失 | `service/openai_ws_forwarder_v2.go:588-881`（grep `response.metadata` 0 命中，修补轮亲核；全 service 其余 2 处命中为事件种类白名单与字段清单，均非票捕获） |
| 预热（默认关） | `service/openai_ws_forwarder_support.go:17-19, 21-23, 74-88, 166-171`；`config/config.go:1319-1320` |
| response_id 续链 | `service/openai_ws_state_store.go:289`（BindResponseConn）、`:334`（BindSessionTurnState）；`openai_ws_forwarder_v2.go:145, 923` |
| 透传路径读下游回带票 | `service/openai_ws_v2_passthrough_adapter.go:841-844` |
| 跨账号票剥离守卫 | `service/openai_gateway_forward.go:1530-1532`（`guardOpenAICodexTurnStateEcho`，本轮亲核；铸造账号溯源表 `openai_gateway_service.go:508`） |
| fail_closed / 开关 | `service/openai_codex_ticket.go:35-37, 310-313`；热更开关 `service/domain_constants.go:723-730`（`openai_codex_ticket_enabled` / `openai_codex_ticket_allow_without_ticket`，本轮亲核） |

#### 3.1.3 网关落地步骤（4 小件 + 上线序列）

1. **帧级回带（代码）**：在 `openai_ws_forwarder_v2.go:77` `setOpenAIWSTurnMetadata` 旁，把本 turn 票写入 `payload` 的 `client_metadata["x-codex-turn-state"]`（对齐 `core/src/client.rs:2117-2119`）。取值优先级：同 turn 已捕获的上游票 > 下游回带票（`v2.go:74` / 透传 `openai_ws_v2_passthrough_adapter.go:841-844`，经跨账号守卫）> 无（turn 首请求不带，官方同款）。同时评估把 dial 时握手头注入（`openai_ws_forwarder_payload.go:159`）降级为可选——官方握手从不带票，多发反而可辨识。
2. **事件级捕获（代码）**：v2 读循环与透传路径解析 `response.metadata` 事件的 `headers["x-codex-turn-state"]`（大小写不敏感，对齐 `codex-api/src/sse/responses.rs:281-290`），写入 **per-turn** 状态（绑定到 response 链/session hash），供步骤 1 后续帧使用；预热读循环（`openai_ws_forwarder_support.go:96+`）同样捕获并把预热票绑定为首轮可回放票（对齐官方预热→首轮回放语义）。
3. **打开预热（配置）**：`gateway.openai_ws.prewarm_generate_enabled=true`。保持既有幂等语义（`support.go:54-66`：有 previous_response_id / 已预热 / 工具续传跳过）——即每连接一次，**不要**改成每 turn 预热（官方预热是线程启动后台一次，`client.rs:2344-2346` 幂等检查；复核员对「WS预热打票」发现的修正点）。
4. **生命周期对齐（代码+配置）**：
   - 修正跨 turn 复用：复用连接上不再把 dial 时握手票当本 turn 票（`v2.go:138-140, 341-349` 路径），票改为 per-turn：turn 首请求无票（或用本 turn 预热票）→ 事件捕获 → 同 turn 回带 → turn 结束丢弃；
   - 跨账号剥离守卫 `guardOpenAICodexTurnStateEcho`（`forward.go:1530-1532`）保留不动；
   - 打开 `openai_codex_ticket_allow_without_ticket`（`domain_constants.go:728`，现成配置）——fail_closed 降级为无票放行：官方每 turn 首请求本来无票，无票放行比塞陈旧票更贴近官方形状。
5. **上线序列**：单账号实测（§3.1.5）→ 灰度 1%→10%→50% 账号 → 全量后用热更开关 `openai_codex_ticket_enabled=false` 下线打票探针（`domain_constants.go:723-726`，"关闭：不打票、不注入 x-codex-turn-state，按原链路转发"）。回滚：同开关热更打开，或 `prewarm_generate_enabled=false` 单独关预热。

#### 3.1.4 账号安全评估（逐项标注上游可见变化 + 灰度）

| 变化 | 上游可见性 | 风险 | 灰度建议 |
|---|---|---|---|
| 票从握手头移到每帧 body client_metadata | **可见**（请求形状变化） | **低**：与官方契约通道一致度上升、可辨识度下降（官方握手从不带票） | 随 P0 整体灰度；开关粒度=网关配置 |
| 事件级捕获 | **不可见**（纯观测响应） | 无 | 无需单独灰度 |
| 开启 generate=false 预热 | **可见**（每次连接建立多 1 个无生成 token 往返） | **低**：官方客户端线程启动即做此动作，形状同款；偏差点在**频率**——网关连接建立频率若远超真实单用户会成为信号 | ①先 1 个测试账号开 24-48h 观测；②灰度 1%→10%→50%；③严守每连接一次幂等（`support.go:54-66` 已实现）；④监控预热读失败/close code（`support.go:96-111` 已有 close_status/close_reason 日志） |
| fail_closed→无票放行 | 不可见（本就发不出去的请求现在发出） | 低：官方首请求无票同款 | 直接开（现成配置） |
| 跨 turn 票复用修正 | 可见（后续 turn 首请求不再带陈旧票） | **低**：从超契约回到契约内，官方契约注释明言跨 turn 回带"can cause routing bugs" | 随 P0 整体灰度 |
| 跨账号共享票 | — | **硬约束判死，不实施**；守卫保留 | — |

**镜像省略原则**（「后端条件门」发现，holds=true）：官方在条件门不满足时会**整头省略**（如 provider 自带凭据时 routing-hint 整个省略，`core/tests/suite/client_websockets.rs:327-360`）；网关镜像任何官方头时必须同时镜像省略逻辑，否则"官方本应省略的字段被多发"反而成为指纹。P0 各件均向官方形状收敛，符合此原则。

#### 3.1.5 验证方法

1. **单测/集测（先行）**：mock 上游 WS，复刻官方测试形状——
   - 预热响应 `response.metadata` 事件带票 → 断言首轮真实帧 `client_metadata["x-codex-turn-state"]` 回放（对照 `core/tests/suite/agent_websocket.rs:639-642`）；
   - 同 turn 第 2 帧回带、下一 turn 首帧不带（对照 `core/tests/suite/turn_state.rs:92-204` 序列 `[null,null,"ts-1",null]`）；
   - 换账号后全量重发+票清空（对照 `core/tests/suite/client_websockets.rs:1006-1145`）；
   - 预热后 tier 变化 → 首轮 full create 但票照放（对照 `agent_websocket.rs:617-643`）。
2. **单账号实测（关键未知项）**：测试账号开预热，验证①`response.metadata` 事件 headers 出现 `x-codex-turn-state`（网关出站形状下上游是否对 prewarm 铸票——唯一静态不可证项）；②后续帧回带被上游接受（无 4xx/非策略 close）。对照 doctor 探针结果语义：HTTP 101 状态、`x-reasoning-included`、`openai-model`、immediate close code/reason（`cli/src/doctor.rs:2377-2395`、`codex-api/src/endpoint/responses_websocket.rs:164-165, 448-495`）。
3. **灰度观测指标**：预热成功率与事件捕获率（对标官方 98.6% 首请求铸票率）；401/403/404/close code 分布对比基线；无票放行请求的成功率与质量（降智判据沿用旧报告方案 1：请求模型 vs 流内实际模型 + safety-buffering 头，与本报告正交）；`previous_response_not_found` 恢复触发率。
4. **回归**：账号切换/failover 链路（守卫剥离+全量重发）、`previous_response_not_found` 恢复、HTTP 回退路径（票走响应头通道，机制等价——「连接复用」发现 holds=true）。

---

### 3.2 P1 工作区路由发现（GET /wham/accounts/check）

**机制原理**：官方客户端里**唯一的服务端下发、账号级粘路由机制**，也是最接近"探针"的官方形态：app-server 用普通认证 `GET {chatgpt_base}/wham/accounts/check`（无 WS、无票、无 attestation）拿到每账号 `workspace_backend_origin` 与 `account_routing_override`（合法值仅 `NO_CONSTRAINT|us|us_cr`）；随后整会话 base_url 重写到该 origin 并注入 `x-openai-account-routing-override` 头；该三元组（base_url+routing 头+auth revision）即 WS 连接复用键；带该头的路由拒绝重定向；按 auth owner generation 缓存，账号变更即失效。

**源码位置**：`codex-rs/backend-client/src/client.rs:400-410`（GET 端点，`:404-405` 两种路径风格）；`backend-client/src/types.rs:142-143`（字段，复核修正原引 147-148）；`app-server/src/request_processors/account_processor/workspace_routing.rs:391-424, 525-566`；`model-provider/src/workspace_routing.rs:12, 40-77, 89-103`；`core/src/client.rs:1305-1312`（拒绝重定向）；`login/src/auth/manager/workspace_routing.rs:80-88`。

**网关落地步骤**：①先核对网关现状（本次发现集未覆盖网关侧是否已实现，见 §5-6）；②按账号调用一次 `/wham/accounts/check`，缓存对齐官方失效语义（凭据刷新/账号切换失效）；③出站 base_url 与 routing 头按映射改写；④WS 连接池的连接复用键纳入该三元组。

**账号安全评估**：新增一种上游可见请求（GET /wham/accounts/check）——官方 app-server 路由发现原样调用，且频率为凭据周期级（非高频）。风险**低**。灰度：单账号先验证响应形状（`AccountsCheckResponse{accounts}`），确认字段与值域后再按账号批量；异常监控该路径 4xx。

**验证方法**：单账号实测响应字段；值域校验（仅三值）；路由改写后的请求落地 host 断言；对照官方"account changed during workspace routing discovery"报错语义做缓存失效测试。

---

### 3.3 P2 缓存亲和 + 会话身份形状对齐

**机制原理**：
- **缓存粘性主通道（官方明文）**：`prompt_cache_key`（body）与 `session-id` 头（连字符）同源——`core/src/client.rs:680-681` 官方注释原文 "ChatGPT derives cache affinity from the Responses session-id header"；三态 SessionDerived（默认）/Custom/Omit；`session_id` = 根 thread_id；跨 turn 恒定；`store` 恒 false（不依赖服务端 storage）；HTTP 结构无 `previous_response_id`（续传只在 WS）。
- **身份头组（全客户端自生成）**：`installation_id`（持久化 `~/.codex/installation_id`，uuid v4 跨会话复用）、`thread_id`/`session_id`（UUIDv7）、`x-client-request-id`=thread_id、`x-codex-window-id`（UUIDv7，随自动压缩窗口推进）、`turn_id`（每 turn now_v7）、`x-codex-turn-metadata`（约 30 字段身份 JSON，部分字段按 request/thread identity 门控）。
- **attestation**：官方 CLI/TUI 均不发送 `x-oai-attestation`（复核确认真正机制是客户端 initialize 能力硬编码 `request_attestation:false`，`app-server-client/src/lib.rs:257`）→ **网关不发送即与官方形状一致**，无需伪造。
- **daybreak/access_programs**：非免票开关（entitlement 与模型策略归服务端，源码注释明说），仅当下游带 `access_programs:{cyber:...}` 时按契约转发。

**源码位置**：`core/src/client.rs:657-688, 1120`；`core/src/thread_manager.rs:271-281`；`codex-api/src/requests/headers.rs:5-14`；`core/src/session/session.rs:973-980`；`core/src/responses_metadata.rs:382-410, 412-461, 548-609`；`core/src/installation_id.rs:20-54`；`core/src/state/auto_compact_window.rs:13, 77-85`；测试锚点 `core/tests/suite/prompt_cache_key.rs:50-83, 231-294`；attestation `core/src/attestation.rs:7-26`、`core/src/client.rs:926-938`、`app-server/src/message_processor.rs:378`；daybreak `core/src/cyber_access_program.rs:1-64`、`codex-api/src/common.rs:31-47`。

**网关落地步骤**：网关已有指纹改写骨架 `applyStagedCodexFingerprintClientMetadata`（入口 `backend/internal/service/openai_codex_fingerprint.go:56`，改写核心 `applyCodexFingerprintClientMetadata` `:414-433` + `applyCodexFingerprintToClientMetadataMap` `:440-448`，改写 installation/session/thread ID）。补齐：①per-account 稳定 installation_id（模拟官方跨会话持久语义）；②`session-id` 头与 body `prompt_cache_key` 同源同值（官方同源语义）；③turn-metadata 字段补全至官方约 30 字段形状（含门控字段的正确出现/省略）。

**账号安全评估**：无新增上游可见行为类型（既有请求字段/头的**取值**对齐）。风险**低-无**。注意：身份值分布要模拟真实用户（避免全部账号同形状同 UUID 版本号模式）；镜像省略原则同样适用（官方门控不出现的字段不要多发——含 §4-1 修正的 beta-features 枚举：桌面平台还有第 4 个可能 key `prevent_idle_sleep`，镜像时按官方过滤逻辑动态生成而非硬编码清单）。

**验证方法**：字段级 diff（官方客户端真实请求 vs 网关出站请求）；prompt cache 命中率变化观测；turn-metadata 字段集快照对比。

---

### 3.4 P3 官方基础设施 cookie jar 对齐（__oailb）

**机制原理**：0.164 官方客户端维护进程级 cookie jar：Set-Cookie 按 allowlist 过滤（`__cf_bm/__cflb/__cfruid/__cfseq/__cfwaitingroom/__oailb/_cfuvid/cf_clearance/cf_ob_info/cf_use_ob/cf_chl_*`），`__oailb` 注释明说"是 OpenAI 基础设施路由 cookie，不是认证 cookie"；HTTP 与 WSS **共用同一 jar**（wss 升级为 https 作用域）；域严格限定 chatgpt.com 等三域；测试证明 `__oailb`（Path=/backend-api）跨 backend-api 各路径复用。

**源码位置**：`codex-rs/http-client/src/chatgpt_cloudflare_cookies.rs:17-49, 106-127, 197-214, 354-382`；`http-client/src/chatgpt_hosts.rs:4-5`；WS 侧 `websocket-client/src/lib.rs:172, 191-194`；测试 `websocket-client/src/cookie_tests.rs:37-221`。

**网关落地步骤**：①连接池 per-account 建 jar（HTTP 与上游 WS dial 共用）；②只存 allowlist cookie；③域限定三域白名单；④显式 Cookie 头优先于 jar（官方语义）。

**账号安全评估**：被动回带，风险**低**。注意一个方向性选择：官方 jar 是进程内跨 auth context 共享的（单用户应用语境），但**网关是多账号服务，跨账号共享 jar 即跨账号信号泄漏**——按硬约束采用 per-account jar（比官方更保守，复核员认可为合理安全建议而非源码事实，此处明确标注）。

**验证方法**：真账号抓包验证 `__oailb`（服务端行为上仅在 /models 下发——运行时转述，源码不可证）被后续请求回带；跨账号隔离测试（账号 A 的 cookie 绝不出现在账号 B 的请求）。

---

### 3.5 P4 routing-hint 构造 + doctor 探针对照

**机制原理**：`x-codex-routing-hint` 是客户端本地拼装的 `model=<slug>` 或 `model=<slug>;tier=<tier>`（tier 取自请求 body.service_tier），advisory 意图声明、非粘性令牌（官方容忍过期值——连接复用时旧 hint 照用）；条件门：uses_codex_backend 且非 guardian reviewer，否则整头省略。doctor 的 `network.websocket_reachability` 内置与真实连接完全同构的握手探针（同 URL/头/认证/TLS/自定义 CA，握手后即断、不发请求帧、250ms immediate-close grace）——这是**官方承认的"探针"形态**：若网关需要诊断上游 WS 可达性/策略秒断，用这个形状，不要伪造推理请求。

**源码位置**：`core/src/client.rs:163, 1281-1297, 1438-1440, 1884-1893`；探针 `cli/src/doctor.rs:2284-2416, 2378-2395`、`codex-api/src/endpoint/responses_websocket.rs:448-495, 164-165`；省略行为测试 `core/tests/suite/client_websockets.rs:326-360`。

**网关落地步骤**：①routing-hint 按官方格式本地构造（含省略门控）；②诊断场景对照 doctor 探针形状与结果语义（HTTP 状态、`x-reasoning-included`、`openai-model`、close code/reason）。

**账号安全评估**：advisory 头本地构造，风险**无**。诊断探针虽是官方形状，但**不要常态化高频调用**（官方仅在 doctor 诊断时用）。

**验证方法**：头格式单测；doctor 探针语义对照（immediate close code/reason 区分可用连接与策略性秒断）。

---

### 3.6 判死项（源码或硬约束否决）

| # | 方案 | 判死原因 | 证据 |
|---|---|---|---|
| D1 | **伪造票**（本地构造 turn-state blob） | 客户端全树无铸造路径：3 处 `turn_state.set(` 全是接上游值；`gAAAAA`/长度/编码生成代码 0 命中；basis_points/BPS 无路由通道 | 「铸票三通道」holds=true；`codex-api/src/sse/responses.rs:72`、`responses_websocket.rs:852, 566` |
| D2 | **跨账号共享票** | **用户硬约束直接判死**；技术上亦知异账号票与出站身份自相矛盾。既有守卫 `guardOpenAICodexTurnStateEcho`（`backend/internal/service/openai_gateway_forward.go:1530-1532`）+ 溯源表（`openai_gateway_service.go:508`）保留 | 硬约束 + 「桥接替代」复核确认 |
| D3 | **跨 turn 票硬塞**（现状：复用连接把 dial 时握手票当本 turn 票） | 超契约：官方契约注释明言跨 turn 回带 "violates the client/server contract and can cause routing bugs"；官方每 turn 新建 OnceLock；能否被上游接受只有实测能证明——不赌，直接在 P0④修正 | 「票生命周期」holds=true；`core/src/client.rs:291-296, 694-707`；现状位置 `openai_ws_pool.go:171-183, 315-320` + `openai_ws_forwarder_v2.go:138-140, 341-349` |
| D4 | guardian 分类器免票通道 | responses-lite 通道（`x-codex-guardian: classifier`，两处 transport 传 turn_state=None）仅服务 guardian 分类器，主模型不可复用；标注 classifier 头反而新增可辨识标记 | 「guardian分类器免票通道」holds=true；`ext/guardian-v2/src/async_scorer/sampler/connection_pool.rs:400-432, 496, 552` |
| D5 | API-key 整体豁免 | 换到 api.openai.com 计费世界≠绕 chatgpt.com 门禁；且该发现整体未过复核（§4-3：astra 实为内置目录事实、模型目录请求仍打 chatgpt.com/backend-api/codex） | 「API-key整体豁免」holds=false |
| D6 | 模型目录 Etag 上行 | 纯下行信号：/models 请求空 HeaderMap，If-None-Match 全树 0 命中；etag 仅诊断可观测 | 「模型目录Etag」holds=true |
| D7 | 新面旁路 headers（rendezvous-rejection-reason / noise-stream-id / subscribe-cursor / server-id / protocol-version） | 全部 confined 在 exec-server 远程中继与 remote_control 旁路通道，不参与 /responses 门禁或打票路由 | 「新面headers全在旁路通道」holds=true；穷举 grep 支持 |
| D8 | attestation 伪造 | 官方 CLI/TUI 不发送 x-oai-attestation → 不发送即对齐；伪造反而新增可辨识面（且真值只有桌面/IDE 宿主能生成） | 「attestation非硬门禁」holds=true |
| D9 | daybreak/access_programs 当免票开关 | entitlement 与模型策略归服务端（源码注释明说"Discovery metadata does not grant access"）；仅形状对齐项 | 「daybreak访问程序声明」holds=true |

其他核销（「不存在项核销」holds=true）：0.144 版本门槛（纯服务端）、basispoint/partner 通道、turn_state 配置开关（票的关闭点只有 turn 结束与换主两处）、`codex.websocket.continuation`（0.164 只是 otel 指标名非开关）——均无客户端通道，不值得投入。

---

## 4. 复核未通过的发现（unconfirmed）

以下 5 条发现独立复核**未通过**（holds=false）。**均不影响 P0 推荐**——P0 的证据链全部来自 holds=true 的发现；未通过项的反证反而强化了部分结论。

| # | 发现（侦查员） | unconfirmed 原因 | 对本报告的影响 |
|---|---|---|---|
| U1 | **beta特性声明头**（粘性机制侦查员） | grep 记账错误：`Stage::Experimental` spec 定义实为 **4 处**（桌面平台还有 `PreventIdleSleep`，`features/src/lib.rs:2077-2092`），非声称的 3 处——`x-codex-beta-features` 头内容枚举不完整；次要：Stage 判定函数引错（应为 `experimental_menu_description` lib.rs:76-83）。机制主线（头存在、非路由粘性、RemoteCompactionV2 短路绕过 stage 过滤）核实为真 | P2 形状对齐中：beta-features 头内容**按官方过滤逻辑动态生成**，勿硬编码 3-key 清单 |
| U2 | **启动预热**（粘性机制侦查员） | 证伪点："预热握手 model/tier 与后续不一致时**放弃连接复用**"与其所引测试直接矛盾——`responses_websocket_prewarm_reuses_advisory_model_and_tier_routing_hint`（`client_websockets.rs:1232-1300`）断言 `handshakes().len()==1` 即**连接照旧复用**，真正被放弃的只是增量续传（tier 变更→首轮 full create，`agent_websocket.rs:617-643`）；另"预热不捕获票"仅对握手成立，预热请求本身经 `stream_request(Some(turn_state))` 捕获（`client.rs:2251`） | 对 P0 是**利好修正**：预热铸票链路更稳固（预热请求自带捕获）；P0 设计采纳正确版本——tier 变化只降级续传、票回放不变、连接复用 |
| U3 | **API-key整体豁免**（新面开关侦查员） | 两条实质证据被证伪：① "grep astra = 0 命中"为假——`models-manager/models.json:4` 内置 `gpt-6-astra`（include_str! 编入二进制）；② "请求不再打 chatgpt.com/backend-api/codex"对模型目录不成立——API-key 模式下 /models 目录请求仍打 chatgpt.com/backend-api/codex（`model-provider/src/models_endpoint.rs:113-120`） | D5 判死的证据更实：astra 在列是客户端内置事实，非服务端策略 |
| U4 | **gateway_oauth与命令auth**（新面开关侦查员） | 过度解读被证伪："该形态不带任何 x-codex-* 私有头"为假——通用 HTTP Responses 路径**无条件**附加 `x-codex-beta-features`（恒非空）与 `x-codex-window-id`（`client.rs:2504`、`responses_metadata.rs:384`），门控的只是 routing-hint/guardian/CodexResponsesHeaders。主定性（合法接网关形态、非绕门禁、gateway_oauth 保留名单禁注入 codex 门禁头）仍与代码相容 | 对 P2 的补充：即使下游经 gateway_oauth 接入，官方客户端仍会带 beta-features/window-id 等兼容头到网关——网关转发时勿当异常剥离 |
| U5 | **guardian-v2定性**（新面开关侦查员） | 证伪点："x-codex-guardian: reviewer 头本身不受 auth 限制"为假——整个 CodexResponsesHeaders（含 reviewer 头）仅在 `config.model==model && uses_codex_backend && supports_codex_backend_routes` 时附加（`core/src/client.rs:1225-1235`、`core/src/responses_headers.rs:1-9`），API-key 模式下 reviewer 头根本不上请求。次要：feature key 实为 `guardianv2`（非 `guardian_v2`）、spec 计数为 10 非 8 | 不改变 D4 判死；强化"镜像省略原则"：guardian 系头在非 codex-backend 形态下官方会省略，网关同样不该发 |

---

## 5. 遗留未知与下一步

### 5.1 遗留未知（静态不可证 / 未核对）

| # | 未知项 | 性质 | 缓解 |
|---|---|---|---|
| 1 | **网关出站形状（非官方 UA/指纹）下，上游是否对 generate=false 预热铸票** | 服务端策略，源码静态不可证（官方代码只处理"带了票就存"，`let _ = turn_state.set(...)`） | P0 上线前必须的单账号实测（§3.1.5-2）；这是 P0 唯一的残余不确定性 |
| 2 | 上游是否接受跨 turn 票回放（现状超契约行为） | 只有实测能证明 | 不赌：P0④直接修正为 per-turn，不依赖该行为 |
| 3 | 首请求铸票率 98.6% 的 2 个例外 turn | `notes-client-runtime.md §3.2` 自述"未能完全解释" | 观测指标里保留无票分支的成功率统计 |
| 4 | B1（OnceLock 写一次 vs 服务端中途换票→陈旧令牌回放）是否在生产发生 | 服务端行为未知；客户端行为已被官方测试坐实（同 turn 第 2 票被忽略） | 网关事件级捕获实现时**对齐官方写一次语义**（同 turn 首票胜出），与官方一致即无额外风险；官方披露走 `REPORT-codex-bug-disclosure.md §6` 渠道 |
| 5 | `__oailb` 是否仅在 /models 下发；"/responses 只能抓到 __cf_bm" | 服务端行为，客户端源码不可证（源码只决定收什么存什么）；0.163 二进制 0 命中的历史 grep 无法重跑（scratch 无 0.163 树） | P3 实施时真账号抓包确认 |
| 6 | **网关侧是否已实现工作区路由（/wham/accounts/check）** | 本次发现集只覆盖官方客户端侧，网关侧现状未核对 | P1 动工前先 grep/读网关源码核对（本次未查，如实标注） |
| 7 | 0.163 HTTP 回带行为（旧 B2） | 无 0.163 源码树 | 悬置；对 P0 无影响（P0 基于 0.164 契约） |

### 5.2 下一步（优先级序）

1. **P0 四件套实现**（§3.1.3）：帧级回带 + 事件级捕获 + per-turn 生命周期修正（代码）；`prewarm_generate_enabled` 与 `openai_codex_ticket_allow_without_ticket`（配置，现成开关）。
2. **单测先行**：复刻官方 `turn_state.rs` / `agent_websocket.rs` 测试形状的网关侧测试（§3.1.5-1）。
3. **单账号实测**：验证未知项 1（预热铸票）——P0 的 go/no-go 门。
4. **灰度**：1%→10%→50% 账号，观测 §3.1.5-3 指标；任一灰度段 401/403/close 异常升高即热更回滚。
5. **下线探针**：全量稳定后 `openai_codex_ticket_enabled=false`（热更）。
6. **P1 前置核对**：查网关 workspace routing 现状（未知项 6），再决定实现。
7. **P2-P4 形状对齐**按 P0 稳定后的观测差值决定优先级。

---

## 附：发现集统计与置信说明

- 33 条发现：28 条复核通过（holds=true），5 条未通过（§4）。（发现集复核为上一轮复核员工作，本报告未重跑全部 33 条的原始侦查。）
- 修补轮（发布前）全量亲核：P0 全部承重断言的官方源码锚点、测试锚点、网关锚点与运行时证据数字均由修补者本会话逐条 Read/grep 重验（清单见卷首"修补轮全量亲核"1-10 项），全部证实；发现并改正 5 处行号漂移（卷首第 11 项）。
- 本报告未运行任何 Go/Rust 测试套件（静态分析汇总；P0 验证方法中的测试为实现后执行项，尚未运行）。
- 两处背景报告与本报告的关系：旧 `REPORT-codex-solution-final.md` 方案 2（打管线重构）由本报告 P0 **取代**——P0 不再修探针形状，而是让探针无存在必要；旧方案 1（降智检测换判据）与本报告正交，继续有效；`REPORT-codex-bug-disclosure.md §1 b2`（"WS 握手回带票"）已被本报告证据**修正**：官方不在 WS 握手头回带票（`client.rs:1427` 显式 `/*turn_state*/ None`，修补轮亲核）、101 握手头捕获是死代码（三处调用方全传 None，「WS帧级回带」发现 holds=true）——该报告所引 `responses_websocket.rs:715` 实为事件流函数参数（`turn_state: Option<&OnceLock<String>>`，捕获用非回带用），修补轮亲核。

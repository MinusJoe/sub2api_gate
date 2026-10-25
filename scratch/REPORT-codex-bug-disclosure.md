# Codex「打票」机制全源码定位 + 替代方案位置 + 可披露 Bug 清单

日期：2026-10-24 ｜ 方法：子agent 额度被占用（model concurrency limit exceeded，另一对话占用），由主 agent 亲自执行三角任务
证据基座：
- **同版源码快照**：openai/codex tag `rust-v0.164.0-alpha.11.1`（与本机二进制精确同版），`scratch/src/codex-rust-v0.164.0-alpha.11.1/`
- **新二进制**：`C:\Users\zfx\AppData\Local\OpenAI\Codex\bin\2993767bd1dfb009\codex.exe`，354,661,168 B，SHA256 前缀 `1103e9e0`，0.164.0-alpha.11.1（30 处版本串）
- 扫描器：`scratch/scan2_headers.py`（输出见本报告 §4）

---

## 1. 打票机制源码地图（file:line，全部来自 rust-v0.164.0-alpha.11.1）

| # | 机制 | 精确位置 | 要点 |
|---|---|---|---|
| a1 | 票常量定义 | `codex-rs/core/src/client.rs:164` | `X_CODEX_TURN_STATE_HEADER` |
| a2 | **HTTP/SSE 读票点** | `codex-rs/codex-api/src/sse/responses.rs:66-72` | 从 `/responses` 响应头 `set` 进 `OnceLock` |
| a3 | **WS 握手读票点** | `codex-rs/codex-api/src/endpoint/responses_websocket.rs:560-566` | 握手响应头 set |
| a4 | **WS 事件流读票点**（第三通道） | `responses_websocket.rs:849-852` + `sse/responses.rs:211,281-284` | SSE 事件 JSON 里 `headers` map 也能带票 |
| b1 | **HTTP 回带注入** | `core/src/client.rs:1489-1518`（`build_responses_options`）→ `:2496-2510`（`build_responses_headers`） | extra_headers 注入 `x-codex-turn-state` |
| b2 | **WS 回带注入（头）** | `responses_websocket.rs:715`（`send_websocket_request` 携带） | 每条 WS 请求回带 |
| b3 | **WS 回带注入（body）** | `core/src/client.rs:2117-2118` | 票还写进请求体 `client_metadata["x-codex-turn-state"]` |
| b4 | **compact 回带** | `core/src/session/turn.rs:437,623` | 自动压缩请求经 `turn_state_with_auth_owner()`（带属主守卫）回带 |
| c | 票存储与契约 | `core/src/client.rs:291-311` | turn 级 `Arc<OnceLock<String>>`；**跨 turn 复用违反契约**；auth 换主清空 |
| d | 票清除点（换主） | `core/src/client.rs:1725-1729` | `auth_owner_generation` 变化 → 重置 OnceLock；HTTP 路径靠 `:1822-1827` TurnAborted 兜底 |
| e | turn-metadata 构造 | `core/src/session/mod.rs`（`has_turn_metadata`/`build turn metadata`），头常量 `client.rs:165` | JSON（含 `turn_id`），同 turn 恒定 |
| f | attestation | `core/src/attestation.rs:7-30` | `AttestationProvider` trait，host(app-server) 逐请求即时生成；HTTP 路径同样注入（`client.rs:1510-1512`）——**修正旧结论"WS 专属"** |
| g | WS 握手附加头 | `client.rs:163-179` + `build_websocket_headers :1419-1445` | routing-hint/beta-features/responses-lite/`x-openai-subagent`（新） |
| h | WS beta 值 | `client.rs:177` | `responses_websockets=2026-02-06`（未变，"V2"） |
| i | 服务端降智信号读取 | `sse/responses.rs:50-53`（safety_buffering_treatment）、`:34-35`（openai-model） | `treatment_from_headers`；降智判据=served model |
| j | 会话级传输回退 | `client.rs:283-287` | 一旦某 turn 回退 HTTP，**整个会话后续全走 HTTP**（`disable_websockets`） |
| k | WS 预热 | `client.rs:1330-1337`（"startup prewarm"）、测试 `core/tests/suite/agent_websocket.rs:570`（`warmup_turn_state`）、`from_untraced_warmup` 字段 | 官方客户端自己也做预热+增量续传 |
| l | 客户端版本门槛 | **源码中无 0.144 检查**（grep 0 命中） | 旧版本 404 是纯服务端行为 |
| m | BPS/basispoints | 源码 0 命中 + 二进制 0 命中 | 客户端不存在该通道 |

测试契约锚点：`core/tests/suite/turn_state.rs:28-95`（HTTP 同 turn 回带/跨 turn 清空）、`:97+`（WS 版）。

## 2. 对旧结论的修正（0.163 二进制级 → 0.164 源码级）

| 旧结论（0.163 二进制） | 新结论（0.164 源码+测试） |
|---|---|
| HTTP 路径不回带票（唯一写点 0x78719e0 全在 WS） | **错**。HTTP 同 turn 内回带（client.rs:1501-1503 + 测试断言 requests[1] 带 ts-1）。0.163 是否确实没有需用 `rust-v0.163.0-alpha.11.2` tag 复核（见 §6 遗留） |
| x-oai-attestation 仅 WS | **部分错**。HTTP 也会带（client.rs:1510），前提是 host 提供 provider |
| 读票只有响应头+WS握手 | 还有第三通道：SSE/WS 事件 JSON 的 headers map（responses_websocket.rs:849） |
| 票只进请求头 | 还进 WS 请求体 client_metadata（client.rs:2117） |
| turn-metadata 28 字段 | 源码化后见 session/mod.rs；新增 `x-openai-subagent`、`x-codex-parent-thread-id` |

## 3. 「打票的替代方案」——位置级答案

**客户端内生的"免打票"机制（官方客户端自己怎么活）：**
1. **WS 长会话 + 增量续传**：`client.rs` `WebsocketSession.last_request`/`WebsocketContinuation`——同 turn 后续请求只发增量（`prepare_websocket_request`，mode="incremental"），配合票回带形成粘性；**位置：client.rs:316-330、2119-2135**。
2. **启动预热**：`connect_websocket` "startup prewarm"（client.rs:1330 注释）——预热连接也走完整握手身份。
3. **会话粘性 ID**：`prompt_cache_key`、`session_id`/`thread_id` 头组（`build_session_headers`）。
4. **提速档**：`service_tier`/`fast_mode`(43 处)/`ultrafast`(13 处)/`step_model_switching`(10 处)——二进制偏移 `273598125` 起簇。
5. **HTTP 会话级回退**：`disable_websockets`（client.rs:283-287）——回退后全会话 HTTP+回带。
**结论不变**：不存在客户端可用的"免票 VIP 通道"；BPS 无客户端痕迹。对网关的可行替代仍是上一轮报告的方案 A（被动采集+同账号同模型回带）——现在有了源码级契约依据（client.rs:291-307）。

## 4. 0.164 新二进制增量（vs 0.163）

仍在（机制未下线）：turn-state×9、turn-metadata×10、attestation×4、routing-hint×4、websockets=2026-02-06×6、safety_buffering、Luna×33、too_many_denials、device_key_bindings×2、attestation/generate×3、0.144.0×3（服务端版本号串）。
**新增头/子系统（0.163 笔记未记录）**：
- `x-codex-guardian`/reviewer（源码：`client.rs:2483-2488` `is_guardian_reviewer`，`ext/guardian-v2/` 整个扩展）
- `x-codex-noise-stream-id`、`x-codex-rendezvous-rejection-reason`、`x-codex-subscribe-cursor`、`x-codex-server-id`/`x-codex-protocol-version`、`x-codex-inference-call-id`、`x-codex-image-turn-id`/`imagegen-request-id`、`x-codex-parent-thread-id`+`x-openai-subagent`、`x-codex-websocket-max-unfragmented-message-bytes`、app-server zstd 流（`x-codex-app-server-compression…zstd-stream-v1`）
- 消失：无（`__oailb` 依旧 0 命中，维持"客户端不感知"结论）

## 5. 可披露 Bug 候选（诚实分级）

**B1（主要候选，功能/路由类）：turn-state `OnceLock` 写一次语义 vs 服务端中途换票 → 陈旧令牌回放**
- 位置：`sse/responses.rs:66-72`、`responses_websocket.rs:560-566`、`:849-852`（三处全部 `let _ = turn_state.set(...)`，第二次 set 静默失败）
- 机制：契约（client.rs:291-307）要求同 turn 持续回带"unchanged"；但三处捕获代码对**每个响应**都尝试 set，说明预期服务端可能再次下发；OnceLock 写一次意味着**中途轮换的新令牌被静默丢弃**，后续请求继续回带旧令牌——正是其文档自己警告的"routing bugs"。
- 现有测试盲区：`turn_state.rs` 只测"首令牌持续有效"，未测"同 turn 第二响应携带不同令牌"。
- 复现思路（可直接给官方的测试草案）：mock server 同 turn 第 2 个响应带 `ts-2`，断言第 3 个请求回带 `ts-1`（现状）vs `ts-2`（契约期望）。
- 严重度：Low-Medium（路由质量/降智关联，非安全）。置信：代码级高；服务端是否真的中途换票=未知（报告里应以问询+测试草案形式提交）。

**B2（问询级）：0.163/0.164 行为漂移** —— 若 `rust-v0.163.0-alpha.11.2` 源码确认 HTTP 无回带注入，则 0.163 存在违反自身契约的真实缺陷（影响所有会话级 HTTP 回退用户）。20 分钟可核实。
**B3（新面未审）**：guardian-v2 `ext/guardian-v2/src/async_scorer/sampler/connection_pool.rs:422` 复用 responses-lite 通道——建议作为后续审计对象（本机源码已就绪）。
**排除记录（看似问题实为设计）**：HTTP 不注 attestation（实为注入，host 可用时）；换主不清票（HTTP 路径由 TurnAborted 兜底）；版本 404（纯服务端）。

## 6. 披露方案（官方指定路径）

1. **安全类**（若 B1 被官方认定为可滥用面）：Bugcrowd — `https://bugcrowd.com/engagements/openai`（仓库 `SECURITY.md` 官方指定，"managed through Bugcrowd"）。
2. **功能/路由类（B1 首选）**：`github.com/openai/codex` issues/RFC，附：源码 file:line + 最小复现测试草案 + 影响描述（sticky routing 断裂→质量退化）。
3. 报告写法要点：只描述客户端行为与契约矛盾；**不携带**网关侧运营信息（账号池/代理/共享票实践）；以"官方客户端自身质量"为出发点。
4. 提交前自查：B1 先在本地跑通复现测试（源码树已具备全部 test harness：`core_test_support::responses` mock server）。

## 7. 遗留（下次子agent额度可用时）

- `rust-v0.163.0-alpha.11.2` tag 复核 B2（HTTP 回带是否存在）
- guardian-v2 / noise-stream / rendezvous 三个新面专项审计
- 卫星二进制（codex-code-mode-host.exe 78MB 等）深扫
- `scan2_headers.py` 扩展成 xref 级（对齐 notes-binary.md 方法论）

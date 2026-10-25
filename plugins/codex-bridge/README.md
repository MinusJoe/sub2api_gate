# codex-bridge —— 官方契约对齐的 OpenAI OAuth 出站传输插件

sub4api（Sub2API 系）插件，能力 `openai.oauth.outbound_transport.v1`。目标：**用官方客户端的票据与路由契约替代失效的"打票探针"，缓解模型降智（safety-buffering / 模型映射降级）**。

设计依据：`scratch/REPORT-codex-ticket-alternatives-v2.md`（33 条发现、28 条独立复核）与 `scratch/REPORT-codex-bug-disclosure.md`（机制源码地图，rust-v0.164.0-alpha.11.1 file:line）。

## 它做什么

官方客户端从不"打票"：票（`x-codex-turn-state`）由上游按"每 turn 首个非增量请求"自然铸造（实测 98.6% 首请求响应带票），客户端只做**捕获 + 契约通道回带**。本插件把网关的出站传输对齐到同一契约：

| 件 | 行为 | 官方契约锚点 |
|---|---|---|
| 票捕获 | 从上游 HTTP 响应头接住 `x-codex-turn-state` | `codex-rs/codex-api/src/sse/responses.rs:66-72` |
| 票回带 | 同 (账号, 会话线程) 的续请求回带；TTL 过期不再回带；**无线程头（`x-client-request-id`/`session-id`/`session_id` 皆缺）不捕获、不回带（fail-closed）**——OpenAI 兼容 API 调用方通常不带线程头，账号级回带会把 A 会话的 turn-state 注入 B 会话请求，官方协议不允许 | `codex-rs/core/src/client.rs:2495-2513` |
| 共享票剥离 | 宿主/下游注入的 `x-codex-turn-state` 一律剥掉（跨账号共享票违反官方契约） | `client.rs:291-307` 契约注释 |
| cookie jar | per-account，仅官方白名单（`__cf_bm`/`__oailb`/`__cflb` 等，绝不持久化认证态） | `http-client/src/chatgpt_cloudflare_cookies.rs` |
| 粘性连接池 | (账号, 代理) 维度复用连接；代理无效时**拒绝直连**（出站 IP 红线） | 官方 ResponsesConnectionKey 语义 |
| 降智观测 | 记录 `x-codex-safety-buffering-enabled/-faster-model`、请求模型 vs 实际服务模型（`openai-model`）不一致计数，快照进 Health.status_json 与配置 UI 状态页 | 运行时证据 `notes-client-runtime.md` |
| **WS 桥（Phase 2，默认关）** | `ws_bridge_enabled: true` 后 POST /responses 走官方 WS 通道：`generate=false` 预热铸票（连接建立即铸）→ 票注入每条 `response.create` 的 `client_metadata`（帧级回带）→ `response.metadata` 事件捕获 → 事件流转 SSE 回宿主；失败自动回退 HTTP | `client.rs:2117-2119`（帧级回带）、`responses_websocket.rs:849-853`（事件捕获）、`client.rs:2187`（generate=false）、官方测试 `agent_websocket.rs:564-642` |

**诚实边界**：降智是服务端路由决策，任何客户端都无法"关闭"它。本插件的立场是——让网关流量获得与官方客户端一致的粘性路由待遇（已知最佳缓解），并把降智变成**可观测、可归因**的指标。实际效果需按 §灰度 实测对比（对比开启前后 `model_downgrades` / `safety_buffering_responses` 占比）。

## 构建 / 测试 / 打包

```bash
./build.sh                 # vet + 单测 + 当前平台二进制 + dist/codex-bridge.s2plugin
GOOS=linux GOARCH=amd64 ./build.sh   # 追加交叉平台运行时
go test ./... -count=1     # 单元测试
go test . -count=1 -v -run TestPluginProcessEndToEnd   # 进程级集成测试（真实 go-plugin 握手 + Forward 双向流）
```

## 安装

1. 管理面「插件管理」上传 `dist/codex-bridge.s2plugin`，先保持停用；
2. 未签名包需在宿主配置临时开启 `plugins.allow_unsigned: true`（仅限隔离环境，装完即关）；
   生产签名：`go run ./tools/packager -keygen build/keys/my-publisher` 生成密钥，`./build.sh -signing-key build/keys/my-publisher.private -key-id my-v1`，公钥写入宿主 `plugins.trusted_publishers`；
3. 启用并绑定 OpenAI OAuth 账号灰度（先单账号）。命中的账号：HTTP 路径经插件出站；下游 WS 客户端会被宿主自动桥成 HTTP 过插件（`forceHTTPBridge`）。

## 配置参考（snake_case，空对象=默认）

| 字段 | 默认 | 说明 |
|---|---|---|
| `manage_turn_state` | true | 插件接管票：剥离外部注入 + 捕获 + 同线程回带；无线程头的请求 fail-closed（不捕获、不回带） |
| `turn_state_ttl_seconds` | 300 | 回带窗口（30–3600）；近似官方 per-turn 边界 |
| `cookie_jar_enabled` | true | per-account 官方白名单 cookie jar |
| `max_idle_conns_per_account` | 8 | 每池每主机空闲连接上限（1–64） |
| `idle_conn_timeout_seconds` | 120 | 空闲连接回收（5–600） |
| `dial_timeout_seconds` | 15 | 拨号+TLS 超时（3–120） |
| `response_header_timeout_seconds` | 600 | 等待上游响应头超时（30–3600），不限制流式 body |
| `observe_downgrade` | true | 降智观测（安全缓冲头 + 模型比对） |
| `model_sniff_bytes` | 4096 | 请求体首段扫描窗口，提取请求模型（0=关闭） |
| `ws_bridge_enabled` | **false** | 开启上游 WS 桥（Phase 2 全量契约通道；建议 HTTP 模式灰度数据出来后再开） |
| `ws_prewarm_enabled` | true | 每条新 WS 连接先发 generate=false 预热帧铸票 |
| `ws_handshake_timeout_seconds` | 15 | WS 拨号+握手超时（3–60） |
| `ws_event_idle_timeout_seconds` | 300 | 流内相邻事件最大间隔（30–3600） |
| `ws_max_pool_size` | 4 | 每 (账号, 代理) 空闲 WS 连接上限（1–16） |

## 账号安全红线（内置实现）

- 剥离跨账号共享票（协议错误信号，官方封号相关）
- 代理无效即失败，绝不直连（出站 IP 一致性）
- 错误消息截断 URL 查询串；不落 Token/代理凭据/敏感头
- cookie 只存基础设施白名单，认证态不入 jar

## 后续阶段（见 PROJECT_STATUS.md）

- ~~Phase 2：插件内上游 WS 桥~~ **已实现**（`internal/wsbridge/`，默认关，`ws_bridge_enabled` 开启；单测 + 进程级集成测试覆盖预热铸票→帧级回带→SSE→连接复用→HTTP 回退全链路）
- Phase 3：单账号实测——重点悬空项"网关出站形状下上游是否对 prewarm 铸票"；对比 HTTP 模式与 WS 模式的 `model_downgrades` 占比后按 `rolloutPercent` 放量

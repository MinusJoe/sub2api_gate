# sub4api × Codex 逆向与优化 —— 全程工作记录

> 记录日期：2026-10-25 ｜ 分支：dev ｜ 涉及会话：多轮连续会话（含并行会话的部分改动已注明）
> 本文是完整的项目级存档：目标、分析、进展、产出物、未完成项、实现状态与效果。

---

## 1. 用户要实现什么（目标全景）

| # | 目标 | 提出背景 | 当前状态 |
|---|---|---|---|
| G1 | **解决首字延迟(TTFT)与输出 TPS 慢** | sub4api 网关代理 Codex OAuth 账号，体感慢 | 部分实现（5 项零风险优化已落地），根治方案待实施 |
| G2 | **逆向 Codex CLI 找"打票"的替代方案** | 主动打票探针（伪造请求换 x-codex-turn-state）已失效 | 方案已定（P0 官方契约对齐桥接），未实施 |
| G3 | **找出 Codex CLI 的 bug 报告官方** | 配合 G2 的负责任披露 | 1 个主候选（B1）+ 渠道方案已备，未提交 |
| G4 | **把替代方案做成独立插件** | 不污染网关核心、可独立灰度/回滚/复用 | **Phase 1 已实现并通过进程级集成测试**（`plugins/codex-bridge/`，见 §5.1）；Phase 2（WS 预热桥）待做 |
| G5 | **硬约束：不得引发账号安全问题（别封号）** | 贯穿所有方案 | 全部方案已按此约束设计（跨账号共享票判死、上游可见变化逐项标注+灰度） |

## 2. 分析了什么（按阶段）

### 阶段 A：Codex CLI 逆向 + 网关瓶颈排查（前序会话）
- **codex.exe 0.163.x 静态逆向**（336MB，无 Ghidra，用自写字符串/xref 扫描）：36 条二进制事实。核心发现：`x-codex-turn-state` 读点 2 处、回带写点唯一（当时判定仅 WS，后被 0.164 源码修正，见阶段 C）、turn-metadata 28+ 字段、`x-oai-attestation` 设备级、`__oailb` 客户端零感知。
- **建连成本实测**：直连 TCP+TLS≈200ms、完整往返 410-430ms、冷 TCP 515ms、住宅代理 ×2-3 → **WS 慢的根因 = 冷账号现场拨号 + 代理链路建连成本**（WS 池本身完善：min4/max12、30s ping、90s 回收，但 `ensureTargetIdleAsync` 有 `lastAcquire==nil` 短路，冷账号首请求仍现场拨号）。
- **网关链路 15 条瓶颈候选**，4 条 high 亲自核实。
- 产出：`scratch/REPORT-codex-{ticket-alternatives,solution-final,countermeasures-and-alternatives}.md` + 11 份 notes-*.md。

### 阶段 B：另一对话的代码审查 6 项发现逐项验证（本会话）
- **P1-1 旧票据覆盖新票据：属实，已修**。竞态：DB 读到旧票 T1 → 打票保存并发写入新票 T2 → T1 无条件 Store 盖掉 T2。修复：CapturedAt 新者胜出。
- **P1-2 cachedAt 数据竞争：属实，已修**。`e.cachedAt = ...` 原地改已发布指针，与热路径并发读构成 time.Time 多字竞争。修复：删除整个 cachedAt 字段，新鲜度改存独立 `openaiCodexTicketFresh` 表（不可变值、只整体替换）。
- **P2-6 一小时客户端缓存不生效：属实，已修**。`config.go:2579` viper 默认 900 压掉了 repository 常量 3600（死代码）。修复：viper 默认同步 3600。
- **P2-5 usage 预筛漏记：误报**。`strings.Contains(payload, "usage")` 是 JSON 键的严格必要条件，不可能漏。
- **P2-3 统计失败拖垮列表 / P2-4 CONCURRENTLY 索引失败后无法重建：属实**，属另一并行会话的文件，只出判定未改动（P2-4 修法：仿 migration 120 先 `DROP INDEX CONCURRENTLY IF EXISTS`）。
- **修复中额外发现并修复一个自引入缺陷**：`lookupOpenAICodexTicket`（快照预置路径）复用残留 fresh 时间戳把未确认快照误判新鲜 → 该路径写入时撤销 fresh 标记（测试 `TestCodexDiagnosticAndNormalRequestsUseSameTicketPolicy` 抓住，修复后全绿）。
- 验证：build/vet/gofmt 干净；票据目标测试全过；service 全量 **ok 143.9s**；2 个预存在失败已用 git worktree 基线法证明与改动无关（`cmd/codex-ticket-hypothesis` Windows 环境问题、`internal/config` D 盘位置相关问题）。

### 阶段 C：0.164 源码级全量定位 + bug 猎捕（本会话）
- 新二进制扫描：`codex.exe` 354MB（+18MB），**0.164.0-alpha.11.1**（2026-10-24 构建），机制全在 + 新增头：`x-codex-guardian`、`x-codex-noise-stream-id`、`x-codex-rendezvous-rejection-reason`、`x-codex-parent-thread-id`+`x-openai-subagent`、`x-codex-server-id`/`protocol-version` 等。
- **下载同 tag 源码快照**（`rust-v0.164.0-alpha.11.1`，18MB）到 `scratch/src/`，机制定位全部升级到 file:line：
  - 读票 3 通道：HTTP 响应头（`sse/responses.rs:66-72`）、WS 握手头（**死代码**，所有调用方传 None）、WS 事件流 `response.metadata` headers（`responses_websocket.rs:849-853`）
  - 回带 4 点：HTTP 头（`client.rs:1501`）、WS 头、**WS 请求体 client_metadata（`client.rs:2117`，旧结论没有的通道）**、compact（`session/turn.rs:437,623`）
  - 契约原文（`client.rs:291-307`）：per-turn、跨 turn 复用"violates the client/server contract"、auth 换主清票
- **修正 3 个旧结论**：①HTTP 同 turn 也回带票（测试 `turn_state.rs:60-70` 坐实，0.163 的"仅 WS"是错的/过时的）②attestation 非 WS 专属（HTTP 也注入，`client.rs:1510`）③101 握手头捕获是死代码。
- **Bug 猎捕**：B1（主候选）= turn-state 用 `OnceLock` 写一次，但三个捕获点对每个响应都尝试写 → 服务端中途换票被静默丢弃、客户端继续回带旧票（官方测试只测了首令牌场景，未测轮换）。B2（问询级）= 0.163/0.164 行为漂移待核。B3 = guardian-v2 新面未审。
- 披露渠道：仓库 `SECURITY.md` 指定 **Bugcrowd**（bugcrowd.com/engagements/openai）安全类；功能类走 GitHub issues 附复现测试草案。
- 产出：`scratch/REPORT-codex-bug-disclosure.md`。

### 阶段 D：多子agent 深挖打票替代方案（本会话）
- 用户指定 glm-5.3-flash（后经 GUI 改为 GLM-5.3$max、并发 8），用 dynamic-workflows 三路侦查 + 每条发现独立复核 + 汇总。
- 过程波折：第一次 3 并发 Agent 全被 `model concurrency limit exceeded` 拒（并行会话占额度）→ 亲自执行阶段 C；workflow 跑完 38/40 步后 `artifact.file` 时序/路径问题报错 → 修复脚本（探测工作区根→验证文件→优雅降级）Amend 复用缓存完成。
- **成果：33 条发现，28 条独立复核通过**（复核员逐行亲核引用、修正多处行号、推翻 5 条并给出证伪过程）。约 2.63 亿 token。
- 产出：`scratch/REPORT-codex-ticket-alternatives-v2.md`（47KB，含 11 项修补轮全量亲核）。

### 阶段 E：插件化调研（本会话）
- 评估三种形态（独立桥接服务/铸票代理/进程内模块），推荐形态 A。
- 随后**发现 sub4api 自带完整插件系统**，形态 A 有官方实现：
  - `.s2plugin` 独立进程包（签名/信任发布者/管理面上传/生命周期管理）
  - gRPC 双向流协议（`pkg/pluginapi/v1`）：Start 帧（方法/URL/头/**代理URL**/账号元数据）+ body 分帧，响应流式回
  - 能力 `openai.oauth.outbound_transport.v1`：钩子在 `doOpenAIUpstream`（RoundTrip 级），插件回标准 `http.Response`，SSE 解析/计费留在核心
  - **WS 入口已内置切换**：命中插件账号 `forceHTTPBridge=true`（`openai_ws_forwarder_ingress.go:119`）
  - 灰度 `rolloutPercent`；文档 `docs/PLUGIN_DEVELOPMENT.md`（12 节）；集成测试 `TestPluginRuntimeIntegration`
- 结论：P0 四小件全部可在插件内自治实现，网关核心零改动。

## 3. 核心结论（一句话版）

**官方客户端从不"打票"**：票由上游按"每 turn 首个非增量请求"自然铸造（实测 98.6% 首请求带票、续请求 0），客户端只做事件级捕获 + 契约通道回带（WS=每帧请求体 `client_metadata`，HTTP=请求头；**从不在 WS 握手头回带**——网关现在恰恰做反了）。官方还有零成本铸票形态：**线程启动预热**（`generate=false` 的 WS 帧，无生成 token，官方测试逐项断言）。

**P0 推荐方案「官方契约对齐桥接」四小件**（在现役 HTTP→WS 桥或插件内）：
1. 帧级回带：票写进每帧 `client_metadata["x-codex-turn-state"]`（现状只写握手头且仅 dial 时）
2. 事件级捕获：从 `response.metadata` 事件取票（现状 v2 路径 0 处事件解析，依赖的握手头通道是官方死代码）
3. 打开预热：`prewarm_generate_enabled`（代码已实现且幂等语义与官方一致，默认 false）
4. per-turn 生命周期：修掉复用连接把陈旧握手票跨 turn 复用；用 `openai_codex_ticket_allow_without_ticket` 降级；验证后 `openai_codex_ticket_enabled=false` 下线探针

**判死项**：伪造票（客户端无铸造路径）、跨账号共享票（硬约束）、跨 turn 票硬塞（超契约）、guardian 免票/API-key 豁免/Etag 上行/attestation 伪造。

**账号安全**：四件中三件让网关更贴近官方形状（风险为负）；唯一新增可见行为=预热帧（官方同款）；唯一悬空项=网关出站形状下上游是否对 prewarm 铸票（服务端策略，需单账号实测）。

## 4. 产出物清单

| 文件 | 内容 |
|---|---|
| `scratch/REPORT-codex-ticket-alternatives-v2.md` | **主交付物**：打票替代方案决策报告（P0-P4+判死项+落地步骤+安全评估，33 发现/28 复核） |
| `scratch/REPORT-codex-bug-disclosure.md` | 机制源码地图（file:line）+ bug 候选（B1 主）+ 披露渠道方案 |
| `scratch/REPORT-codex-{ticket-alternatives,solution-final,countermeasures-and-alternatives}.md` | 前序三份报告（0.163 时代结论，部分已被 0.164 修正） |
| `scratch/notes-*.md`（16 份） | 运行时证据/二进制笔记/探针规格/社区时间线等 |
| `scratch/src/codex-rust-v0.164.0-alpha.11.1/` | 与本机二进制同版的官方源码快照（18MB） |
| `scratch/scan2_headers.py` | 新二进制头/关键字扫描器 |
| 代码改动（backend，已全量测试通过） | 见 §5 |

## 5. 已落地的代码改动（全部验证通过）

**性能优化 5 项**（前序会话实施、本会话补修）：
1. `openai_codex_ticket.go`：票据热路径内存缓存（10s TTL + 同代指针保留 + CapturedAt 新者胜出 + fresh 不可变时间戳表）
2. `codex_ticket_handler.go` + `routes/admin.go` + `openai_ws_pool.go`：WS 池指标暴露到管理 API（`GET /admin/accounts/codex-ws-pool-metrics`，adminAuth 后）
3. `http_upstream.go` + `config.go`：客户端缓存 TTL 900→3600（常量+viper 默认双修）
4. `openai_gateway_response_handling.go` + `apicompat`：SSE 主链路 delta fast-path（Observe 门控 + 单键提取 `ProcessTextDelta`/`ProcessReasoningDelta`）
5. `openai_gateway_cc_pipeline.go`：CC 回退路径 usage 字面量预筛

**测试修复 3 处**：票据缓存引入后三处测试的缓存清理适配 + lookup 预置撤销 fresh。

**注意**：并行会话（另一对话）另有 usage_log 聚合、前端 session TPS 显示等改动，不属本记录范围，但已评估账号安全（上游不可见，零风险）。

### 5.1 codex-bridge 插件（2026-10-25 已交付，G4 Phase 1+2）

`plugins/codex-bridge/` —— 能力 `openai.oauth.outbound_transport.v1` 的独立进程插件（.s2plugin，windows-amd64）：
- **票契约对齐（HTTP 通道，默认开）**：从上游 HTTP 响应头捕获 `x-codex-turn-state`，同 (账号, 线程) 回带（TTL 300s 可配）；**剥离宿主/下游注入的共享票**
- **WS 桥（Phase 2，默认关 `ws_bridge_enabled`）**：POST /responses 走官方 WS 通道——`generate=false` 预热帧铸票（连接建立即铸）→ 票注入每条 `response.create` 的 `client_metadata`（帧级回带，官方 WS 契约通道）→ `response.metadata` 事件捕获 → 事件流转 SSE 回宿主；(账号,代理) WS 连接池复用；失败自动回退 HTTP
- **per-account cookie jar**（官方白名单）+ **(账号,代理) 粘性 HTTP 连接池**（代理无效拒绝直连）
- **降智观测**：safety-buffering 头 + 请求模型 vs 实际服务模型比对计数（HTTP 头与 WS 事件双通道），进 Health.status_json 与插件 UI 状态页
- 完整 gRPC 传输协议（Forward 双向流、配置校验/热应用、3xx 透传）；自带打包器（可签名）与配置 UI
- **安全审计与加固（2026-10-25，主模型执行，flash 配额耗尽兜底）**：确认并修复 F1-high（cookie jar/连接池按 proxyURL 键控致同代理多账号共享 → 改 (账号,代理) 双键）、F2-medium（预热帧带全量 input → 剥离对齐官方空 input 形状）、F3-medium（业务帧写失败回退 HTTP 有双发风险 → ErrRequestSent 哨兵禁止重放）；六条红线全项核查通过；报告 `scratch/REPORT-codex-bridge-safety-audit.md`
- 验证：单测 5 包全绿（含新增 cookie 账号隔离测试；HTTP 回退为进程内单测覆盖：`internal/transport/server_test.go` 的 `TestForwarderWSBridgeFallsBackToHTTP`）+ **两个进程级集成测试**（真实 go-plugin 握手：HTTP 契约链路 + WS 桥全链路含预热铸票/帧级回带/连接复用）
- 局限（诚实声明）：降智是服务端决策，插件=契约对齐（已知最佳缓解）+ 观测；"网关形状下上游是否对 prewarm 铸票"仍是悬空项，需 Phase 3 单账号实测

## 6. 未完成 / 待完成（按优先级）

| 优先级 | 事项 | 依赖/说明 |
|---|---|---|
| **P0-1** | ~~透传插件脚手架（Phase 1）~~ **已完成**（2026-10-25，超出原计划：透传之上直接实现了票契约对齐+cookie jar+降智观测，见 §5.1） | 剩余：真实上游单账号灰度安装验证 |
| **P0-2** | ~~桥接内核 WS 件（Phase 2）~~ **已完成**（2026-10-25，`internal/wsbridge/`：预热铸票/事件捕获/帧级回带/连接池/HTTP 回退，单测+进程级集成测试全绿） | 剩余：单账号实测 WS 模式效果 |
| **P0-3** | 实测悬空项 + 放量（Phase 3） | 单账号验证"网关形状下 prewarm 是否铸票"，`rolloutPercent` 放量，然后 `openai_codex_ticket_enabled=false` 下线探针 |
| P1 | 早期性能梯队（未实施） | WS 冷账号预热（去 `lastAcquire==nil` 短路）、keepalive-first SSE 模式、forced_service_tier=priority、first_output_timeout 15-30s、粘性 TTL≥30min——待 WS 池指标/session TPS 观测数据后决策 |
| P1 | B1 bug 披露 | 先在源码树跑复现测试（mock server 同 turn 第二响应带不同票），然后 Bugcrowd 或 GitHub issues；报告不得携带网关运营细节 |
| P2 | 遗留审计 | guardian-v2/noise-stream/rendezvous 深层审计、卫星二进制（codex-code-mode-host 等）深扫、0.163 tag 复核 B2（HTTP 回带是否存在） |
| P3 | 环境问题（非本改动引入） | `cmd/codex-ticket-hypothesis` Windows 测试失败；`internal/config` 两个测试 D 盘位置相关失败（C 盘通过）；本机无 gcc（`go test -race` 不可用） |
| — | 另一会话的 P2-3/P2-4 | usage 列表降级、索引 DROP-IF-INVALID 重建（在对应会话修） |

## 7. 最终实现状态与效果

| 目标 | 实现？ | 效果 |
|---|---|---|
| G1 TTFT/TPS | **部分** | 5 项零风险优化已合入 dev 并全量测试通过（票据 DB 查询消除、SSE 解析 CPU 降低、连接复用延长）；**量化效果未实测**——需部署后观察 `/admin/accounts/codex-ws-pool-metrics`（复用率/排队毫秒）与 session TPS。根治（冷账号预热、桥接）待 P0 |
| G2 打票替代 | **方案完成，未实施** | 根因链闭环（探针形状不被认可 + 长度假设过时 + 契约通道做反）；P0 四小件有 file:line 级实施依据与灰度路径 |
| G3 报官方 bug | **候选就绪，未提交** | B1（OnceLock 轮换缺陷）有源码+测试盲区证据与复现草案；渠道=Bugcrowd/GitHub |
| G4 插件化 | **Phase 1+2 已实现** | `plugins/codex-bridge` 交付：HTTP 契约对齐（默认开）+ WS 桥（预热铸票/帧级回带/事件捕获，默认关）+ 共享票剥离 + cookie jar + 降智观测；单测 5 包 + 两个进程级集成测试全绿；Phase 3 灰度实测待用户操作 |
| G5 账号安全 | **持续满足** | 所有已实施改动上游不可见；P0 方案 3/4 件让网关更贴近官方形状；跨账号共享票判死 |

**诚实的边界**：33 条发现中 5 条未通过复核（已单列，不影响 P0 推荐）；服务端行为（是否对 prewarm 铸票、是否校验 turn 归属）静态不可证，只能单账号实测；性能优化的量化收益尚无部署后数据。

## 8. 账号安全红线（所有后续工作必须遵守）

1. 禁止连接数放大、出口 IP 跳变（全链路同账号同出口 IP，含 token 刷新/打票/插件出站）、机械心跳
2. 多账号禁共享代理 IP；请求头地理配套
3. 跨账号共享票 = 判死（协议错 + 封号风险）
4. 上游可见的行为改动必须灰度（单账号→1%→10%→50%）
5. 禁止读取 `~/.codex/auth.json` 等任何凭据文件
6. 插件必须使用网关传入的 `proxyURL` 出站；日志不得落 Token/代理凭据/敏感头（插件文档红线）

---

## 9. 增量：2026-10-25 降智攻坚（多 agent 工作流，接续 §1-§8）

> 主交付物：`scratch/REPORT-codex-downgrade-warroom.md`（35 条复核确认发现 + 13 项修复 + 5 项增强 + 未验证清单 + §⑧ 主会话事后核验补记）。本节只记增量要点。

**降智机制定论**（逆向桌面版 26.1021.3705.0 内置 codex.exe=0.164.0-alpha.11.1，与源码快照精确同版；35/35 发现独立复核确认、0 证伪）：
- 降智三信号通道：served-model 只走 `openai-model`/`x-openai-model` 头（响应体无 model 回显）→ 官方 `maybe_warn_on_server_model_mismatch` 检测后发 ModelReroute 事件、无自动重试；`safety_buffering` 双头+metadata 事件（含 retry_model，官方唯一降级重试通道=用户确认后 fork 新线程用快模型+effort=Low 重发）；`service_tier` requested vs active 可观测（priority→flex 降档）。
- 官方明路由信号：`x-codex-routing-hint: model={model};tier={tier}` 每请求注入（HTTP+WS 握手）；`service_tier` 客户端可设（Fast→priority，FastMode 默认开）——网关可对齐的官方形状（上游可见，须灰度）。
- 票机制闭环：turn-state=服务端 sticky routing token、客户端只收首值回显；owner 变化弃票；model/tier 变更使 WS 增量续用失效退全量；预热=generate=false 零推理连接准备。
- 模型目录已换代 6.x（无 gpt-5.3-codex/gpt-5.2），全模型 use_responses_lite=true + priority=Fast。

**已落地（全部过确定性测试门）**：
1. 插件+宿主 13 项缺陷修复（12 实现 + S2 亲读官方源码确认无需修）：线程键断层双向打通（threadKey 三回退+白名单放行 x-client-request-id）、body 票穿透、管道泄漏（负向对照验证）、SetReadLimit 16MiB、连接池活性+TTL+retired、握手头剥票、capture 竞争删除、预热剥链锚、Prune 接线、idle 显式报错、文档纠偏。插件 5 包 + backend service 全量（153s）绿。
2. 5 项降智增强（全部默认关/observe-only，上游不可见）：served-model 变体补检、冷却指数退避、下游透明头（x-sub4api-codex-served-model/degraded）、限流/余额头族被动观测、管理端降智指标面板（/admin/accounts/codex-degradation/metrics）。
3. 终审缺陷 B1（observer 生命周期：零信号尝试计样、回退信号丢失）/B2（fail_closed 票务锁死）已修：headersObserved/eventObserved 门控、turn_state_mode=relay/harvest/off、新增 `codex_ticket_recycle.go` 业务响应头回收票（**P0 方案 A 落地**：只收上游真实下发票、按账号键控、不跨账号）。
4. 同日追加：下游透明头接线补全至 **10 处**（主链路 3 + passthrough 3 + WS v2 流式 1 + cc/messages 共用流式闭包 1 + cc/messages 非流式 JSON 2）+ buffered 聚合路径事件观测补齐 2 处（readOpenAICompatBufferedTerminal 原先不喂降智 observer，GLM-5.3 评审抓出后修复）——评审两轮终判 approved。WS-ingress/sink 分支经评审确认不适用（下游是 WS 连接或内存管道）。
5. **W4 竞争探测闭环**：安装 mingw-w64（~/.zcode-tools/mingw64，gcc 16.2.0）后 `go test -race` 插件全量 **5 包全绿**（含两个进程级集成测试）——capture 竞争消除从"结构证据"升级为"race 探测器验证"。

**环境事实更新**：go 工具链恢复（系统 D:\envs\Go + 便携 C:/Users/zfx/.zcode-tools/go，均 1.27.3）；C 编译器已装（C:/Users/zfx/.zcode-tools/mingw64，winlibs gcc 16.2.0，`-race` 可用且已跑通）；`.gitignore` 忽略 /plugins/ 与 /scratch/（插件代码与报告不受 git 保护，待用户决定是否纳管）。

**未验证（Phase 3 单账号实测，优先级取代 §6 已完成的 P0-1/P0-2）**：网关形状下 prewarm 是否铸票；回带票是否被上游采纳；降智触发/解除条件。性能量化未做。

**同日第二轮收尾**：
- B2 修复完整复查（独立评审 approved）：relay/off 在调度阻塞（openai_codex_ticket.go:488-491）与注入（:389-392）两侧同口径放行；recycle 方案 A 全链闭合（业务响应头→Fernet 双重校验→(账号,模型) 键控→accounts.extra 原子合并，与 harvest 读侧同存储）；红线全守住；28 窄测试过。**4 项残留须知**：①显式 harvest+fail_closed+probe_enabled=false 冷启动仍锁死（有意保留，配置文档未警示）②WS 传输流量不参与回收（relay 默认不受影响）③relay 下 fail_closed 实际失效、管理端 Blocked 显示与行为不一致④回收 60s 限频在写库失败时也消耗窗口。
- backend `go test -race` 全量首次跑通：3 处竞争全部定位在**并行会话的测试代码**（content_moderation_cyber_test.go:264 构造后改字段 vs worker 读 :1570；usage_cleanup_service_test.go:68 stub 计数器 vs :583/:611 Eventually 读），本次改动文件零竞争；修法已明确（测试侧原子化/先配置后启动），留对应会话处理。
- 降智透明头第 11 处（SSEToJSON 协议错误分支）补齐，全量回归门第四轮 exit 0。
- 插件重打包：dist 旧包为修复前产物 → 已重建并 Ed25519 签名（key_id=codex-bridge-v1，公钥 FEKM4LP+bJhNBve6cO3ScMrXEzvZS6PvGCphQhfu1s0=，私钥 plugins/codex-bridge/signing-key.private 需用户备份）。宿主 TestPluginRuntimeIntegration 为通用示例插件设计（签名+示例配置 schema 断言），不适用 codex-bridge，非缺陷。

## 9. 2026-10-25 本轮：降智治理 + 打票替代（实现与验证）

- 完整交付与诚实边界见 `scratch/team2/FINAL-REPORT.md`；执行计划 `scratch/team2/PLAN.md`；REA 更新 `scratch/team2/re/REPORT.md`。
- **降智**：从 ModelTrace 探针改为真实流量权威信号（`OpenAI-Model`、`x-codex-safety-buffering-enabled/-faster-model`、`response.metadata{type:"safety_buffering"}`、`response.model`），覆盖 HTTP/SSE、passthrough、WSv2、messages、chat_completions；默认 `observe_only=true`，指标走 `GET /admin/accounts/codex-degradation/metrics`；显式关闭 observe_only 后用调度器真实模型 scope 冷却 (账号, 模型)。
- **打票**：新增 `turn_state_mode=relay(默认)/harvest/off` 与 `probe_enabled`（默认 false）；业务响应头回收票（零探针，`business_response_v1`，60s 防抖）；TTL/形状校验生效；cookie-only 不注入。
- **REA**：以 REA 6.7.0 对 Codex Desktop 26.1021.3705.0（asar SHA-256 `04862a65…`，core 仍 0.164.0-alpha.11.1）做 inventory/模块/fuses 证据；确认降智/票逻辑不在 Electron JS 壳。无 Ghidra，未反编译 core。
- **验证**：主工作树 `go build ./...`、`go test ./internal/service`（147.6s）、admin/routes 全绿；`internal/config` 2 个预存在失败已用 HEAD 基线复现，与本轮无关。
- **未完成**：无生产灰度数据；WS ingress/WS passthrough 两条路径未接降智 hook；未做前端 UI；6 个子agent 因环境限制全部失败（无独立评审），本轮由 Lead 完成并自查修复 4 个问题。
- 主工作树改动未提交（因存在另一并行会话的未提交改动）；干净可合并版本在分支 `codex-degradation`（提交 `352c18c4f`、`002256865`）。

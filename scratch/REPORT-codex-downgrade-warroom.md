# Codex 降智作战室 · 最终报告

- 生成日期：2026-10-25
- 仓库：`D:\projects\sub4api`（dev 分支）
- 报告性质与诚实声明：本报告由「总结撰写人」根据本次作战室流程各 ask 返回的结构化结果（fixes / findings / enhancement / review）汇编而成。**其中全部证据（命令、输出、file:line、二进制扫描计数）均为前序各 ask 亲自执行所得，本 ask 未重新执行任何测试、扫描或代码核对**，仅做转录与归纳。输入数据中被截断的字段以「〔输入截断〕」标注，报告不补全、不推测。

---

## ① 背景与目标

**降智问题（一句话）**：上游会按账号风险等条件将 Codex 请求静默路由到降级模型或降低服务档位（served-model 与请求模型不一致 → `ModelReroute`、`safety_buffering` 缓冲通道、`service_tier` 降档，见 ③ rea-3/rea-4/rea-5/src-2），官方客户端靠 `openai-model` 响应头自检并弹警告，而本网关此前既不观测、不判定、也不向下游透出任何信号——用户「付最贵价格拿到降级模型」完全无感知。

**本次任务范围**：
1. 逆向桌面客户端（MSIX `OpenAI.Codex 26.1021.3705.0` 内置 CLI `0.164.0-alpha.11.1`）与同版源码快照（`scratch/src/codex-rust-v0.164.0-alpha.11.1/codex-rs/`），确认降智信号通道与票（turn-state）机制；
2. 修复插件桥接层（codex-bridge）与宿主侧（backend）已确认的缺陷：线程键打通、body 票穿透封堵、管道泄漏、WS 读上限/连接池/握手头/流中票归属/预热帧/退役池/空闲超时、turnstate 修剪、文档表述归位；
3. 实施 5 项获批增强（served-model 检测盲区、冷却指数退避、下游透明头、配额头族被动观测、管理端指标面板）；
4. 明确未验证事项，划定 Phase 3（单账号真实流量实测）边界。

---

## ② 已落地修复

> 证据列中的 `forwarder.go` / `bridge.go` / `server.go` 均为 codex-bridge 插件内文件（路径按输入转录）；`openai_gateway_*`、`config.go` 为 backend 文件。

| 编号 | 标题 | 是否实现 | 证据（前序 ask 亲自取得） |
|---|---|---|---|
| T1 | 线程键双向打通：插件 threadKey 三回退 + 宿主两白名单放行 x-client-request-id | ✅ | forwarder.go:227-237 threadKey 三回退；forwarder.go:35 常量；openai_gateway_service.go:87-89 / :109-110 白名单。`cd plugins/codex-bridge && go test ./internal/transport/ ./internal/wsbridge/ -count=1` → both ok；`go test ./internal/transport/ -run 'TestThreadKeyFallbacks\|TestForwarderTicketScopedByUnderscoreSessionID' -v` → 5+1 全 PASS；`cd backend && go test ./internal/service/ -run TestOpenAIWhitelistsCarryClientThreadID -v` → --- PASS（non-passthrough/passthrough 两子用例） |
| T2 | body 票穿透封堵：BuildCreateFrame 无条件 Set/Del + 缓冲路径剥离外来票 | ✅ | bridge.go:294-330 BuildCreateFrame 无条件 Set/Del；bridge.go:333-357 StripClientMetadataTicket；forwarder.go:280-284 缓冲路径剥离。`go test ./internal/wsbridge/ -run 'TestBuildCreateFrameForeignTicketHandling\|TestStripClientMetadataTicket' -v` → 7 子用例全 PASS；`go test ./internal/transport/ -run TestForwarderWSFallbackStripsBodyTicket -v` → --- PASS |
| T3 | 管道泄漏修复：Do 失败统一 CloseWithError 释放 drain goroutine（含负向对照验证） | ✅ | server.go:128 pipeReader 提升；server.go:150-155 统一 CloseWithError。`go test ./internal/transport/ -run TestForwardDoFailureReleasesDrainGoroutine -v` → --- PASS (0.02s)；**禁用修复后同命令 → FAIL Condition never satisfied (5.78s)**（负向对照）；恢复后 `go test ./internal/transport/ ./internal/wsbridge/ -count=1` → both ok；`grep TEMP-NEGATIVE-CONTROL plugins/` → 无输出（对照残留已清理） |
| W1 | Dial 后设读上限 ws_read_limit_bytes（默认 16MiB，越界 1009 断流修复） | ✅ | coder/websocket v1.8.14 read.go:107 `defaultReadLimit=32768`（亲读模块缓存源码确认）；新测试 TestBridgeWSReadLimit（bridge_test.go）：40KB 单条消息默认配置完整透传 PASS、`cfg.WSReadLimitBytes=1024` 时流以"读事件"错误收尾 PASS。⚠️ 断流路径依据库源码推定，未对旧代码复跑复现断流 |
| W2 | acquire 活性检测 + 空闲 TTL 弃用；pool.limit 读写统一 p.mu | ✅ | 关键前提：coder/websocket v1.8.14 未导出 `Closed()/IsClosed()`（`go doc github.com/coder/websocket.Conn` 输出无此方法，仅未导出 isClosed，close.go:328），故用自有 dead 标记等价实现；对端在池内静默关闭无法被动探测（Ping 属上游可见机械流量，违反红线，不用），由 TTL 兜底。测试：TestConnPoolClosedConnNeverReused（归还前已判死→release 不入池；池内判死→acquire 跳过并弃用，两子测试 PASS）、TestConnPoolAcquireEvictsIdleConn（超龄弃用+未超龄复用 PASS）、TestConnPoolSetLimitVisible PASS |
| W3 | WS 握手头剥离 x-codex-turn-state | ✅ | TestHandshakeHeaderFiltersAndSetsBeta 扩展断言 `src.Set("x-codex-turn-state")` 后 out.Get 为空 PASS（bridge_test.go）。官方依据（client.rs:1425 turn_state=None）来自上级 ask 提供材料，修复 ask 当时未亲读官方源码（后续 S2/src 系列 ask 已亲读同版源码快照，覆盖 turn_state 行为，见 ⑥-6） |
| W4 | 删除 streamEvents 对 capture.Ticket/PrewarmTicket 的并发直写 | ✅ | 结构证据（亲自执行）：sed 提取 streamEvents 函数体 grep `capture.` → 仅 capture.Live.finish()/setTicket/setServed 三处方法调用，无字段直写；行为测试 TestBridgeMidStreamTicketGoesToLiveFields PASS（业务流中 metadata 票进 Live 与 store，返回值 capture.Ticket 保持 Request 返回时刻快照，SSE 不含 metadata）。⚠️ `-race` 在本机不可运行（CGO 需 gcc，PATH 无任何 C 编译器，已尝试 which gcc/cc/clang/mingw 均无），race 探测器层面未验证，以结构证据代替 |
| W5 | 预热帧剥离 previous_response_id（官方预热不带链锚） | ✅ | TestBridgePrewarmDropsPreviousResponseID PASS：含 `previous_response_id:"resp-old"` 的请求，录得预热帧（frames[0]）无该键、业务帧（frames[1]）保留 "resp-old"（require.Eventually 轮询帧记录）。官方依据（client.rs:2169-2185 continuation=None）来自上级 ask 提供材料，修复 ask 当时未亲读官方源码（同 W3 说明） |
| W6 | connPool 增 retired 原子标志，release 对退役池关连接不入池 | ✅ | TestRetiredPoolReleaseClosesConn PASS（retire 后 release 的连接判死、acquire 为 nil、池空）；TestBridgeCloseIdleConnectionsThenRequest PASS（换池后重建连接继续服务） |
| W7 | 事件空闲超时以 CloseWithError 收尾，宿主可见中断 | ✅ | TestBridgeIdleTimeoutSurfacesInterruption PASS：上游回 created 后静默，`cfg.WSEventIdleTimeoutSeconds=1`，`io.ReadAll(resp.Body)` 返回错误且消息含"空闲超时"（旧实现此处为干净 EOF，宿主会当成完整响应） |
| S1 | turnstate Capture 超阈值(4096)锁内顺带 Prune，条目不再只增不减 | ✅ | 全仓调用方核查：`grep -rn "\.Prune()" plugins/ backend/` 仅命中 store_test.go:50（测试自身）。`go fmt ./internal/turnstate/`（无输出=gofmt 干净）；`go vet ./internal/turnstate/`（干净）；`go test ./internal/turnstate/ -run . -v` → TestCaptureReplayWithinTTL / TestKeysAreAccountScoped / TestCaptureEmptyIgnoredAndPrune / TestCaptureTriggersPruneAtThreshold 4/4 PASS (ok, 0.675s) |
| S2 | 预热读窗 terminal 即停与官方行为一致，确认**无需**加晚到 metadata 等待窗 | ❌（确认无需实现） | 亲读官方源码 `scratch/src/codex-rust-v0.164.0-alpha.11.1/codex-rs/`：codex-api/src/sse/responses.rs:211-217（turn_state() 仅认 response.metadata）、:281-290（header 提取）；codex-api/src/endpoint/responses_websocket.rs:162（常量）、:849-853（OnceLock.set）、:866-869（completion 捕获）、:923-939（terminal 同迭代 break）；core/src/client.rs:2365-2375（预热消费者首个 Completed 即 break）、:2117-2118 与 :2506-2510（.get() 非阻塞消费）；core/tests/suite/agent_websocket.rs:567-572（mock 编排 metadata→completed）、:640-642（回带断言）。grep agent_websocket.rs 全文件 response.metadata/x-codex-turn-state：metadata 仅出现于 :569 一处，**所有用例均无 metadata 晚于 completed 的编排** → 官方无等待窗，无需实现 |
| S3 | PROJECT_STATUS.md 验证条目改为客观表述：HTTP 回退归位进程内单测 | ✅ | 进程级范围核查：`grep -n "^func Test" plugins/codex-bridge/integration_test.go` → 仅 :95 TestPluginProcessEndToEnd 与 :255 TestPluginProcessWSBridge；亲读 :255-295，断言含 "ws_prewarm_tickets":1、client_metadata["x-codex-turn-state"]、frames==3（1 预热+2 业务），无任何回退路径注入。回退单测实测存在：internal/transport/server_test.go:238 `func TestForwarderWSBridgeFallsBackToHTTP` |

### 测试与评审结果

**主会话已验证基线**：插件 5 包测试绿；backend `./internal/service/` 全量绿（153s）；双端编译绿（go1.27.3）。

**修复批次自报**：pluginTestsPassed = true；backendTestsPassed = true。

**最终评审**：finalReviewApproved = true（通过），但评审记录了两个**未解决的开放缺陷**，通过不代表二者已修复：

- **B1 — 降智观测 observer 生命周期缺陷（默认 WS 部署下指标系统性错误）**：
  - (a) `backend/internal/service/openai_ws_forwarder_v2.go:45-46` 在 attempt 顶部 begin+defer finalize，build_ws_url 失败（:49-51，未触达上游）也 finalize；`recordCodexDegradation`（openai_codex_degradation.go:353-444）无 headersObserved/eventObserved 门控，把零信号的失败尝试记成 Degraded=false 样本；WS 重试循环（openai_gateway_forward.go:926-1035）每次失败注入一条假阴性。
  - (b) WS 失败回退 HTTP 复用同一 gin context，`observeCodexDegradationHeaders`（openai_codex_degradation.go:210-228）同账号同模型时复用已 finalize 的 observer，而 `finalizeCodexDegradation`（:243-247）见 finalized=true 直接返回——**回退响应里的真实降智信号永不入库**。
  - 影响：openai_ws 默认开启，本次交付的 `/admin/accounts/codex-degradation/metrics` 默认即错；observe_only=false 时稀释比例误导冷却。测试无 WS 失败/回退生命周期用例。
- **B2 — 升级后 fail_closed 票务部署被调度器锁死**：`openaiCodexTicketBlocksAccount`（backend/internal/service/openai_codex_ticket.go:480-503）不检查 turn_state_mode；新默认 turn_state_mode=relay（backend/internal/config/config.go:2532）+ probe_enabled=false（:2531）下票无补充渠道——harvest 循环跳过刷新（openai_codex_ticket.go:706-710），业务回收仅 harvest 写库（backend/inter…〔**输入截断，B2 详文不完整，完整影响面未在报告层核实**〕）。

---

## ③ 逆向与挖掘确认发现

> 来源列：`rea-*` = 桌面客户端逆向（二进制扫描 + 本机文件取证）；`src-*` = 0.164 同版源码快照挖掘。**「复核结论」列摘自输入数据的 confirmVerdict 字段（复核由前序复核 ask 完成）**；多数复核详文在输入 JSON 中以「…」截断，本列仅依据其保留的结论句（如「复核成立」「成立，行号有 X 偏差」）归纳，未添加输入之外的判断。

| 编号（来源） | 结论 | 证据（file:line / 命令输出） | 与降智/票相关性（severity） | 复核结论 |
|---|---|---|---|---|
| rea-1（桌面逆向） | 桌面内置 CLI codex.exe = 0.164.0-alpha.11.1，与源码快照精确同版；卫星 exe 同 rustc 构建 | scan_ver.py 对 4 exe：codex.exe 30 处版本串（first@273645533；`version: 0.164.0-alpha.11.1`@274531004）；code-mode-host@45785592、sandbox-setup@13326773 各 1 处；command-runner 无版本串但同 rustc `/rustc/58cf8f9c…`（×71/77/86）；4 exe VS_VERSIONINFO 全空 | 票/基线（medium） | 成立。全部证据亲自重跑复现（定位 `C:/Users/zfx/AppData/Local/OpenAI/Codex/bin/2993767bd1dfb009/` 四 exe 及字节数、与 `~/.codex/plugins/.plugin-appserver/` 副本 md5 一致；详文输入截断） |
| rea-2（桌面逆向） | 桌面壳 MSIX = OpenAI.Codex 26.1021.3705.0（Store 签名），本体目录 ACL 受限；旧版本串 26.930.21537/prod 残留 | `Get-AppxPackage`：Name=OpenAI.Codex Version=26.1021.3705.0 InstallLocation=C:\Program Files\WindowsApps\OpenAI.Codex_26.1021.3705.0_x64__2p2nqsd0c76g0；`ls WindowsApps` Permission denied；chrome-native-hosts-v2.json：appVersion/cliVersion=26.930.21537、channel=prod、nativeHost com.openai.codexextension | 间接（环境基线）（low） | 三项证据均亲自复核成立（PowerShell 实跑，SignatureKind=Store、IsDevelopmentMode=False） |
| rea-3（桌面逆向） | **降智核心**：客户端主动检测 served-model（OpenAI-Model 响应头）≠请求模型 → 发 ModelReroute 事件 + 硬编码警告文案 | core/src/session/mod.rs:4210-4248 `maybe_warn_on_server_model_mismatch`（小写比较，不一致→EventMsg::ModelReroute{reason:HighRiskCyberActivity}）；:4227 硬编码文案 "routed to gpt-5.2 as a fallback…regain gpt-5.3-codex" + chatgpt.com/cyber(:558)；二进制证实 ModelRerouted×6、chatgpt.com/cyber×2、gpt-5.2×3 | **降智（high）** | 全部成立（两份源码快照 diff --strip-trailing-cr 完全相同，行号对两份均有效；二进制扫描确认同版；详文输入截断） |
| rea-4（桌面逆向） | safety_buffering = 服务端缓冲降智通道：双头 + 事件 JSON，含 retry_model 快模型与专属 UI | codex-api/src/safety_buffering.rs:5-8 双头 `x-codex-safety-buffering-enabled/-faster-model`（enabled=false 也不 gate，测试:48-62）；sse/responses.rs:246-262 response.metadata{type:safety_buffering}；common.rs:135-142 SafetyBuffering{use_cases,reasons,show_buffering_ui,retry_model} → tui/chatwidget/safety_buffering.rs:15 渲染；二进制 safety_buffering×14、faster_model×9 | **降智（high）** | 成立，5 处源码定位全部亲读、二进制计数亲测精确吻合（详文输入截断） |
| rea-5（桌面逆向） | service_tier 降档可观测：thread_processor 记录 requested vs active（priority→flex 实例） | app-server/src/request_processors/thread_processor.rs:132-148 逐项记录 model/service_tier/model_provider 的 requested=…active=…；thread_processor_tests.rs:767 锚定 requested=Some("priority") active=Some("flex")；ServiceTier={Fast,Flex}（protocol/src/config_types.rs:528-531）、"default"=标准档哨兵(:537) | **降智（high）** | 成立（行号有小偏差需修正，不影响结论） |
| rea-6（桌面逆向） | 模型目录已换代 6.x：无 gpt-5.3-codex/gpt-5.2；全部模型 use_responses_lite=true 且带 priority=Fast tier | `~/.codex/models_cache.json`（红线允许项，client 0.164.0@2026-10-24）：gpt-6.1-sol/astra/6-sol/reserve(hide)/6-luna/5.6-sol/terra/luna/codex-auto-review(hide)；service_tiers=[{id:priority,name:Fast}]，additional_speed_tiers=[fast]，available_access_programs={cyber:[standard]}；二进制 gpt-6-astra×23、gpt-6.1-sol×10、gpt-reserve×0 | **降智（high）** | 全部成立（复核者亲自 python json.load 解析 398,806B 文件并逐一核对 slug/priority/visibility；详文输入截断） |
| rea-7（桌面逆向） | 客户端降智/受限信号错误分类学：flexUnavailable、cyberPolicy、misalignmentPolicyViolation、tooManyDenials 均为可呈现错误类型 | sdk/python/src/openai_codex/generated/v2_all.py:528-545 CodexErrorInfoValue 全枚举；misalignment=公开解释块 MisalignmentErrorDetails（app-server-protocol/src/protocol/v2/thread_data.rs:434-442），挂 ErrorEvent.misalignment（protocol/src/protocol.rs:2203）→ app-server bespoke_event_handling.rs:1054,1570 透传 | **降智（medium）** | 成立（一处行号轻微偏差，不影响结论） |
| rea-8（桌面逆向） | 桌面→上游额外身份信号：每请求附 x-codex-installation-id、x-codex-window-id（thread_id:n 复合）、turn-metadata JSON 含 context_window_id | core/src/client.rs:162,167 常量 → core/src/responses_metadata.rs:333-338,384 挂头；:364 turn-metadata 同时进 client_metadata body；v2 client_metadata.rs:135-145 断言 installation_id/window_id/context_window_id(uuid)；:314 `window_id.split_once(':')` 证复合格式；guardian sampler 另用 thread_id:0（ext/guardian-v2/.../execution.rs:322） | 票（medium） | 成立，6 处源码定位全部亲读命中、行号精确 |
| rea-9（桌面逆向） | 三个卫星二进制零网络/降智面：票、头、safety_buffering 全 0 命中，路由行为只在 codex.exe | scan2_headers.py 输出（rea-desktop/scan2_codex-code-mode-host.txt 等 3 份）：turn-state/turn-metadata/attestation/routing-hint/safety_buffering/fast_mode/__oailb 全部 0；code-mode-host 仅 OTEL trace ws + http listen 串 | 降智+票（medium） | 成立（复核者亲自重跑扫描脚本，非仅读存档；详文输入截断） |
| rea-10（桌面逆向） | 票通道机制未变（同版复现）：turn-state×9 / turn-metadata×10 / attestation×4 / routing-hint×4 / responses_websockets=2026-02-06×6 | scratch/scan2_headers.py 复扫 bin/2993767bd1dfb009/codex.exe → rea-desktop/scan2_codex.txt:47-52,45；计数与 REPORT-codex-bug-disclosure.md §4 完全一致（同一 354,661,168B 文件，SHA256 前缀 1103E9E09C71FA2C） | 票（medium） | 亲自重跑扫描并逐项比对，全部成立（详文输入截断） |
| rea-11（桌面逆向） | 旧报告未枚举的头族补全：credits-balance / promo-message / rate-limit-reached-type / active-limit / ws-stream-request-start-ms | scan2_codex.txt 行 4,9,16,17,37-40 命中；源码：codex-api/src/rate_limits.rs（credits-balance/promo-message）、api_bridge.rs（active-limit）、client.rs:183（ws-stream-request-start-ms）；RateLimitReachedType 5 变体（protocol/src/protocol.rs:2497-2503）；均在 0.164 源码，非桌面独有 | **降智（low）** | 成立（两处行号引用有误，实质不受影响；复核者重扫+读存档核对） |
| rea-12（桌面逆向） | prewarm 仍单变体：WS response.create + generate=false，turn 首个 stream 请求前 best-effort，与 in-turn 重连共用握手头 | core/src/client.rs:17-25 注释（v2-only prewarm、best-effort）、:1331 startup prewarm 与 needs_new 重连共用路径、:1420 注释、:2067/2153 generate=false 非推理请求；未发现桌面新变体 | 票（low） | 成立（逐行亲读核对；详文输入截断） |
| rea-13（桌面逆向） | 壳层前端无降智逻辑可读：壳本体在 WindowsApps（ACL 拒绝），可读 server.mjs 仅 1 处 x-codex-turn-metadata | `ls 'C:/Program Files/WindowsApps/'` Permission denied；LocalCache/Roaming/Codex/web/Codex/executor-plugins/codex-app-tools/server.mjs（1,164,011B，plugin 26.1021.31345）：grep x-codex=1、guardian/safety/downgrade/service_tier/gpt-6/gpt-5/model_slug 均 0；codex-browser-app Chromium profile Cache 仅 1.9MB | **降智（low）** | 全部亲自取证核实，精确成立（ACL 拒绝重跑复现；详文输入截断） |
| rea-14（桌面逆向） | __oailb 与 BPS/basispoints 维持 0 命中（二进制扫描面） | scan2_codex.txt 行 59-60,64-66：basispoints=0、bps.openai=0、__oailb=0、__cflb=0、__cf_bm=0；三卫星同 0 | **降智（low）** | 二进制 0 命中事实全部复核成立；但标题解释性结论「客户端不感知该通道」**被源码证据部分修正**——src-10 证实客户端进程级 jar 白名单存 __oailb 并原样回发（仅作不透明路由 cookie，不解析语义）。复核详文输入截断（详见 ④） |
| rea-15（桌面逆向） | 硬编码警告文案与目录代际脱节：0.164 客户端写死 gpt-5.2/gpt-5.3-codex，当前服务端目录已是 6.x——reroute 提示会失真（可披露点） | mod.rs:4227 文案硬编码（to_model 字段用真实 server_model，但警告文本独立写死）；models_cache.json 目录无 gpt-5.3-codex/gpt-5.2；severity=文案级 | **降智（low）** | 全部亲自取证核实，精确成立（grep 全源码树非测试文件唯一命中 core/src/session/mod.rs:4227，Read 4175-4248 确认） |
| src-1（源码挖掘） | served model 回显不走 response.created/completed 体，只走 openai-model 头：HTTP 响应头、WS 握手头、事件内 headers JSON（openai-model/x-openai-model） | codex-api/src/sse/responses.rs:405-411（response.created 仅取 response.id）；:109-116（ResponseCompleted 仅 id/usage/end_turn，无 model 字段）；:34 `const OPENAI_MODEL_HEADER: &str = "openai-model"`；:50-54,76-78 头→ResponseEvent::ServerModel；:269-279 事件 headers.openai-model/x-openai-model | **降智（high）** | 成立，三通道 + 否定性结论均证实（全部引用行亲读命中；详文输入截断） |
| src-2（源码挖掘） | 模型不匹配检测：ServerModel 与请求 slug 大小写不敏感比较，不匹配只发一次 ModelReroute 事件 + 固定网络风险警告，无自动重试/升级 | core/src/session/mod.rs:4219（小写比较）；:4226-4227 固定文案 "Your account was flagged for potentially high-risk cyber activity and this request was routed to gpt-5.2 as a fallback"；turn.rs:3122-3133 每 turn 仅一次；core/tests/suite/safety_check_downgrade.rs:114（error_response_emits_typed_error_without_retry） | **降智（high）** | 成立（亲读 + 补证新证据点） |
| src-3（源码挖掘） | safety_buffering 响应头链路：两头，enabled=false 也不阻断 faster_model 下发 | codex-api/src/safety_buffering.rs:4-6 两常量；:8-20 treatment_from_headers；:49-66 测试 buffering_enabled_header_does_not_gate_the_faster_model_fallback（enabled:false + faster-model 头仍返回 Some(treatment)） | **降智（medium）** | 成立（逐行 Read 全文 68 行核实，全部精确成立） |
| src-4（源码挖掘） | safety_buffering 事件的 retry_model 是唯一降级重试通道：TUI 弹确认框，用户同意才中断当前 turn、fork 新 thread，用 faster_model + effort=Low 重发 | sse/responses.rs:259-264（缺 retry_model 时回退响应头值 faster_model）；tui/src/app/safety_buffering.rs:101-102（`*turn_model = model.clone(); *effort = Some(ReasoningEffortConfig::Low)`）；tui/src/chatwidget/safety_buffering.rs:199-211（'Retry with a faster model' 需用户选择+二次确认） | **降智（medium）** | 成立（全链路逐行核实） |
| src-5（源码挖掘） | service_tier 是客户端可设参数：Fast→"priority"、Flex→"flex"，config 解析，FastMode/UltrafastMode feature 默认开启，"default" 为显式 standard 哨兵 | protocol/src/config_types.rs:540-544（映射）；:537 SERVICE_TIER_DEFAULT_REQUEST_VALUE="default"；core/src/config/mod.rs:4144-4160 解析+feature 门控；features/src/lib.rs:2035-2046（FastMode/UltrafastMode stage:Stable default_enabled:true） | **降智（high）** | 成立（一处行号需修正，其余精确） |
| src-6（源码挖掘） | service_tier 传递路径：config → get_service_tier 过滤 → model_info.service_tier_for_request 再过滤 → 请求体 service_tier 字段（声明在 routing 字段区，注释明言网关会增量扫描请求体） | core/src/session/mod.rs:1203-1219（flex 无条件直通，其余需 feature+supports_service_tier）；protocol/src/openai_models.rs:1017-1022（过滤 "default" 与 catalog 不支持的 tier）；codex-api/src/common.rs:278-304（:280-281 注释 "gateways may inspect request bodies incrementally"） | **降智（medium）** | 成立（三处引用区间精确命中，传递链全链闭合） |
| src-7（源码挖掘） | 官方何时设置/剥离 tier：guardian reviewer 请求强制 service_tier=None；compaction 与 startup 预热沿用当前 config/settings tier；子代理继承根 tier | core/src/client.rs:1881-1883 与 :2051-2053（`if is_guardian_reviewer(...) { request.service_tier = None }`）、:2483-2487 判据（x-codex-guardian==reviewer）；compact.rs:842（turn_context.config.service_tier.clone()）；startup_prewarm.rs:436-447（预热传 settings.service_tier）；:344-355（子代理用 agent_control.service_tier()） | **降智（medium）** | 成立（五组证据亲读，剥离发生在 build_routing_hint_header 之前，全部精确） |
| src-8（源码挖掘） | x-codex-routing-hint 头把 model+tier 明文发给后端路由：`model={model};tier={tier}`，仅 codex backend 且非 guardian 时注入 HTTP 与 WS 握手 | core/src/client.rs:163 常量；:1292-1295 format（无 tier 时只发 model=）；:1438-1439、:1884-1892 注入；:1288 门控（uses_codex_backend 且 !is_guardian_reviewer） | **降智（high）** | 成立（行号全部精确核实） |
| src-9（源码挖掘） | model 或 service_tier 变更会使 WS 增量续用失效：responses_request_properties_mismatch 判 model_changed/service_tier_changed，退回全量 input（不换连接，previous_response_id 置空） | core/src/client.rs:432-449（previous_model != current_model → "model_changed"；service_tier 变 → "service_tier_changed"）；:2129-2146（失败时 continuation=None、mode="full"，同连接重发全量） | **降智（medium）** | 成立（四段断言精确核实） |
| src-10（源码挖掘） | __oailb 是 OpenAI 基础设施路由 cookie（非认证），存于进程级全局 jar 白名单，ChatGPT 域的 HTTP 与 WSS 共用同一 store 并原样回发 | http-client/src/chatgpt_cloudflare_cookies.rs:197-213 白名单含 "__oailb"；:198 注释原文 "__oailb is an OpenAI infrastructure routing cookie, not an authentication cookie"；:17-22 全进程共享 jar 警告（process-global，仅存 Cloudflare 基础设施 cookie）；:109-117 chatgpt_cookie_header 对 https/wss 同源取用 | 票（medium） | 成立（四组引用亲读命中，两组行号精确；并在二进制定位到白名单编译产物；详文输入截断） |
| src-11（源码挖掘） | startup 预热变体实为 Base/History（非 Base/Full）：Base 空输入建连，History 用与真实采样相同的 history 投影；真实 turn 仍校验 prompt 前缀与 settings 匹配才复用 | core/src/session/startup_prewarm.rs:35-48 PrewarmInput::{Base,History}；codex_thread.rs:277-278（"Sends `generate: false`; the next turn reuses the prepared response only if its prompt still extends this history and its request settings match"）；tasks/regular.rs:78 consume_startup_prewarm_for_regular_turn | 票（medium） | 成立（全部亲自读取核实，:398-399 `PrewarmInput::Base => Vec::new()` 等精确命中） |
| src-12（源码挖掘） | generate:false 预热 = 零推理连接准备：标 Warmup 类型、禁 trace，response 标记 untraced_warmup，下一真 turn 可沿用其 response_id 作 previous_response_id 并省略已发送 input | core/src/client.rs:2186（`generate: if warmup { Some(false) } else { None }`，发现引 :2187 差 1）；:2152-2155 注释（"Prewarm sends generate=false; it is connection setup, not a model inference attempt"）；:2259（last_response_from_untraced_warmup=warmup，发现引 :2260 差 1）；:2159-2164 复用注释 | 票（high） | 成立（两处行号差 1，实质全部核实） |
| src-13（源码挖掘） | x-codex-turn-state 是服务端下发的 sticky routing token：经 HTTP 响应头 / WS 握手头 / response.metadata 事件下发，OnceLock 只收首个值，客户端在后续 HTTP 头与 ws client_metadata 回显 | core/src/client.rs:164 常量；:2493 注释（"x-codex-turn-state: sticky routing token captured earlier in the turn"，发现引 :2494 差 1）；下发 sse/responses.rs:66-73、responses_websocket.rs:560-567、:849-853；回显 client.rs:2117-2119（client_metadata）、:2506-2511（HTTP 头） | 票（high） | 成立（两处引用行号有 1 行内偏差，内容精确） |
| src-14（源码挖掘） | auth owner 变化的票清理：owner generation 不符即重置 WS 会话（连接丢弃）并重建 turn_state OnceLock；turn_state 仅 owner 一致时返回，不符的流式请求直接 TurnAborted | core/src/client.rs:1725-1736（owner_changed → websocket_session.reset + `self.turn_state = Arc::new(OnceLock::new())`）；:1523-1529（turn_state_with_auth_owner 仅 owner 一致才返回）；:1823-1827（expected owner 不符 → Err(CodexErr::TurnAborted)） | 票（high） | 成立（三处引用全部精确核实） |
| src-15（源码挖掘） | 连接/路由身份键：ResponsesConnectionKey = base_url + x-openai-account-routing-override + auth_revision；discovery 返回 backend_origin 与 override（仅 NO_CONSTRAINT/us/us_cr），期间账号变更即硬错 | model-provider/src/workspace_routing.rs:12（`x-openai-account-routing-override`）；:59-65（仅 "NO_CONSTRAINT"\|"us"\|"us_cr"）；:88-103（ResponsesConnectionKey）；login/src/auth/manager/workspace_routing.rs:80-88（owner_generation 或 account_id 变 → Err "account changed during workspace routing discovery"） | 票（high） | 成立（四处引用行亲读命中，行号精确） |
| src-16（源码挖掘） | compact 会话语义：remote compaction 是与普通 turn 同路径的正常推理流（request_kind="compaction"、同 config service_tier、v2 走 HTTP executor）；用旧模型失败且模型不同时用 current model 重试 | core/src/responses_metadata.rs:167-187（request_kind 枚举 turn/prewarm/compaction/memory）；compact.rs:821-848（stream 用同 tier）；compact_model_fallback.rs:10-33（should_retry_with_current_model）、:46（reason=model_downshift）；compact_remote_v2.rs:304（ResponsesHttpExecutor） | 票（medium） | 成立（五处证据亲自读取核实，精确成立） |
| src-17（源码挖掘） | thread/session 与路由的身份载体：client_metadata 携带 session_id/thread_id/turn_id/request_kind 及 fork/parent/window 元数据（x-codex-turn-metadata blob + 平铺投影），HTTP 另发 session-id/thread-id/x-client-request-id/x-openai-subagent 头 | core/src/responses_metadata.rs:32-39 键名常量（SESSION_ID_KEY/THREAD_ID_KEY/TURN_ID_KEY/WINDOW_ID_KEY/CONTEXT_WINDOW_ID_KEY/REQUEST_KIND_KEY 等）；:547-609 CodexTurnMetadataPayload；codex-api/src/endpoint/responses.rs:87-93（x-client-request-id=thread_id + build_session_headers）；requests/headers.rs:5-14（session-id/thread-id 头）、:16-31（subagent 含 compact/review/collab_spawn） | 票（medium） | 成立（五处证据亲读核实，精确成立） |
| src-18（源码挖掘） | ModelDownshift：切到更小上下文模型且历史超新窗口限时，客户端先用前一模型跑预压缩，再由新模型接管，失败则 fallback 到 current model | core/src/session/turn.rs:1422-1504（maybe_run_previous_model_inline_compact）；:1482（CompactionReason::ModelDownshift）；:1488-1492 注释（"The previous model summarizes the old window; the current model owns its replacement"） | 票（low） | 成立（三处引用全部精确命中） |
| src-19（源码挖掘） | 非 WS 模式的 startup prewarm 只做 prewarm_auth（凭据解析/Agent Identity bootstrap），不产生任何网络预热请求；CodeModePrewarm 分支仅本地 code-mode host | core/src/session/startup_prewarm.rs:244-254（`if !responses_websocket_enabled()` → spawn model_client.prewarm_auth() 后 return，注释说明 Agent Identity bootstrap）；:233-242（CodeModePrewarm 分支） | 票（low） | 成立（三段断言亲读核实） |
| src-20（源码挖掘） | 实测（允许项）models_cache.json：客户端可设 tier 集合由服务端模型目录下发，如 service_tiers=[{id:"priority",name:"Fast",description:"2x speed, increased usage"}]，additional_speed_tiers=["fast"] | python 只读解析 `C:\Users\zfx\.codex\models_cache.json`：client_version=0.164.0，fetched_at=2026-10-24T17:42:20Z（复核时 19:42Z 有过一次刷新，所引字段不变）；models[0].slug=gpt-6.1-sol；目录支撑 openai_models.rs:1008-1015 supports_service_tier | **降智（medium）** | 成立（复核者亲自重跑 python 解析） |

**与本网关直接相关的机制闭环**（由上表交叉得出，均为输入数据内的归纳）：
- 降智信号面（观测依据）：served-model 只走 `openai-model`/`x-openai-model` 头（src-1）→ 网关增强 Item 1 据此补齐 X-Openai-Model 变体；safety_buffering 走两头 + metadata 事件（rea-4/src-3）；service_tier 降档可从 requested vs active 记录对照（rea-5/src-5）。
- 票机制面（转发依据）：turn-state 是服务端 sticky routing token，客户端只收首值并回显（src-13）；owner 变化即弃票重置（src-14）；model/tier 变更使增量续用失效（src-9）；预热 = generate=false 零推理连接准备（src-12/rea-12）。

---

## ④ 已证伪发现

输入数据 `findingsDisproved` 为**空数组**——本次流程没有按「证伪」归档的发现，无表格行可填（不虚构）。

最接近证伪的一例是 **rea-14 的标题解释性结论被复核部分修正**（发现本体保留在 ③ 确认表中）：

- **维持成立的部分**：四个二进制（codex.exe + 三卫星）中 `__oailb` / `basispoints` / `bps.openai` / `__cflb` / `__cf_bm` 均 0 命中（scan2_codex.txt:59-60,64-66；复核者另用自写 python mmap 大小写不敏感正则独立复扫四个二进制——该加强验证的详细输出在输入中截断）。
- **被修正的部分**：src-10（http-client/src/chatgpt_cloudflare_cookies.rs:197-213）证实客户端把 `__oailb` 列入进程级 cookie jar 白名单，并在 ChatGPT 域的 HTTP 与 WSS 上原样回发。即客户端在**传输层感知并以不透明方式处理**该 cookie，只是不解析其路由语义。正确表述应为：「客户端不解析 __oailb 语义，仅作为不透明基础设施路由 cookie 存储/回发」，而非「客户端不感知该通道」。
- 处置：rea-14 结论保留但表述按上述修正；未单独归入「已证伪」（其可验证主张——0 命中——仍成立）。

---

## ⑤ 增强方案实施结果

### 获批清单（5 项，全部 touchesUpstreamShape=false）

| # | 标题 | 内容要点 | 触及文件 | 风险/规模 |
|---|---|---|---|---|
| 1 | 补齐 served-model 检测盲区：事件 headers JSON 的 x-openai-model 变体 | observeHeadersLocked 原只读 headerOpenAIServedModel="OpenAI-Model"；response.metadata 事件的 headers JSON 经 http.Header.Set 规范化后 x-openai-model 变成 X-Openai-Model，Get("OpenAI-Model") 取不到而漏检。修复：同时读规范化键 X-Openai-Model（两处同在时 OpenAI-Model 优先），补 observeEvent 用例 | backend/internal/service/openai_codex_degradation.go + test | low / small |
| 2 | 降智冷却重复触发指数退避（仅 observe_only=false 生效，默认关） | registry 记录连续触发次数，冷却时长 = cooldown_seconds × multiplier^n 封顶 max_cooldown_seconds（新配置 multiplier=2 / max=1800）；窗口内出现无降智样本清零；EntryView 增 next_cooldown_at/consecutive_triggers | openai_codex_degradation.go、config.go、deploy/config.example.yaml | low / small |
| 3 | 下游降智透明响应头（新配置，默认关） | downstream_header_enabled（默认 false）开启后写 `x-sub4api-codex-served-model=<实际服务模型>` 与 `x-sub4api-codex-degraded=model_mismatch\|safety_buffering`；复用 x-request-id 先例的下游头提交模式 | config.go、openai_codex_degradation.go、openai_gateway_response_handling.go、config.example.yaml | low / small |
| 4 | 限流/余额头族被动观测 | observeHeadersLocked 只读捕获 x-codex-rate-limit-reached-type / -active-limit / -credits-balance（头名经 scratch/rea-desktop/scan2_codex.txt:2,4,17 核实），写入 entry last_* 字段并透出 View；promo-message 不采集（避免上游营销文案泄入管理端 API）；取值原样字符串计数，不臆造枚举语义，不参与降智判定 | openai_codex_degradation.go + test | low / small |
| 5 | 管理端「真实流量降智指标」面板 | 调 GET /admin/accounts/codex-degradation/metrics（路由已存在 admin.go:377-378），表格展示每 (账号,模型) 的 total/degraded/model_mismatch/safety_buffering、窗口样本与降智比、last_* 字段，附「清空指标」按钮（POST .../reset 已有）；API client 加进 codexTickets.ts，面板挂 AccountsView.vue，补 en/zh 文案 | frontend/src/api/admin/codexTickets.ts、AccountsView.vue、en/zh admin/accounts.ts | low / medium |

### 实施结果

**done（5/5 项实现）**，逐条实施要点（转录自实施 ask 的 summary）：

1. **Item 1**：openai_codex_degradation.go 新增 `headerOpenAIServedModelFallback="X-Openai-Model"` 常量（:35-38），observeHeadersLocked 在 Get("OpenAI-Model") 为空时回退读规范化键，两处同在时 OpenAI-Model 优先。新用例 TestCodexDegradationObserverParsesLowercaseServedModelVariant（事件 headers JSON 仅含 x-openai-model 时判定 mismatch）与 TestCodexDegradationServedModelHeaderPriority 均通过。
2. **Item 2**：config.go CodexDegradationConfig 新增 CooldownBackoffMultiplier（Normalized 默认 2，<1 钳到 1=关闭累积）与 MaxCooldownSeconds（默认 1800）；registry 新增 consecutiveTriggers map；recordCodexDegradation 遇无降智样本清零计数；allowCodexDegradationCooldown 改为 (seconds, consecutive, ok)，在 registry.mu 内完成防重入+计数推进，第 n 次连续触发 = cooldown_seconds × multiplier^(n-1) 封顶 max（codexDegradationBackoffSeconds 含溢出保护）；冷却 warn 日志新增 consecutive_triggers；CodexDegradationEntryView 新增 consecutive_triggers/next_cooldown_at 并在 Metrics/Reset 填充与清空。**默认关依据**：viper.SetDefault("gateway.openai_codex_degradation.observe_only", true)（config.go:2557），backoff 仅在显式 observe_only=false 的冷却路径可达，默认部署零行为变化。
3. **Item 3**：config 新增 downstream_header_enabled（默认 false，零值即关）；新增 observer.downstreamSnapshot() 与 service.applyCodexDegradationDownstreamHeaders（自带 c==nil / Written() 防护，无判定不写），写 x-sub4api-codex-served-model 与 x-sub4api-codex-degraded（多原因 "|" 连接）。接线三处：流式暂存提交点 applyAttemptResponseHeaders（守卫分解为 !stageFirstOutput 先行 return，暂存集为空时仍提交）、流式非暂存 x-request-id 处、handleNonStreamingResponse 响应体写出前。已知限制：streaming 提交点只能携带彼时已落定的响应头级信号，其后到达的事件级信号照常入库但无法补写已发出的响应头。config.example.yaml 同步注释（含灰度说明）。
4. **Item 4**：observeHeadersLocked 只读捕获三头（头名经 scan2_codex.txt:2,4,17 核实），observer 与 entry 新增 last_rate_limit_reached_type/last_active_limit/last_credits_balance（omitempty）并透出 View；TestCodexDegradationObserverCapturesQuotaHeaders 断言 snapshot 未降智（不参与判定）。
5. **Item 5**：codexTickets.ts 新增 CodexDegradationEntry/CodexDegradationMetrics 类型与 codexDegradationMetrics()/resetCodexDegradationMetrics()（GET/POST /admin/accounts/codex-degradation/metrics[/reset]，路由已存在 admin.go:377-378）；AccountsView.vue 以内联 Teleport 弹窗实现只读面板（item 5 文件清单不含新组件文件），入口挂「更多工具」菜单，展示 (账号,模型) 全部指标与连续触发/冷却截止/last_*，头部含 observe_only 状态徽标、刷新与清空按钮，纳入 isAnyModalOpen 防自动刷新冲突；en/zh accounts locale 各补 codexDegradation 段 22 键。

**skipped（1 项部分未覆盖）**：Item 3 范围内未覆盖 **passthrough 与 WS 两条下游路径**——openai_gateway_passthrough.go 与 openai_ws_forwarder_v2.go 不在 item 3 获批文件清单内（改动范围红线）。降智透明头目前只写入 openai_gateway_response_handling.go 的三条路径（流式暂存提交点 / 流式非暂存 x-request-id 提交点 / 非流式 JSON 响应体写出前）。helper（applyCodexDegradationDownstreamHeaders）与配置已就绪，补齐这两条路径只需在各自 observeCodexDegradationHeaders 之后加一次调用，**需扩文件清单后跟进**。

### 测试（增强实施 ask 亲自执行）

- `go build` 与 `go vet`（internal/service、internal/config）→ 0 输出通过
- `go test ./internal/service/ -run TestCodexDegradation -count=1` → ok（**20 用例全 PASS，含新增 8 个**）
- `go fmt` 后无差异
- `npx vue-tsc --noEmit` → 0 错误
- `npx vitest run src/i18n/__tests__/localeKeyCompleteness.spec.ts` → 3/3 通过
- `npx eslint` 四个改动前端文件 → 0 告警
- python+pyyaml 解析 deploy/config.example.yaml → 新键 cooldown_backoff_multiplier=2 / max_cooldown_seconds=1800 / downstream_header_enabled=false 均正确落位 gateway.openai_codex_degradation 下
- pluginTestsPassed = true；backendTestsPassed = true

**异常说明（如实记录）**：`go test ./internal/config/` 有两个失败（TestLoadForBootstrapAllowsMissingJWTSecret、TestLoadDefaultDatabaseSSLMode），经 git stash 回到干净 HEAD 复测**同样失败**——属本次会话开始前即存在、与本次改动无关（本次 config.go 仅动 CodexDegradationConfig）；stash 往返已完整恢复，仓库遗留 stash@{0}（codex-fidelity-migration-backup）为先前工作所留、未触碰。全量测试未在增强 ask 内跑（按约定由脚本统一执行）；backend `./internal/service/` 全量绿（153s）来自主会话基线。

---

## ⑥ 明确未验证事项

**只能 Phase 3（单账号真实流量实测）验证的上游行为——本地仅有客户端/源码侧机制证据：**

1. **prewarm 是否真实铸票**：本地已证客户端会发 generate=false 预热帧（src-12/rea-12），服务端会经 response.metadata 下发 turn-state token（src-13 源码面）；但**真实上游是否为预热帧铸造票**从未实测。
2. **票是否被上游采纳**：回显 x-codex-turn-state 是否真的换来 sticky routing（同后端/同模型路由亲和）从未实测；src-13 只证明客户端会回显，不证明上游采纳。
3. **上游降智判定条件**：何种账号/流量/内容条件触发 served-model 替换（ModelReroute）、safety_buffering、service_tier 降档——全部证据为客户端侧检测与呈现机制（rea-3/rea-4/rea-5/src-2），**上游触发条件未验证**。

**流程内其他未验证事项：**

4. **W4 的数据竞争消除**：`-race` 在本机不可运行（CGO 需 gcc，PATH 无任何 C 编译器，已尝试 which gcc/cc/clang/mingw 均无），race 探测器层面未验证，仅以结构证据（streamEvents 函数体无 capture 字段直写）+ 行为测试代替。
5. **W1 的断流路径**：32KiB 默认读限与 1009 断流的因果依据 coder/websocket v1.8.14 read.go:107 库源码**推定**，未对旧代码复跑复现断流。
6. **W3/W5 的官方行为依据**：修复 ask 时来自上级 ask 提供材料（client.rs:1425 turn_state=None；client.rs:2169-2185 continuation=None），当时未亲读官方源码；后续 S2/src 系列 ask 已亲读同版源码快照（v0.164.0-alpha.11.1）覆盖 turn_state 下发/回显与预热行为（src-13/src-12/S2），与该依据一致——此为**间接**补证，非对 W3/W5 本身的复验。
7. **性能量化未做**：连接池 TTL/retire（W2/W6）、读上限（W1）、空闲超时（W7）、管道修复（T3）等改动对延迟/吞吐/上游请求量的全部影响未量化。
8. **全量测试覆盖面**：增强实施 ask 内未跑全量套件（按约定由脚本统一执行）；backend `./internal/service/` 全量绿（153s）为主会话基线，**其余 backend 包的当前全量状态未在本次流程复验**（config 包另有 2 个预存在失败，见 ⑤ 异常说明）。
9. **B2 完整影响面**：最终评审 B2 详文在输入数据中截断（止于「业务回收仅 harvest 写库（backend/inter…」），完整影响范围未在报告层核实（见 ② 测试与评审结果）。
10. **B1 未修复前的指标可信度**：openai_ws 默认开启，B1 缺陷未修复前 `/admin/accounts/codex-degradation/metrics` 在默认部署下系统性失真（假阴性样本 + 回退信号丢失）——**任何依赖该面板的实测结论都应先修 B1 再采集**。
11. **rea-13 的壳层盲区**：桌面壳本体位于 WindowsApps（ACL 拒绝），其前端是否含降智逻辑**无法读取、未验证**；可读的 server.mjs 仅 1 处 x-codex-turn-metadata。

---

## ⑦ 下一步建议（按优先级）

**P0 — 交付物正确性（先于一切实测）**

1. **修复 B1（observer 生命周期缺陷）**：给 recordCodexDegradation 加 headersObserved/eventObserved 门控（未触上游的失败尝试不计样本）；WS→HTTP 回退路径不复用已 finalize 的 observer（或允许二次 finalize 合并）。依据：openai_ws_forwarder_v2.go:45-46/:49-51、openai_codex_degradation.go:353-444/:210-228/:243-247、openai_gateway_forward.go:926-1035。不修则本次交付的降智指标与冷却在默认（WS）部署下系统性错误。
2. **处理 B2（fail_closed 票务锁死）**：先向最终评审 ask 索取 B2 完整记录（输入截断），并在代码复核 openai_codex_ticket.go:480-503（blocks 不检查 turn_state_mode）、:706-710（harvest 跳过刷新）与 config.go:2531-2532（新默认 relay + probe_enabled=false），再定修复方案。升级后 fail_closed 部署若被锁死属可用性风险。

**P1 — 验证闭环**

3. **Phase 3 单账号实测**（依赖 P0-1 修复后的指标面板 + Item 4 被动头族观测作为工具）：① prewarm 是否铸票（观察预热帧后是否收到 response.metadata turn-state）；② 票是否被采纳（回显后是否同后端/同模型路由）；③ 上游降智判定条件（触发 served-model 替换 / safety_buffering / tier 降档的账号与流量条件）。
4. **补齐 Item 3 未覆盖的两条下游路径**（passthrough、WS forwarder）：helper applyCodexDegradationDownstreamHeaders 与配置已就绪，扩获批文件清单后在各自 observeCodexDegradationHeaders 之后各加一次调用即可。

**P2 — 测试与工程卫生**

5. **补 WS 失败/回退生命周期测试用例**（B1 指出的测试空白：WS 失败注入假阴性、回退信号丢失两场景）。
6. **在具备 C 编译器的环境对 plugins/codex-bridge 补跑 `-race`**，闭环 W4 的竞争探测（当前仅结构证据）。

**P3 — 可选**

7. **性能量化**：池化（W2/W6）、读上限（W1）、空闲超时（W7）对延迟/吞吐/上游请求量的影响测量。
8. **可披露项**：rea-15——0.164 客户端硬编码 gpt-5.2/gpt-5.3-codex 警告文案与服务端 6.x 模型目录代际脱节，reroute 提示对用户失真（文案级，可对外披露）。

---

*报告结束。所有结论的证据边界与未验证项见 ⑥；本报告未添加输入数据之外的任何断言。*

---

## ⑧ 主会话事后核验补记（2026-10-25，报告成文后追加）

> 本节由主会话（流程编排者）在报告完成后独立核验追加，证据均为亲测，修正 §②/⑥/⑦ 中已被后续事件超越的表述。

**B1/B2 已修复**（§②"未解决的开放缺陷"表述过时）：独立终审员实际以 blocking=[B1,B2] 驳回 → 「开发-终审修复」轮实施修复 → 插件全量测试门通过 → 终审转通过。报告撰写人只收到终审 blocking 备注转录、不知修复轮存在，故误写为"未解决"。事后核验证据：

1. **B1 修复在位**：`backend/internal/service/openai_codex_degradation.go:88-89` 新增 `headersObserved/eventObserved` 字段、`:110/:166` 观测点置位、`:465` `upstreamObserved := observer.headersObserved || observer.eventObserved` 门控——未触上游的失败尝试不再计样本。主会话独立复跑 `go test -C backend ./internal/service/ -run "TestCodexDegradation|TestOpenAIWhitelistsCarryClientThreadID"`：**全部 PASS**，含三个 B1 场景用例 `TestCodexDegradationZeroSignalTurnNotRecorded`、`TestCodexDegradationHandshakeObservedTurnRecorded`、`TestCodexDegradationWSFallbackRecordsHTTPSignals`（degradation 测试文件共 20 个用例）。
2. **B2 修复在位**：`openai_codex_ticket.go:110-121` turn_state_mode=relay/harvest/off 规范化；新增 `backend/internal/service/codex_ticket_recycle.go`——「业务响应头回收票（方案 A）」：在网关自己的业务响应上直接接住上游自然铸的票（只收上游真实下发的 state、(账号,模型) 键控、不跨账号），配 `codex_ticket_recycle_test.go` 6 个测试函数，随 service 全量套件（门控两次）通过。**注意：B2 修复的完整影响面仍建议下轮复审**（终审原始详文截断，本补记只核实修复标记与测试通过，未逐行复审 recycle 全部路径）。
3. **测试终态复跑**：主会话以 go1.27.3 亲测 `plugins/codex-bridge ./...` 5 包全 `ok`。
4. 因此 §⑦ 的 P0-1/P0-2（修 B1/B2）已完成，下一步优先级直接从 P1（Phase 3 实测）起跳。
5. 附带发现（运维提示）：`.gitignore:170` 忽略整个 `/plugins/`、`.gitignore:3` 忽略 `/scratch/`——**codex-bridge 全部插件代码与全部研究报告当前不受 git 版本控制**，无历史保护，建议用户决定是否纳管。

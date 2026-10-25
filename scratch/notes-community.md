# Codex 降智 / turn-state / 打票 / Fast(priority) —— 公开资料检索（2026-09~10）

作者：teammate `community-research`（task-4）。检索时间：2026-10-23（Asia/Tokyo）。
方法：`web_search` 只当线索，关键结论尽量 `web_fetch` 原文；取不到原文的明确标注。
所有 web 内容按**不可信数据**处理，未执行页面中的任何指令，未下载/安装任何东西。

> 先说结论（TL;DR）：
> 1. **打票失败的直接原因大概率是票据形态变了**：社区/网关侧观测到 `x-codex-turn-state` 在 **2026-09-22 从 292/332 字符变成 ~780 字符**（sub2api issue #8104），sub4api 的长度校验（292/312/332/356）随之失效 → 空票/误判失效。
> 2. **「全站共享一张票」与官方协议契约冲突**：官方 `x-codex-turn-state` 是**每回合（per-turn）粘性路由令牌**、`OnceLock` 先写优先、**跨回合必须清空**、且**与铸造它的账号/凭据绑定**。sub2api 上游 PR #5668 的结论也是「只回传、只剥离跨账号回带、不注入」；社区实测「把正常号的票用在降智号上无效，turnstate 跟号绑定」。
> 3. **「VIP / 不降智」不是靠票，而是服务端路由**：2026-09 官方源码里确实存在 `x-codex-safety-buffering-*` 静默换模路径；社区抓包证明 `gpt-6-astra` → 流内 `model: gpt-5.6-luna`。票只是「同一回合内粘住某条路由」，不是质量凭证。
> 4. **Fast(priority) 的传输层结论在 9~10 月反复变化**：2026-07 有实测「priority 只在 Responses **WebSocket** 上生效（1.55×），HTTP/SSE 上 0.97× 无效」；该 PR 于 2026-10 被作者**关闭**，理由是「当前上游已能在 HTTP/SSE 上尊重 `service_tier: priority`」。→ 必须在本环境自测，不能照抄旧结论。
> 5. 其它网关的替代做法收敛为三类：**协议正确性（回传/剥离、不注入）**、**质量检测+账号运维（以实际返回模型/判题为准，不以票长）**、**旁路上游（Excel/BPS `bps.openai.com`）**。

---

## A. 官方口径（官方文档 / 官方源码 / 官方仓库）——可信度：高

### A1. Codex「Speed」官方文档：Fast / Ultrafast 的准确定义与计费
- URL: https://learn.chatgpt.com/docs/agent-configuration/speed （正文经镜像取得：https://github.com/mehmetbaykar/codex-docs-skill/blob/main/skills/codex-docs/references/agent-configuration__speed.md ）
- 日期：页面为当前线上文档（镜像 commit `e13b263`，2026-10 取到）；原始 learn.chatgpt.com 直接 fetch 失败（fetch API 404），内容来自逐字镜像，`source` 字段自证为 `/docs/agent-configuration/speed`。
- 结论（原文要点）：
  - **Fast mode**：加速 `GPT-6.1 Sol`、`GPT-6 Astra`、`GPT-6 Sol`、`GPT-6 Luna`；`GPT-5.6` 提速 1.5×。CLI 用 `/fast` 切换；可在 `config.toml` 持久化：`service_tier = "fast"` **且** `[features].fast_mode = true`。仅 **ChatGPT 登录**（desktop / CLI / IDE 扩展）可用。
  - **计费**：订阅内额度按 Standard 的 **2.5×** 计；购买的 credits / Enterprise PAYG 按 **2×**。
  - **Ultrafast**：仅 `GPT-6 Astra`、`GPT-6.1 Sol`；Astra Ultrafast 比 Standard **快至 8×**；仅 **Pro $500** 与符合条件的 Enterprise/Edu；订阅内额度 **8×**、credits/PAYG **6×**；Enterprise 默认关闭。
  - **API key 登录不吃 ChatGPT 倍率**，按 API token 价计费。
  - **GPT-5.5 于 2026-10-14 12:00 PDT（19:00 UTC）从 Codex + ChatGPT 登录退役**（API / API-key 会话不受影响）。
- 可信度：官方文档（镜像逐字，高）。
- 对 sub4api 的可落地性：**高**。sub4api 已有 `forced_service_tier ∈ {default, priority, fast, ultrafast}` 与 `x-codex-routing-hint`，与本文档语义可对齐：`fast` 是**配置值**，线上请求值是 `priority`（见 A9）；`ultrafast` 只在 Astra/6.1 Sol 有意义且**需要账号真的有 entitlement**（Pro $500/Enterprise），否则上游会拒/静默降级。另外要检查 CLI 侧 `[features].fast_mode` 是否被网关下发/保留，否则客户端自己会把 tier 丢掉（见 B7）。

### A2. OpenAI API「Fast mode」指南
- URL: https://developers.openai.com/api/docs/guides/fast-mode
- 日期：当前线上文档（原文 fetch 被 fetch 端点 404 挡住，仅取到搜索结果摘要，**未取得全文**）。
- 结论（搜索摘要口径）：`service_tier: "priority"` 与 `service_tier: "fast"` **都接受**，`priority` 与 `fast` 行为相同；响应回显会规范化成 `priority`；Fast mode 最快 2.5×；不支持 fine-tuned/embeddings。
- 可信度：官方（但此处只有摘要，未见全文 → 中高，需复核）。
- 可落地性：中。与 sub4api 现有 `priority`/`fast` 归一化一致；**回显 `priority` 不能当作"生效了"的证据**（另见 B6 的官方员工说法）。

### A3. OpenAI API「Ultrafast mode」指南
- URL: https://developers.openai.com/api/docs/guides/ultrafast-mode
- 日期：当前线上文档（同样只拿到搜索摘要 + 第三方引用，**未取得全文**）。
- 结论（摘要口径）：Ultrafast 是 API 最快服务层，面向 `gpt-6-astra` / `gpt-6.1-sol`；**在每次 `response.create` 事件里**设 `service_tier: "ultrafast"`；Astra 约 6×（API）/ Codex 内 8×。
- 可信度：官方摘要（中高）。
- 可落地性：中。注意「每次 `response.create`」是 **WS 帧**的措辞——如果 sub4api 走 HTTP `/v1/responses`，需要确认上游是否同样接受；这也解释了为什么 sub4api 的 `openAIModelSupportsForcedUltrafast` 只对 Astra / 6.1 Sol / `gpt-5.6-sol` 放行。

### A4. 官方源码：`x-codex-safety-buffering-*`（静默换模信号）确实存在
- URL: https://github.com/openai/codex/blob/main/codex-rs/codex-api/src/safety_buffering.rs
- 日期：main 分支当前版本（2026-10 取到）。
- 结论：官方客户端读取两个响应头 `x-codex-safety-buffering-enabled`、`x-codex-safety-buffering-faster-model`，解析成 `SafetyBufferingTreatment { faster_model }`；**enabled=false 也不阻断面 faster_model 回退**（有单测固定该行为）。客户端在 UI 上会提示「用更快模型重试」，默认操作是把会话切到更弱的模型。
- 可信度：官方源码（高）。
- 可落地性：**高**。这是比"票长"可靠得多的降智判据：网关层可直接读这两个头 + 流内 `model`/`response.created.response.model` 判断是否被换模；也可以选择**剥离**这两个头避免客户端被引导切模型（opencodex 的 `dropCodexSafetyBuffering` 就是这么做的，见 B7）。

### A5. 官方 issue #46632：aster → luna 换模的完整实证（2026-09）
- URL: https://github.com/openai/codex/issues/46632
- 日期：2026-09-19/20（抓包 09-17~09-19）。
- 结论：
  - Plus 账号请求 `gpt-6-astra`，**所有**追踪到的 run、所有 reasoning effort 都被服务端以 `gpt-5.6-luna` 服务；同机同网同分钟 `gpt-5.6-sol/luna/terra`、`gpt-5.5` 正常。
  - 原始 WS 帧：`response.created.response.model = "gpt-5.6-luna"`，`service_tier: "auto"`；同时带 `x-codex-safety-buffering-enabled: true`、`x-codex-safety-buffering-faster-model: gpt-5.6-luna`。
  - 同期 47 次 "Selected model is at capacity"，**全部在 HTTP 200 流内**，无 429，配额仅用 1–3%。
  - **关键环境变化**：`since the 2026-09-18 update, sampling uses the websocket transport`。
- 可信度：官方仓库中的高质量用户报告（带响应 ID/抓包）→ 高。
- 可落地性：**高**。① 降智检测应对比「请求模型 vs 流内 model」，并读 safety-buffering 头；② 2026-09-18 起官方客户端转 WS，意味着网关若仍走 HTTP/SSE，指纹与路由会与真实客户端不一致。

### A6. 官方 PR #9332：`x-codex-turn-state` 的协议契约（per-turn sticky routing）
- URL: https://github.com/openai/codex/pull/9332
- 日期：2026-01-16（codex_state_monitor 的核对记录；PR 页正文可读）。
- 结论（PR 正文 + 代码注释 + 第三方源码核对）：
  - 从 **SSE / WS 握手**捕获该头，`OnceLock`（first-write-wins）按 `ModelClientSession` 存储；
  - **同一回合内**回带，维持 retry/工具续跑的「routing continuity」；
  - **跨回合清空**，代码注释明确 "must not be sent between different turns"；
  - 客户端**完全不校验**该值的长度/内容。
- 可信度：官方 PR + 官方源码注释（高）。第三方核对：codex_state_monitor README（2026-09-20，见 C4）。
- 可落地性：**极高**。这直接判死「抓一张 292 票全站共享」的做法：即使票"能用"，也违反回合/账号作用域，属于被上报过的限流绕过行为，有封号风险。正确做法见 B1/B2。

### A7. 官方 PR #27931（+ #27996）：compact 也要 round-trip turn-state
- URL: https://github.com/openai/codex/pull/27931
- 日期：2026-06-12。
- 结论：远端正压缩（remote compaction）也要携带/回写 `x-codex-turn-state`；inline / pre-turn compaction 用 session 当前值；**compact 响应可以替换**后续 sampling 用的值；standalone compact 用隔离状态不许泄漏进用户回合。Codex 自动 review 指出一个真实坑：**已打开的 WS 连接不会被 compact 的新 state 重建**，会继续走旧路由（作者随后关闭该 stack，改由 #27996 合并）。
- 可信度：官方 PR（高）。
- 可落地性：**高**。sub4api 若做 turn-state 回传，必须覆盖 `/responses/compact` 与 WS 长连接；否则压缩后会"路由漂移"。

### A8. 官方 issue #14204：`service_tier="fast"` 在 ChatGPT 登录 + WS 下的混乱
- URL: https://github.com/openai/codex/issues/14204
- 日期：CLI 0.113.0 时期（2026 上半年）。
- 结论：CLI 首帧 `response.create` **不带** `service_tier`；强行塞 `"fast"` 得到 `400 invalid_request_error: Unsupported service_tier: fast`；直连 WS 探测 `response.completed.response.service_tier` 恒为 `default`（Plus/Team 都如此）。
- 另：CLIProxyAPI #4586 引用了 **OpenAI 员工在该 issue 的说法**："Fast is handled by Codex routing logic server-side… service_tier is not an end-to-end field you can reliably verify."（我 fetch 的 #14204 页面正文里**没有**这句，属二手引用 → 标注为**未独立核实**）。
- 可信度：官方 issue 正文（高）；员工引语（中，二手）。
- 可落地性：**高**。① 别把回显 `default` 当作失败证据（sub4api 代码注释已有同样结论）；② `fast` 在 ChatGPT 后端是**客户端配置值**，线上值用 `priority`（A9）。

### A9. 官方 commit 178c3d3（#21991）：`priority` 持久化成 `fast`
- URL: https://github.com/openai/codex/commit/178c3d30053ba63d118ee93a0dbe2716a12e5769
- 日期：2026（提交信息含 Codex Task 链接）。
- 结论：`ConfigEdit::SetServiceTier` 映射 `priority`/`fast` → 写盘 `"fast"`，`flex` → `"flex"`，未知值保留；`config.toml` 里写 `service_tier = "priority"` 载入时仍解析为 priority 请求值。
- 可信度：官方源码（高）。
- 可落地性：**高**。确认 sub4api 的 `fast ↔ priority` 双向映射与官方一致；admin 端展示"Fast"、发上游 `priority` 是对的。

---

## B. 其它网关/客户端的工程实现（可读代码，可信度中高）

### B1. keakon/chord commit 5c2c5e4：turn-state 的「正确姿势」参考实现
- URL: https://github.com/keakon/chord/commit/5c2c5e44a41bbcdce69dd325fb229352ea88ab32
- 日期：2026（提交可读；上下文提到 Responses/Codex 传输）。
- 结论（逐个抓包点都有代码）：
  - 捕获点三处：`/responses` SSE 的 `response.metadata` 事件里的 `headers["x-codex-turn-state"]`、HTTP 响应头、WS 握手响应头；
  - 存储：`ResponsesTurnState` 用 **provider+凭据（OAuth account id 或 api key）** 做 identity 分桶 → **跨账号/跨 provider 不互相回带**；
  - 生命周期：`newTurn` 创建、turn 结束丢弃；main / compaction / replay 共用同一个 turn 的 state；
  - 重试到**别的账号**时丢弃，避免钉在别人的路由上。
- 可信度：第三方工程实现（中高，代码自证）。
- 可落地性：**极高**——这几乎是 sub4api 应该抄的骨架（且与 A6 官方契约一致）。

### B2. Wei-Shaw/sub2api PR #5668（已合并，2026-08-14）：上游官方是怎么做的
- URL: https://github.com/Wei-Shaw/sub2api/pull/5668
- 日期：2026-08-14（v0.1.177）。
- 结论：
  1. `x-codex-turn-state` **只回传、不注入**：在各响应提交点显式回传（不放宽全局白名单，避免污染 Anthropic/Gemini 响应面）；上游没给就清掉上一次 failover 的残留；按 `(API Key + 客户端会话) → 铸造账号` 记账（TTL+清扫），**出站前剥离已知由其他账号铸造的回带值**。
  2. 会话级 beta 头对齐 `remote_compaction_v2`；legacy unary `/responses/compact` 已 404 下线，压缩探测改走原生 v2（带 `compaction_trigger`，要求真的产出 compaction item）。
  3. **指纹收敛（codex_fingerprint_mode）默认改回 `off`**：v0.1.175 把"缺省"当 `session`，升级后把所有存量 OAuth 账号的五类标识（installation/session/thread/turn/window）静默改写，导致 #5555/#5556/#5582 的额度缩水；回退 v0.1.173 即恢复（A/B 实测）。
- 可信度：上游官方仓库已合并 PR（高）。
- 可落地性：**极高**。这是"合规回传"的完整配方，也是**指纹收敛风险**的实证：sub4api 的 `codex_fingerprint_mode` 同样应默认 off / 显式 opt-in。

### B3. Wei-Shaw/sub2api v0.2.6（2026-09-17）：打票（票据采集+注入）曾进过上游
- URL: https://newreleases.io/project/github/Wei-Shaw/sub2api/release/v0.2.6
- 日期：2026-09-17。
- 结论：上游 0.2.6 曾加「Codex `x-codex-turn-state` 票据采集与注入（默认关闭、管理端开关实时生效）；ChatGPT OAuth 账号后台采集 292 位票据并在生产请求复用，默认有效期 1 小时」。
- 后续：0.2.8~0.2.17 的 release notes 里**再没出现票据能力**；MACOS DO 的说法是「因某种原因被迫回退删除」（C2，社区口径，未见官方说明）。
- 可信度：release notes（高）；"被迫回退"（低-中，社区传闻）。
- 可落地性：中。说明上游已经踩过一遍并撤了；sub4api 作为 fork 保留该能力要自担风险。

### B4. Wei-Shaw/sub2api issue #8104（2026-10-14）：**780 票据** 与 502 的实证
- URL: https://github.com/Wei-Shaw/sub2api/issues/8104
- 日期：2026-10-14（影响面：生产一天约 2300 次 502）。
- 结论：
  - 「单条 `x-codex-turn-state` 目前约 **780 字符**（**2026-09-22 起官方长度从 292/332 增加到 780**）」；
  - 流式首输出暂存阶段把同一组暂存头 **重复 Add 1–34 次**（`applyAttemptResponseHeaders` 只在 `c.Writer.Written()` 后跳过，但暂存阶段没真正写出），响应头最大约 27KB → nginx `upstream sent too big header` → 下游 502；客户端若合并多个值回带也会超 header 限制。
- 可信度：上游官方仓库 issue（带复现、curl、分布统计）→ 高。
- 可落地性：**极高**，两条都直接命中本任务：
  1. **打票失败根因候选**：sub4api 的票长校验（292/312/332/356、10/12 块）在 2026-09-22 之后会把 780 票判成"无效票"→ 空票、一直补票、票失效流水异常。
  2. 若 sub4api 也是 `Header().Add` 重复写，会复制出同样的 502；且重复票头对 turn-state 毫无意义（官方 OnceLock 先写优先）。

### B5. CLIProxyAPI #4586 / PR #4587（2026-07-25；2026-10 关闭）：priority 只在 WS 上生效？
- URL: https://github.com/router-for-me/CLIProxyAPI/issues/4586 、 https://github.com/router-for-me/CLIProxyAPI/pull/4587
- 结论：
  - issue：Codex OAuth 带 `service_tier: "priority"` 走 HTTP `POST /responses` 时，后端**接受字段但按 standard 服务**；只有 Responses **WebSocket** 才应用 priority 调度。同一 Team 凭据、固定负载实测：官方 CLI（WS v2）**1.55×**（85.0 vs 54.9 TPS）；HTTP 直打 **0.97×**（无效）。配额消耗也呈 2.3–2.5×（与官方 Fast 计费一致）。
  - PR #4587：把「HTTP/SSE 下游 + 解析出 priority + auth 允许 websockets」的**流式**请求桥到 WS executor，实测 1.55×。
  - **2026-10 该 PR 被作者关闭**：「current upstream Codex backend now respects `service_tier: priority` over HTTP/SSE, so this WebSocket bridge is no longer necessary」。评论里另有一组 6 次交替基准：priority 中位 83.7 TPS vs default 50.1（≈1.67×），但**回显仍是 default**。
- 可信度：第三方网关 issue/PR + 实测数据（中高）。结论随时间反转过 → 必须自测。
- 可落地性：**高**。sub4api 同时有 HTTP 与 upstream-WS 两条转发路径（ws_v2 / 客户端 WS ingress）。建议：**先做 A/B 实测**（同账号同模型，`service_tier=priority` 分别走 HTTP/SSE 与 upstream WS，比较输出 TPS 与 `x-codex-primary-used-percent` 消耗速率），再决定是否强制桥到 WS。**不要**根据回显 `service_tier` 判断生效。

### B6. opencodex（lidge-jun）网关：safety-buffering 剥离 + Fast tier 对齐
- URL: https://github.com/lidge-jun/opencodex/blob/main/docs-site/src/content/docs/zh-cn/reference/configuration/server.md
- 日期：2026-06-17 起持续更新（文档站）。
- 结论（可作为 sub4api 的功能清单）：
  - `dropCodexSafetyBuffering`（默认关）：从 Responses 透传里移除 `x-codex-safety-buffering-enabled` / `-faster-model` 响应头、`safety_buffering` 类型的 `response.metadata` SSE 事件，以及其他 SSE 事件中的 `safety_buffering` 字段；**不改上游安全策略/拒绝**；注意「原生 WebSocket `codex.response.metadata.headers` 和 `/responses/compact` 不在过滤范围内」。
  - `shadowCallIntercept`：把 Codex 的辅助/影子小模型调用重定向；用 `x-codex-turn-metadata`（`request_kind`/`thread_source`）与 `x-openai-subagent` 区分用户轮次/子代理，避免误伤。
  - `websockets`：声明并允许面向客户端的 Responses WS 路径；`false` 时客户端退回 HTTP/SSE。
- 另一条直接相关：commit `a828ccf`「fix: align Codex fast service tier」：**官方文档是 `service_tier = "fast"` 加 `[features].fast_mode = true`；本地只写了 `service_tier` 没开 `fast_mode`，当前 Codex 会因 feature gate 把 tier 丢掉**。
- 可信度：第三方网关文档/commit（中高）。
- 可落地性：**高**。① 降智"提示"可以在网关层剥离（但要覆盖 WS metadata 与 compact，否则漏）；② Fast 生效的客户端侧前置条件（`fast_mode=true`）要写进 sub4api 的「Codex 配置生成/文档」，否则用户以为开了 Fast 其实没开。

### B7. openclaw `docs/tools/thinking.md`：Fast 模式的映射契约
- URL: https://github.com/openclaw/openclaw/blob/main/docs/tools/thinking.md
- 日期：2026-10 取到（main）。
- 结论：`/fast` 级别 `auto|on|off|ultrafast|default`；对 `openai/*`：**普通 Fast → `service_tier=priority`，显式 Ultrafast → `service_tier=ultrafast`**，走 Responses；「provider decides whether the account/model can use that tier; sending it does not guarantee faster service」；Codex harness 下 Fast on 从 `priority` 起步，Standard/auto 清空 tier；Ultrafast 需要**已认证的 app-server 目录**确实广告支持，否则回落 Fast。
- 可信度：第三方 harness 文档（中高，且与 A1/A9 自洽）。
- 可落地性：高。sub4api 的 `ultrafast` 强推也应遵守「目录未广告就回落 priority」，避免上游 400/静默降级。

---

## C. 中文社区经验与传闻（可信度：低~中，必须标注为未验证）

### C1. sub4api 作者本人的「科研成果」帖（时区论 + 打票原理 + BPS 旁路）——重要
- URL: https://macos.do/t/topic/138
- 日期：2026-09-23 前后（帖子正文提到的测试期 9 月下旬）。
- 结论（作者自述，非官方）：
  - **时区**可能才是降智主因：Codex CLI / ChatGPT Desktop 会把时区写进系统提示词的 `<environment_context>` 的 `<timezone>` + `<current_date>`；不要用中国大陆时区。降智号「静置一段时间能用一下」也被解释成 CF 节点缓存过期。
  - **292/780 打票的原理**：不断换 IP，路由到一个该账号没访问过的 **Cloudflare 节点**，约 **240s** 内没有黑名单记录 → 不降智；超时后仍会被识别。→ 「打票只能换几分钟」。
  - 「**打票已经没有意义了，就算你打到了也只能用 200 秒**」；已降智账号**没有主动解除办法**，等官方解除（约一周，运气好几天）。
  - **BPS 旁路**（帖内 "IKUN" 提供，社区实测口径）：直接打 `https://bps.openai.com/basispoints/api/responses`，带 `authorization: Bearer <access_token>` + `chatgpt-account-id` + `x-openai-account-id` + **`x-basispoints-auth-mode: chatgpt`** → 社区称"满血"（不降智）；缺点：**没有 max effort 档**、**不支持原生 tools**（要写转换层，坑多）、上下文约 **600k 就压缩**。
- 可信度：作者/社区一手经验（中），无受控复现；「打票 200 秒」「240s 窗口」为经验值。
- 可落地性：**高**。① 时区替换成本极低、sub4api 已实现（默认 `Asia/Singapore`），可加强为"每账号与出口 IP 一致"；② BPS 已进 sub4api 1.1.5（见 C2），可作为打票的替代主路径；③ 「不要再把打票当核心」与 B4 的 780 变化互相印证。

### C2. sub4api 官方公告帖（打票时间线，作者 Jobs）——了解「我们已经试过什么」
- URL: https://macos.do/t/topic/81 （含 2026-09-25 更新）
- 日期：2026-09-19 ~ 2026-09-25（+ 后续回复）。
- 结论（作者自述）：
  - 观测规律（作者标注「未经 100% 准确验证」）：20X 账号比 Plus 更容易被标记；**被标记账号很难打出 292，长度一般是 312**；**没带 292 长度票的 `gpt-6-astra` / `gpt-5.6-sol` 请求几乎 100% 被路由到 `gpt-5.6-luna`**；`astra` 被换模时上游会回真实 luna（使用记录能看到模型不一致），`sol` 被换则不回显；2026-09-23 起「不再显示模型不一致，只能用 modeltrace/ModelTrace 测真实模型」。
  - 版本线：1.1.0 修好 292 打票；1.1.2（09-22）改用 L 站方案，**注入打票时的 Cookie（`__oailb`）**、票 TTL 200s、自动刷新 30s、首次 30–60s 随机、持续 30–40s；1.1.3 按模型校验票据 + 代理池 + 30 分钟主动刷新；**1.1.4（09-24）全局打票默认关闭**（TTL 太短没意义），保留时区替换（默认新加坡）"养号"。
  - 回复区：「一个 292 票有效期 1 小时」；「动态家宽 IP（每请求换 IP）出票率明显更高」。
- 可信度：第一方自述（对"事实时间线"高，对"机理"中）。
- 可落地性：**高**。给 lead 提供"已尝试/已放弃"清单，避免重复造轮子：sub4api 侧能做的（TTL、cookie、指纹、代理池、时区）基本都做过了。

### C3. sub4api README（当前 HEAD 2026-10-20）——现有能力边界
- URL: https://github.com/MACOS-DO/sub4api
- 日期：HEAD 2026-10-20（commit "fix(migrations): restore upstream 241…"）；releases 到 1.1.10/1.1.11。
- 结论：
  - 打票：按账号/模型参与、代理池轮换、失败随机重试、**主动刷新默认 30 分钟**、票据中心 + 流水（90 天）、**多模型降智检测**（走正式请求链路、正常计费）。
  - 时区替换：仅改 `<environment_context>` 里的 `timezone`/`current_date`（同步 `user_location.timezone`），默认 `Asia/Singapore`，列表不含中国大陆/港澳台。
  - 请求头语言统一 `Accept-Language: en-US,en;q=0.9`（注意：1.1.4 release notes 说**移除了**强制英文改写——以 release notes 为准，README 有滞后）。
  - 1.1.5 新增 **OpenAI BPS 平台**（ChatGPT Excel/BPS 上游，HTTP Responses，BPS Token 手动、无自动续期、**首版不支持 WebSocket**、需工具协议转换、Redis 必需）。
  - 1.1.4 关键修复：票失效判定改为「实际发送了该代次票 **且** 上游返回的 turn-state 与 `__oailb` **同时变化**」；修复 WS 复用旧连接把历史握手状态关联到新票代次导致误判失效。
- 可信度：第一方文档（高）。
- 可落地性：**极高**。做替代方案前先确认这些已知坑：票失效判定、WS 重连代次、BPS 无 WS、指纹收敛。

### C4. `codex_state_monitor`（2026-09-20）：对官方源码的逐条核对 + 社群数据
- URL: https://github.com/YueDongSun/codex_state_monitor
- 日期：2026-09-20（核对 `openai/codex main @ 5c5308fc`）。
- 结论：
  - 源码级：turn-state 由 PR #9332（2026-01-16）引入，per-turn sticky，`Arc<OnceLock<String>>` 先写优先，同轮回传、跨轮清空，注释 "must not be sent between different turns"，**客户端零校验**。
  - 源码级：`safety_buffering.rs` 解析 `x-codex-safety-buffering-enabled` / `-faster-model`；#46632 + community.openai.com 1399076 抓包证明 astra→luna。
  - 规模：**2026-09-01~09-20 openai/codex 新增 134 个相关 issue**，9/17 单日爆发 #46139–#46231；多数配额远未耗尽、错误藏在 HTTP 200 流里、**零 429** → 状态页的 HTTP 级监控结构性看不见。
  - 官方部分承认：Codex 负责人 **Tibo Sottiaux 2026-09-12** 公布修复三项质量缺陷，其中「badly configured engines that resulted in a measured quality degradation for a long tail of traffic（removed）」；9/7 有全局用量重置。
  - 先例：2026-02 有「网络安全滥用风险」账号被静默 GPT-5.3-Codex → GPT-5.2 的账号级降级路由。
  - 社区启发式：个人 292/312、Team 332/356，**20 字符差 = 多一个 16 字节密文块**（Fernet），个人 10→11 块、Team 12→13 块；工具作者自承「292 不是官方质量指标」。观察到的**抖动**：1 小时内 312→292→312→292 逐请求变化。
  - 明确立场：**不注入、不重放、不采集** turn-state；跨回合/跨 IP 复用违反契约、已被作为限流绕过上报、有封号风险。
- 可信度：源码核对（高）+ 社区数据（中）。
- 可落地性：**极高**。建议直接把它的判据搬进 sub4api 的「降智检测」：`请求模型 == 流内 served model` 才是权威判据；票长只作提示。并且**票形校验要能兼容 780**（B4）。

### C5. `ccodex-sleep-state`（gylive，2026-09-18~20）：打票工程化的另一种实现
- URL: https://github.com/gylive/ccodex-sleep-state
- 日期：2026-09-18 ~ 09-20（v0.4.0）。
- 结论：
  - 处理 `X-Codex-Turn-State`：从候选出口发短请求，检查**封装、时间、密文块数**；个人「10 块，通常 292 字符」，Team「12 块，通常 332 字符」；**不把 11 块/312、13 块/356 当合格值**；state 按**账号、凭据、模型**隔离，不跨 Astra/Sol/Terra 混用。
  - 严格模式 vs 普通转发兜底；401/403/429 停止规则；**不重放**已发到上游的正式生成。
  - 边界：**只走 Responses HTTP/SSE，不支持 WebSocket 和任意协议转换**；采集消耗真实额度；「292 不是官方质量指标，长度符合不证明满血」。
- 可信度：第三方开源实现（中高，作者自述未全部验收）。
- 可落地性：高。它的「块数+封装+时间」校验比单纯长度稳，且已把「票只是路由令牌」写进文档；但**同样会撞上 780 变化**（除非它已适配）。

### C6. linux.do「780 爽用方法」帖（2026-09-24）
- URL: https://linux.do/t/topic/2947373/41 （同主题 /40）
- 日期：2026-09-24。
- 结论（社区回帖，只拿到摘要/片段）：
  - 「**cpa 上把正常号的票据用在降智号上面没有解除降智**」「**turnstate 跟号绑定**」→ 直接否定"共享票"路线；
  - 「打票封号」：有人「为了打 95/88 之类的，打的时间长了，早上起来封号了」；
  - 「透明模式是只探测不注入吗」（讨论探测/注入分离）。
- 可信度：社区回帖（低-中，无可复现）。
- 可落地性：**高（反向）**。为「不要跨账号共享票」「打票有封号风险」提供社区证据；也提示产品上应支持"只探测不注入"。

### C7. locdd「292 state 注入原理」搬运帖（2026-09-17）
- URL: https://locdd.com/t/topic/92276
- 日期：2026-09-17（作者声明「非原创，只是搬运」，原文标题未给出）。
- 结论：
  - 把机制描述为「HTTP **292** 状态码 + `current_turn_state`」：292=调度正常、下发签名 state；200=降级；state TTL≈1 小时；**state 的获取与使用不绑定 IP**（"房卡"比喻）。
  - 方案 `codex-state-kit`：`keeper.py` 每 45s 检查/提前 5 分钟用住宅 IP 续期 + `inject_proxy` 注入 header + Clash 切线路。
  - IP 质量分级：美国住宅 > 原生 v6 > 欧亚住宅 > 数据中心 > 共享代理；Pro/20X 比 Plus 更容易拿到 292；工作日美西 10:00–15:00 收紧。
  - 也承认：state 与**模型**要对齐（用 astra 采的票给 astra 用最有效）；多账号需各自 keeper；滥用会招致绑定 IP/缩短 TTL。
- 可信度：**低-中**。搬运文，且「HTTP 292 状态码」这个说法与官方/网关侧观测**不一致**（实际是响应头长度 292/312；292 并非标准状态码语义），应视为二手解释；但「票≈路由凭证、TTL≈1h、按模型隔离」与 C2/C4/C5 互相印证。
- 可落地性：中。可作为「原理直觉」参考，**不要**按文中"注入共享 state"实现（与 A6/C6 冲突）。

### C8. 其它零散社区信号（低可信，仅供氛围判断）
- https://linux.do/t/topic/2920223/36 （2026-09-17）：「刚换 IP 能拿到 292，而且能注入，但过一会返回的还是会变成 312」→ 抖动/快速失效。
- https://locdd.com/t/topic/94715 （2026-09-24）：「打票方案确实拉闸了，研究 780 特征」；「中转站也在降智中，最近没什么好办法」。
- https://locdd.com/t/topic/93184 （2026-09-20/21）：「建议别用 sub2api 了，**tibo 专门点名说过不支持的**」；同帖回复「账号降智好像静置一小时可以正常一下然后又开启降智」。
- https://macos.do/t/topic/114 （2026-09-23，标题「悲报：最新的 GPT 6 Astra 降智不再返回上游真实路由模型」）：搜索摘要显示作者称「准备把 292 功能删除回退掉，因为没什么用了」「是的打票没用了」。**原文 fetch 超时，未取得全文** → 标注未验证。
- https://macos.do/t/topic/81/100 （2026-09-25）：「降智号第一次拿到很容易打上票，后面就几乎打不上了」；作者称下个版本准备**默认关闭打票、仅保留时区替换**。（正文只拿到标题+摘要，未取得全文。）

### C9. 另一个主流 fork：ranxi2001/sub2api（2026-10）——「不以票据形态判断质量」
- URL: https://github.com/ranxi2001/sub2api
- 日期：HEAD 2026-10-22。
- 结论：
  - **账号质量运维**：定时「答题检测」+ 判题记录 + 失败处置/自动恢复，按模型隔离；明确写「**检测结果以实际回答和判题证据为准，不以票据形态判断质量**」。
  - **Excel / Basispoints(BPS)**：模型级 BPS 路由、图片中转、磁盘/并发保护、结构化输出校验、工具历史与 transport 恢复；不支持的托管工具按策略**回退原生 Codex 或保持 BPS 省略工具**。
  - **Mihomo 出口管理**：订阅/动态代理/节点状态，按账号固定出口。
  - 文档提到「**780 / 292 打票边界**」与「降智运维步骤」（新人入门文档）。
- 可信度：第三方 fork 文档（中高）。
- 可落地性：**极高**。这是「打票退化为可选、质量运维 + BPS 成为主路径」的现成蓝本，且与 sub4api 的 1.1.5 BPS 方向一致。

---

## D. 综合判断：2026-09~10 到底发生了什么

1. **2026-09-17/18：降智+「at capacity」大爆发**（官方仓库 134 issues；#46632 抓包 astra→luna；官方 09-12 承认「badly configured engines」并移除）。同日官方客户端**改走 WebSocket transport**（#46632 环境段）。
2. **2026-09-22：票据形态改变** —— `x-codex-turn-state` 从 292/332 变 **~780 字符**（sub2api #8104 明确写出这个日期）。任何「按长度 292/312/332/356 判合格」的实现在这一刻集体失效 → 与「打票失败/空票」时间线吻合。
3. **2026-09 下旬：中文社区转向「票只是路由、打票只能换 200 秒/240 秒」**，sub4api 1.1.4 默认关闭打票、改推时区替换；社区出现 BPS/Excel 旁路。
4. **Fast / priority 的传输层结论被推翻**：2026-07 实测「priority 只在 WS 生效」，2026-10 同一 PR 作者关闭并称「HTTP/SSE 已能尊重 priority」。→ **结论具有时效性，必须自测**。
5. **官方定价/资格线明确化**：Fast = priority/fast、2.5×；Ultrafast = astra/6.1sol、仅 Pro $500/Enterprise、8×；GPT-5.5 于 2026-10-14 退役（与 sub2api 0.2.17 的 "GPT-5.5 退役" 修复一致）。
6. **上游对打票的态度**：官方客户端契约明确「turn-state 不得跨回合」；sub2api 官方 0.2.6 加过又撤了；社区有封号报告与「被 Tibo 点名」的说法。→ 打票是**高 ToS 风险**路线。

---

## E. 最值得试的候选方案（按「性价比 × 风险」排序）

### 候选 1（立刻做，低风险）：把票长校验改造成「形态自适应 + 不当质量指标」
- 依据：B4（780 自 2026-09-22）、C4（292 非官方指标）、C5（块数校验）。
- 做法：接受 780（及未来形态），用「Fernet 信封可解析 + 块数 + 时间窗」做格式校验，长度只做 soft 判断；**票长不参与"是否降智/是否失效"**；顺手修 `Header().Add` 重复写（B4 的 502 坑）。
- 预期：把「打票一直失败/空票/票失效流水刷屏」降级为「票照常回传但不再声称能防降智」。

### 候选 2（协议正确性，低-中风险）：turn-state 从「全站共享注入」改为「按账号+回合回传/剥离」
- 依据：A6/A7（官方契约：per-turn、不跨回合、不跨账号）、B1（chord 实现）、B2（sub2api PR #5668 官方配方）、C6（社区实测跨号无效）。
- 做法：三处捕获（SSE `response.metadata.headers` / HTTP header / WS 握手）→ 按 (provider, account) 分桶 → 同回合后续请求（含 `/responses/compact`、WS 每帧）回带 → 跨账号剥离；**只回传+剥离，不主动注入别人的票**。压缩后记得重建 WS 连接（A7 的坑）。
- 预期：协议链正确，不再有"跨号污染路由"和封号面；代价是**放弃"防降智"承诺**。

### 候选 3（推荐主方向，中风险）：降智判据换成「served model / safety-buffering」，账号运维化
- 依据：A4/A5（safety-buffering 头 + 流内 model）、C4（权威判据 = 请求模型==返回模型）、C9（ranxi：不以票形判断质量）。
- 做法：每个账号/模型发低推理短探测，比对 `response.created.response.model` vs 请求模型 + 读 `x-codex-safety-buffering-*`；命中即标记"疑似降智"并冷却/换号；可选**剥离**这两个头避免客户端被提示切弱模型（B6 的 `dropCodexSafetyBuffering`，注意 WS metadata/compact 漏点）。
- 预期：可解释、可观测、不依赖票；sub4api 已有"降智检测"骨架，改判据即可。

### 候选 4（Fast 路线，中风险，需实测）：用官方 Fast/Ultrafast 替代"VIP 票"
- 依据：A1/A2/A3/A9 + B5（传输层结论反转过）+ B6（`fast_mode=true` 前置）+ B7（ultrafast 需目录广告 & entitlement）。
- 做法：① 给 Codex 客户端下发 `service_tier="fast"` + `[features].fast_mode=true`；② 网关 `forced_service_tier=priority` 时做 **HTTP/SSE vs upstream-WS 的 A/B**（输出 TPS + `x-codex-primary-used-percent` 消耗），据实测决定是否桥 WS；③ `ultrafast` 仅在 Astra/6.1 Sol 且账号有 Pro $500/Enterprise entitlement 时放行，且目录未广告就回落 priority；④ **不要**用响应回显 `service_tier` 判断生效。
- 预期：拿到真实 1.5×~2.5× 提速（这是官方承诺、非"防降智"），计费 2.5× 要提前告知用户。

### 候选 5（旁路，中-高风险）：BPS / Excel 上游作为"不降智"替代通道
- 依据：C1（`bps.openai.com/basispoints/api/responses` + `x-basispoints-auth-mode: chatgpt`，社区称"满血"）、sub4api 1.1.5 已内置 BPS、C9（ranxi 的 BPS 路由/回退策略）。
- 做法：把 BPS 做成独立平台/分组（sub4api 已有），映射 `gpt-5.6-luna → gpt-5.6-sol`（客户端会用 luna 做标题/审批）；工具协议转换走文本目录 + `run_officejs`（1.1.5 已实现）；注意 **BPS 首版无 WS**、无 max effort、约 600k 上下文强制压缩、Token 手动续期。
- 风险：非官方协议、账号限制/封禁、Token 过期；必须默认关闭 + 明确风险提示。

### 候选 6（零成本兜底）：时区/环境上下文治理保持开启
- 依据：C1/C2（作者结论：非中国大陆时区；sub4api 已默认 `Asia/Singapore`）。
- 做法：保持每账号时区替换（可做成与出口 IP 一致），保留 `Accept-Language` 透传（1.1.4 已移除强制改写）。
- 说明：证据等级弱（作者自述 + 经验），但成本近乎零、无 ToS 风险，适合作为"养号"默认项。

---

## F. 明确"无法验证 / 需自测"的清单
- 780 是否真的从 2026-09-22 起全局生效：只有 sub2api issue #8104 一个来源（虽质量高），需用本环境实际响应头复核（看当前票长分布）。
- priority 在 HTTP/SSE 上是否**现在**生效：B5 前后矛盾，必须 A/B 实测；回显不可信。
- `x-codex-routing-hint` 与 transport/池亲和的实际影响：仓库注释已标 "impact unverified"（见 `scratch/notes-fast-path.md` §4），无公开资料。
- Ultrafast 的 entitlement 判定：官方只写"Pro $500 / eligible Enterprise/Edu"，具体账号是否可用的判定只能靠上游返回探测。
- 「Tibo 点名 sub2api 不支持」「sub2api 0.2.6 打票被强制回退」：社区传闻，未见官方原文。
- locdd 92276 的「HTTP 292 状态码」说法：与官方/网关观测矛盾，判为二手误读，不作为依据。

## G. 引用清单（便于复核）
官方：https://learn.chatgpt.com/docs/agent-configuration/speed ｜ https://developers.openai.com/api/docs/guides/fast-mode ｜ https://developers.openai.com/api/docs/guides/ultrafast-mode ｜ https://github.com/openai/codex/blob/main/codex-rs/codex-api/src/safety_buffering.rs ｜ https://github.com/openai/codex/issues/46632 ｜ https://github.com/openai/codex/pull/9332 ｜ https://github.com/openai/codex/pull/27931 ｜ https://github.com/openai/codex/issues/14204 ｜ https://github.com/openai/codex/commit/178c3d30053ba63d118ee93a0dbe2716a12e5769
网关/客户端实现：https://github.com/keakon/chord/commit/5c2c5e44a41bbcdce69dd325fb229352ea88ab32 ｜ https://github.com/Wei-Shaw/sub2api/pull/5668 ｜ https://github.com/Wei-Shaw/sub2api/releases/tag/v0.1.177 ｜ https://newreleases.io/project/github/Wei-Shaw/sub2api/release/v0.2.6 ｜ https://github.com/Wei-Shaw/sub2api/issues/8104 ｜ https://github.com/router-for-me/CLIProxyAPI/issues/4586 ｜ https://github.com/router-for-me/CLIProxyAPI/pull/4587 ｜ https://github.com/lidge-jun/opencodex/blob/main/docs-site/src/content/docs/zh-cn/reference/configuration/server.md ｜ https://github.com/openclaw/openclaw/blob/main/docs/tools/thinking.md
社区：https://macos.do/t/topic/138 ｜ https://macos.do/t/topic/81 ｜ https://github.com/MACOS-DO/sub4api ｜ https://github.com/MACOS-DO/sub4api/releases ｜ https://github.com/YueDongSun/codex_state_monitor ｜ https://github.com/gylive/ccodex-sleep-state ｜ https://linux.do/t/topic/2947373/41 ｜ https://linux.do/t/topic/2920223/36 ｜ https://locdd.com/t/topic/92276 ｜ https://locdd.com/t/topic/94715 ｜ https://locdd.com/t/topic/93184 ｜ https://macos.do/t/topic/114 ｜ https://github.com/ranxi2001/sub2api

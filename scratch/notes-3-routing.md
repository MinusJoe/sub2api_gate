# 角 3：降智判据与路由 —— served-model / 档位 / 路由驻留信号（证据版）

作者：agent3-routing（角 3）。只读取证；**未执行 codex.exe**；未改动任何原始文件。
产出仅本文件 `scratch/notes-3-routing.md`；临时分析脚本在 `scratch/tmp/agent3/`（`served_model.py`/`step_model.py`/`d1..d3.py`，供复核）。
token/cookie/turn-state 一律只报长度/前缀/是否出现/时间/位置，**不贴明文**。

> 结论分级：**观察** = 二进制字符串/指令或本机日志直接可见；**推断** = 组合出的行为判断；**未知** = 现有证据无法回答。
> 引用既有笔记时标注为「二手」，不当作我的一手证据；本文件里「[观察]」默认指我在本轮新做的一手观察。

---

## 0. TL;DR（先看）

1. **降智的权威判据不是票长、不是 turn-state、不是 `service_tier` 回显，而是「请求模型 vs 流内实际被服务模型」**：
   `response.created.response.model`（SSE/WS 帧）或 `openai-model`（`response.metadata` 头）**直接命中**；`x-codex-safety-buffering-enabled`/`-faster-model` 是服务端「此模型拥塞/可换更快(更弱)模型」的**提示**。
2. **这两个「实际模型」信号在本机日志里都拿不到**（`openai-model` 全库 0 次、`response.created` 全库 0 次——SSE body 不落日志）。日志里**唯一**能拿到的降智旁证是
   `x-codex-safety-buffering-faster-model: gpt-6-luna`，它在 **100%** 的 `gpt-6-astra`(103) + `gpt-6.1-sol`(106) HTTP 响应上出现（209/347），`codex-auto-review`/guardian 上**从不出现**。
3. **`service_tier` 是「提速档」，不是「质量档」**：官方口径只有 `fast/priority`(1.5–2.5×) 与 `ultrafast`(6–8×，仅 astra/6.1-sol，需 Pro $500/Enterprise) 是「改变服务」的档位，且都只承诺**速度**；它们**不解除降智**。本账号 `feature.fast_mode=true`、`feature.ultrafast_mode=true`、`feature.step_model_switching=false`（三者恒定）。
4. **`x-codex-routing-hint` / `responses-lite` / `residency` / `code-mode-route` 全部是「客户端→服务端的请求头」（指纹/上下文信号），不是 VIP 票据**：官方客户端自己就会发，网关仿造它们只能「更像官方客户端」，不能「换取不降智」。其中 `responses-lite` 的值是**字面量 `true`**（长度 4，已从二进制确认）。
5. **打票/共享票治不了降智的根本原因**：turn-state 是 **per-turn 粘性路由票据**（官方契约：同回合内回带、跨回合/跨账号必须清空），它不是质量凭证；票长 2026-09-22 已从 292/332 变 780；而降智是**服务端路由决定**（safety-buffering 头 + 流内换模），任何本地票据都无法覆盖它。

---

## 1. served-model 判定信号（Q1）

### 1.1 `x-codex-safety-buffering-enabled` / `-faster-model`（响应头，降智提示）

- **[观察] 二进制**：单函数 `0x3d56430`（343B）从「响应头 map」读这两个头：
  - `0x3d5643f` `lea rdx, "x-codex-safety-buffering-enabled…"` + `mov r8,0x20`(32) → `get_header`；
  - `0x3d56460/0x3d56486` 两处都以 `r8=0x25`(37) 读 `x-codex-safety-buffering-faster-model`；
  - 结果构造成 `SafetyBufferingTreatment { faster_model }`（struct 字段在 `rsi` 输出区，`0x3d5651a..0x3d56524` 写 `tag/faster_model ptr/len`）。
  - 关键：`-enabled` 的**值**在函数里只用来选分支（有/无都继续读 `-faster-model`），**不 gate 结果** —— 与官方 `codex-api/src/safety_buffering.rs` 单测固定的行为一致（二手：notes-community A4）。
- **[观察] 运行时**（合并两份 logs 副本，35,810 行去重）：
  | 请求模型 | HTTP /responses 完成 | 带 `enabled=true`+`faster=gpt-6-luna` | 占比 |
  |---|---|---|---|
  | gpt-6.1-sol | 106 | 106 | 100% |
  | gpt-6-astra | 103 | 103 | 100% |
  | codex-auto-review | 135 | **0** | 0% |
  | 无 model 字段(guardian) | 3 | **0** | 0% |
  - 值恒定 `true` / `gpt-6-luna`，首次 2026-10-21 21:54:34 JST，最后 2026-10-23 00:47:24 JST。
- **[推断]** 这是服务端对 `astra`/`6.1-sol` 说「当前模型拥塞/被降载，可用**更快(但更弱)的 `gpt-6-luna`** 重试」的提示；客户端据此在 UI 提示并默认切到弱模型（二手 A4/A5）。它是**换模提示，不是换模证明**。

### 1.2 `openai-model`（实际被服务模型的回显）

- **[观察] 二进制**：客户端读它。`0x3d135c0`（`codex-api/src/sse/responses.rs`，3310B）+ 6 个 WS connector 单态化（`0x816a940/0x89d4830/0x9c142b0/0xa93a7d0/0xa9f0180/0x27d2490`），都从「响应头 map」里 `get_header("openai-model")`。
- **[观察] 运行时**：`openai-model` 在**整个合并日志里 0 次出现**（含 347 条 `/responses` HTTP 响应头的 `headers={…}` dump）。
- **[推断]** `openai-model` **不是本传输上的 HTTP 响应头**，而是通过 `response.metadata`（SSE/WS 帧，客户端不落日志）下发。→ 本机日志**观测不到**实际被服务模型。

### 1.3 SSE `response.created.response.model`（流内实际模型）

- **[观察] 运行时**：`response.created` / `response.metadata` 在全库 **0 次**（客户端不落 SSE body）。
- **[推断]** 这是**唯一权威**的「实际服务模型」信号（二手 A5 #46632：astra 被 luna 服务时该字段=`gpt-5.6-luna`，且伴随 safety-buffering 头），但需要抓包/在网关读 SSE body 才能拿到。当前打票代码（二手 notes-harvest-flow §3.1）只认 SSE 的 `output_text.delta`/`response.completed`，**不读 `response.created.response.model`**。

### 1.4 「请求模型 vs 实际模型」是否真的不一致

- **[未知]** 本机日志无法坐实「请求 gpt-6.1-sol、实际被 gpt-6-luna 服务」——两个能证明换模的信号（`response.created.response.model`、`openai-model`）都不落日志。
- **[观察]** 能拿到的旁证只有：`x-codex-safety-buffering-faster-model=gpt-6-luna` 在 **100%** astra/6.1-sol 响应上出现。这与「服务端在把这些模型往 luna 引导」一致，但**不构成换模证明**（`gpt-6-luna` 本身也是客户端子 agent 请求过的模型，是干扰项——二手 notes-client-runtime §5.4）。
- **[推断]** 权威判据 = **请求模型 == 流内 `response.created.response.model`（或 `openai-model` metadata）**；本机日志结构上测不到，网关需要在 SSE/WS body 层做对比。

---

## 2. 服务档位（Q2）

### 2.1 取值与等价关系

| 层 | 值 | 来源 |
|---|---|---|
| 客户端 config.toml | `service_tier = "default"`（模型 gpt-6.1-sol, xhigh） | [观察] notes-runtime §C |
| 客户端 thread 级覆盖 | `default`(671+50 turn) / `priority`(436+351 turn) | [观察] 两份 notes 合并 |
| 官方等价 | `fast` ≡ `priority`（盘存写 `fast`，请求发 `priority`）；`flex` 保留 | [观察] 二手 A9 |
| 网关/API | `default/priority/fast/flex/ultrafast`；`fast`→`priority` 归一 | [观察] 二手 notes-fast-path §1 |

### 2.2 特性开关（server 下发，本机恒定）

- **[观察]** `feedback_tags` 2,051 行里：`feature.fast_mode=true`、`feature.ultrafast_mode=true`、`feature.step_model_switching=false`（三者**全程恒定**）。
- **[观察]** `feature.responses_websockets=false`、`feature.responses_websockets_v2=false`，**但客户端实际用了 WS（108 次成功连接）** → 传输选择由 provider 配置 `supports_websockets`（+ `codex.websocket.continuation`）驱动，**不是这两个特性开关**（二手 notes-binary §3）。
- **[推断]** `step_model_switching` 是「多步 agentic 任务里逐步换模型」的独立特性（本账号关闭）；它与 `fast_mode`（单档提速）、`safety_buffering`（降智回退）是**三个不同的东西**，别混为一谈。二进制里它只出现在大请求构建函数 `0x86d94c0`（28436B，含 `openai/entitlementContext`、`token_budget.context_window`、`<multi_agent_mode>`）。

### 2.3 entitlement 头语义（消费/额度记账，非路由/质量）

- **[观察]** 347 条 `/responses` 响应头恒定：`x-codex-plan-type: pro`、`x-codex-active-limit: premium`、`x-codex-credits-has-credits: True`、`x-codex-credits-balance: 63000.0000000000`（**全程不变**）、`x-codex-credits-unlimited: False`、`x-codex-primary-window-minutes: 10080`。
- **[观察]** `x-codex-primary-used-percent` 从 29% 单调升到 46%，期间在 **10/22 16:16 JST 附近重置 100%→1%**（新窗口）；`codex-auto-review` 请求密集时上升最快。
- **[推断]** 这些头是**订阅计划/额度/限流窗的记账信号**，回答「还剩多少额度、什么 plan」，**不携带 tier、不携带路由、不携带质量**。`credits-balance=63000` 恒定说明它是独立于 primary window 的额度池（未在本窗口被扣减），`primary-used-percent` 才是消费速率的直接观测。

### 2.4 哪个档位在哪个传输上生效

- **[观察]** `service_tier` 写在 **HTTP body** 和 **WS `response.create` 帧**两条路径（二手 notes-fast-path §1.4/§1.7）；客户端 tier 参与请求指纹（`service_tier_changed` 触发 non-incremental request，二手 notes-runtime B1）。
- **[观察]** 本机 **HTTP 响应头里 0 次 `service_tier` 回显**（全库 `service_tier` 只在 feedback_tags/thread_settings，2045 次，无响应侧）。
- **[推断]** 用「回显 tier」判断是否生效**不可行**；官方员工口径「service_tier 不是可端到端验证的字段」（二手 A8）。生效性只能靠 **A/B 实测输出 TPS + `x-codex-primary-used-percent` 消耗速率**（二手 B5：2026-07 实测 priority 仅 WS 生效 1.55×，HTTP 0.97×；2026-10 该 PR 作者称 HTTP/SSE 已尊重 priority → **结论有反转，必须本环境自测**）。本机数据因模型混合（auto-review 廉价模型占大头）无法干净分离 default/priority 的消耗速率。

---

## 3. 路由 / 驻留类信号（Q3）

> 全部为**客户端→服务端的请求头**；本机日志 **0 次**（客户端不记录自己发什么）。值/语义只能从二进制定。

| 信号 | 性质 / 值 | 二进制证据 | 是否可复用 |
|---|---|---|---|
| `x-codex-routing-hint` | 请求头；值为**本地计算**的 `model=<slug>[;tier=priority\|flex\|ultrafast]`（default/auto 折叠为 model-only） | [观察] 10 个 WS turn 构建函数写它（`0x6e81c80/0x6e896a0/0x74633f0/0x745ac00/0x7d60560/0x8618ef0/0xa8b4780/0x7d5c6f0/0x872dc30/0xa8b04c0`）；insert 点 `0x6e833bc`，值来自 `fn_0x77d4220` 计算产物（非常量）。格式见二手 notes-fast-path §4 | 网关已自行合成（`openai_routing_hint.go`）；是「路由/连接池亲和提示」，不是特权票 |
| `x-openai-internal-codex-responses-lite` | 请求头；**值 = 字面量 `"true"`（长度 4）**，受 struct 偏移 `0xb19` 的某特性位 gate | [观察] `0x6e82c05` `lea rax,"true…"` + `0x6e82c13` `mov …,0x4`，`0x6e82c3e` insert（`r8=0x26`=38=头名长度）；guardian/subagent 路径 `0x9cca2e0` 同源 | 「responses-lite」内部通道开关；常量 `true`，伪造无门槛，但只是通道提示 |
| `x-openai-internal-codex-residency` | 请求头；数据驻留区域信号（簇内邻接 `core`） | [观察] UA/头构造 `0x9fb7150`、apps-api serde 字段 `0x550ea10/0xabe5920`、头名检索 `0x6d73120`(1441B)、`0xe04ad40`(686B) | 区域/驻留信号；不是质量票 |
| `x-code-mode-route`（+ `session routing token is too long`） | code-mode **宿主**自己的路由 token 机制，与 `/responses` 的模型路由无关 | [观察] 16 个函数（`0x5eacce0` 8788B 含「session routing token is too long」、`0x5f36f20` 9123B 含 `x-code-mode-route-lost`、5 个小 getter `0x5f07c90…`） | 仅 code-mode 宿主；对 /responses 不可复用 |

**可复用性裁决**：这四者都是官方客户端指纹/上下文的一部分，**不是 VIP 票**。仿造它们只让请求「更像官方客户端」，不能换取「不降智」。唯一有路由语义的是 `routing-hint`（model+tier），且网关本就在从最终 body 派生它。

---

## 4. 结论（Q4）

### ① 降智的**权威判据**是什么

- **判据 = 请求模型 是否等于 流内实际被服务模型**：
  - 权威信号：SSE/WS 帧 `response.created.response.model`（或 `response.metadata` 里的 `openai-model`）。
  - 降智提示（非证明）：响应头 `x-codex-safety-buffering-enabled` / `-faster-model`（本机 = `true` / `gpt-6-luna`，100% 命中 astra/6.1-sol）。
  - **非判据**：`x-codex-turn-state` 长度（292/312/332/356/780）、`service_tier` 回显、turn-state 有无、`__oailb` 有无 —— 这些都**不是质量指标**（二手 A8/C4/C5；官方员工原话「service_tier 不是可端到端验证的字段」）。
  - 现有打票的 ModelTrace 指纹是**代理判据**（二手 notes-harvest-flow：`fingerprint_mismatch` 能抓换模），但直接信号是流内模型，当前网关没读。

### ② 打票 / 共享票为什么治不了降智

1. **票的契约就不允许共享**：官方 `x-codex-turn-state` 是 per-turn 粘性路由票据（OnceLock 先写优先、同回合回带、**跨回合/跨账号必须清空**）（二手 A6/A7/C4）。「抓一张票全站注入」违反作用域，且已有社区实证「正常号的票用在降智号上无效、turn-state 跟号绑定」（二手 C6）。
2. **票形已变**：2026-09-22 起票长 292/332 → **780**（本机 10/21–10/23 观测恒 780 / `gAAAAA` / base64url / 585B）（二手 B4 + 一手 notes-client-runtime）；按 292 校验/截断会直接收不到票。
3. **票 ≠ 质量**：即使票有效，它只「把同一回合粘在一条路由上」，不决定「这条路由是满血还是降智」。降智是**服务端路由决定**（safety-buffering 头 + 流内换模），任何本地票据无法覆盖。
4. **探针形状本身可被识破**（二手 notes-harvest-flow §9）：HTTP/1.1+close、`codex-tui/0.146.0` 头 + `Codex Desktop` body、无 tools、无 `x-codex-beta-features`/`routing-hint`/回带旧票 —— 与真实客户端（H2/WS、0.163.0、带 tools）指纹不一致。
5. **假成功**：`/responses` 上唯一的 Set-Cookie 是 `__cf_bm`（`__oailb` 只在 `GET /models` 下发，128 次成对 `__cflb`），打票把任意 cookie 当凭证 → 「cookie-only 假票」（一手 notes-client-runtime §4 + 二手 notes-harvest-flow §3.2/§5）。

### ③ 有没有**真正**能影响模型质量 / 档位的可操作手段（与「只是提速」区分）

**先说清楚「档位 ≠ 质量」**：官方所有档位都只承诺**速度**（fast 1.5–2.5×、ultrafast 6–8×），没有一个档位叫「不降智」。

| 手段 | 影响的是 | 可行性 / 风险 / 成本 |
|---|---|---|
| `service_tier=priority`（+ `fast_mode=true`） | **速度**（1.5–2.5×，计费 2.5×） | 官方支持、低风险；**不解除降智**。生效性需 A/B（HTTP vs WS 有历史反转）。网关已有 `forced_service_tier` |
| `service_tier=ultrafast` | **速度**（6–8×，计费 6–8×） | 仅 `gpt-6-astra`/`gpt-6.1-sol` + Pro $500/Enterprise entitlement；目录未广告应回落 priority；本账号 `feature.ultrafast_mode=true` 但 entitlement 未证实 |
| **检测降智 → 账号冷却/换号** | **质量** | 改判据为「served model + safety-buffering 头」，命中即标记/冷却/切号（二手 C9 ranxi 路线）。**这是唯一真正作用在「质量」上的手段**，且不依赖票 |
| **剥离 safety-buffering 头**（dropCodexSafetyBuffering） | 阻止客户端被提示切弱模型 | 网关层可选；需覆盖 WS `response.metadata` + `/responses/compact`（二手 B6 明确漏点） |
| BPS/Excel 旁路 | 换上游通道 | 社区称「满血」但非官方、无 WS、无 max effort、~600k 压缩、token 手动续期（二手 C1/C9）；中高风险 |
| 时区/环境上下文治理 | 养号（弱证据） | 成本≈0、无 ToS 风险；sub4api 已有（默认 Asia/Singapore）（二手 C1/C2） |
| turn-state 正确回传（per-account/per-turn） | 路由连续性（合规） | 低-中风险、官方契约正确做法；但**放弃「防降智」承诺**（二手 B1/B2） |

**一句话**：能「真正影响质量」的只有**检测 + 账号运维**（换号/冷却/切 BPS）；`service_tier` 是官方唯一「档位」但只管速度；turn-state/打票既不是质量信号、也不能覆盖服务端降智路由。

---

## 5. 未知 / 局限

1. `response.created.response.model` 与 `openai-model` 的**实际值**本机观测不到（SSE body 不落日志）→ 需要抓包或网关读 SSE/WS body 才能坐实「请求模型 vs 实际模型」。
2. `x-codex-routing-hint` 的精确值格式（`model=…;tier=…` 的拼接细节）只从网关仓库二手确认，客户端侧只确认「由 `fn_0x77d4220` 计算产物、非常量」。
3. `x-openai-internal-codex-residency` 的**具体值**（是否=`core`/region）只看到簇内邻接 `core`，未逐字节还原。
4. 原生 REA provider 不可用（本轮 `binary_session` 一手复核：ghidra `not_configured`——`GHIDRA_INSTALL_DIR` 未设且 `java.exe` 无法解析为绝对路径；hopper/ida/kuna 亦 not_configured/unsupported，`analysis_provider_binding=null`；ghidra 对 PE x86-64 目标 `target_support=supported` 但无安装）。二进制证据来自自写静态解码器（`x86.py`/`disasm.py`），Rust 函数只追到「字符串引用 + 头名/长度 + 值常量」粒度，未做完整伪代码级还原。
5. 「priority 是否在本机 HTTP/SSE 上生效」**未自测**（缺 A/B 场景与 TPS 计量），仅记录历史反转与判据建议。

---

## 6. 复现

```powershell
cd D:\projects\sub4api
python scratch\tmp\agent3\served_model.py   # served-model / safety-buffering 交叉分布
python scratch\tmp\agent3\step_model.py     # step_model_switching / 特性开关 / 额度时间线
cd scratch\tmp\binre
python fnstr.py 0x3d56430   # safety-buffering 解析器
python x86.py 0x6e82b40 0x6e82d20   # responses-lite 值="true" 的插入点
python x86.py 0x6e832c0 0x6e834a0   # routing-hint 值来源
```

关键字符串 RVA（`.rdata`，供后续精确 xref）：`x-codex-safety-buffering-*`@`0xf8c2408`、`openai-model`@`0xf7ae540`、`x-codex-routing-hint`@`0xfb6d4c8`、`x-openai-internal-codex-responses-lite`@`0xfb39a98`/`0xfcd32d7`、`x-openai-internal-codex-residency`@`0xfbda2eb`/`0xfa353d0`、`x-code-mode-route`@`0xfa83410`/`0xfa885c8`。

---

## 7. 从角 3 出发的方案建议

**最推荐（1 条）**：把「降智检测」判据从**票形**换成**流内实际模型** —— 在网关读 SSE/WS 的 `response.created.response.model`（及 `response.metadata` 的 `openai-model`）与请求模型比对，并记录 `x-codex-safety-buffering-enabled/-faster-model` 头；命中即标记账号/模型「疑似降智」并冷却/换号。可行性**高**（sub4api 已有「降智检测」骨架，只改判据 + 读 SSE body），风险**低-中**（不违反上游 ToS，只做观测+运维），成本**低-中**（需要把响应侧解析从「只认 output_text/completed」扩展到 `response.created`/`metadata`，含 WS 帧）。

**次选（2 条）**：
1. **把 turn-state 从「全站共享注入」改为「per-account/per-turn 回传+跨账号剥离」**（官方契约，二手 B1/B2）。可行性**高**（代码骨架清晰）、风险**中**（放弃「防降智」承诺，需覆盖 WS 每帧 + `/responses/compact`，否则压缩后路由漂移）、成本**中**。
2. **把 `service_tier` 当「提速」而非「防降智」来运营**：`forced_service_tier=priority` 时做 **HTTP/SSE vs upstream-WS 的 A/B**（输出 TPS + `x-codex-primary-used-percent` 消耗速率），`ultrafast` 仅在 astra/6.1-sol 且 entitlement 实锤时放行；同时给客户端下发 `service_tier=fast`+`[features].fast_mode=true`（否则客户端自己丢 tier）。可行性**中**（需实测，历史结论反转）、风险**低**（官方功能，但计费 2.5–8× 要提前告知）、成本**中**。

# 角 B：官方对「打票/共享票据」的执法动作证据与时间线（封号/警告/官方口径）

作者：forensic analyst（角 B）。工作目录 `D:\projects\sub4api`。成文时间 2026-10-23（Asia/Tokyo）。
范围：只做「OpenAI 官方对打票/共享票据类技术实际做了什么」的客观汇总；不继承其它角的结论，引用旧笔记一律标注二手。
方法：① git/changelog（第一方仓库）；② 本机 `thread_history_1.sqlite`（只读副本，未贴任何明文 token/cookie/turn-state）；③ web_search/web_fetch（不可信数据，逐条标 URL+日期+可信度）。
结论先行：**官方没有公开宣布「因打票封号」的案例**；官方的执法动作是「口径 + 风控标记」（2026-08-21 Tibo 点名 sub2api「not supported / 被反欺诈系统标记」）。社区「打票封号」是传闻、且互相矛盾；本机数据**无任何封号/身份异常痕迹**。

---

## 1. 本机 git / changelog 时间线（第一方，可信度：高）

版本→日期（git tag 创建日期）：

| 版本 | 日期 | 对「打票」的动作 |
|---|---|---|
| v0.2.8 | 2026-09-19 | **新增打票**：自动打票、保存/刷新、请求注入有效票；网关总开关 + 独立打票代理；**全局默认「禁止无票请求」**。 |
| v1.1.0 | 2026-09-21 | 打票状态列/详情、请求流水、打票代理池（「292 打票代理池」）；兼容 Team 332 长度票；429 内接收有效票。 |
| v1.1.1 | 2026-09-21 | 修复 Business Premium 332 长度票被误判无效。 |
| v1.1.2 | 2026-09-22 | 票 TTL **默认 200s**、过期复用（默认 600s）、打票时抓取 Cookie（`__oailb`）并复用；采集后 30–60s 随机间隔下一轮。 |
| v1.1.3 | 2026-09-23 | **恢复按 GPT 模型打票 + 指纹校验**；代理池复用、30 分钟主动刷新；新增降智检测；仅 GPT-5.6+ 参与。 |
| v1.1.4 | 2026-09-24 | **全局打票默认关闭**（升级时一次性强制关闭）；未配无票策略时默认允许无票；修复 WS 复用旧连接误判失效。 |
| v1.1.5 | 2026-09-25 | 新增 BPS 平台（Excel/BPS 旁路，账号界面带「封禁风险警告」）。 |
| v1.1.11 | 2026-10-13(commit)/10-17(tag) | **打票复活为「全局共享票据」**；`2811d632b` require both routing cookies；`0a18d7c82` scope ticket cookies；`591f80e03`(10-15) 票出口 IP 池。 |
| v1.1.12 | 2026-10-22 | 服务档位回声 + Fast WS 桥接（与打票无关）。 |

关键结论：
- **默认关闭点 = 1.1.4（2026-09-24）**。changelog 原文：「升级到 1.1.4 时，会一次性关闭全局打票开关，包括此前已经开启的实例」。
- **为什么默认关闭**：第一方 changelog 只说「需要时手动开启」；作者本人（macos.do，见 §3-C1）给出的理由是「打票 TTL 太短、只能换几分钟、没意义」「准备把 292 功能删除回退掉」；另一技术诱因是 09-22 票长 292/332→780（§3-B），旧长度校验（292/312/332/356）集体失效。
- **回退后的再反转**：1.1.11 不是「保持关闭」，而是改成**全局共享一张票**，比 0.2.8 的单账号打票更激进；且需要 require both routing cookies + cookie 隔离 + 出口 IP 池来加固——反过来说明作者自己也知道共享票有跨账号/跨回合污染风险。
- **上游 sub2api 也有一次回退**：v0.2.6（2026-09-17）加过「票据采集+注入（默认关闭）」，随后 0.2.7/0.2.8 起 release notes 不再出现票据能力（§3-B）。「被迫回退」是 sub4api 作者的措辞（见 §3-C2），未见官方说明。

---

## 2. 本机历史错误复核（`thread_history_1.sqlite`，第一方，可信度：高）

只读副本 `scratch/sqlite-copy-client/thread_history_1.sqlite`（2026-10-23 01:06 快照，1.02GB）。表 `thread_turns`（3614 行）有 `error_json`；全表 error_json 非空仅 **86 条**。

### 2.1 error_json 分类（CN 时间，逐日签名）

- **2026-08-28→09-11：自建网关 502/503/407/404/401**
  - 502 Bad Gateway（08-28 ×3、09-01、09-03）— url `http://<自建网关>:8900/v1/responses`。
  - 503 Service Unavailable（08-31 ×2、09-08 ×2、09-11）。
  - **407 Proxy Authentication Required（09-02）** — 代理认证失败，属自建网关出口代理问题。
  - **404（09-05）** — `Model "gpt-6-astra" is not supported by any configured account in this group`，属**分组无该模型账号**的配置问题，不是封号。
  - **401 invalid_api_key（09-06）** — 但 url 是 `https://api.openai.com/v1/responses`（cf-ray NRT），是**直连 OpenAI 的 API Key 失效**，不是自建网关的拒绝。
- **2026-09-10→09-17：「Selected model is at capacity」潮**（09-10 ×5、09-14 ×5、09-15 ×3、**09-17 ×25**），另 09-10 一条 "Our servers are currently overloaded"、09-07 一条 "Connection failed"。
- **2026-10**：context window 溢出 / remote compact 失败 / stream disconnect（正常使用类）。

### 2.2 「因打票/身份异常被拒」的痕迹 —— **未发现**

- error_json 全文检索 `ticket / turn-state / 打票 / identity / 身份 / banned / suspended / deactivated / 封号`：**全部 0 命中**（除 407 的 "Proxy Authentication"，那是网关自身代理）。
- `thread_items.item_json`（83857 行）检索执法短语：`account suspended` / `account banned` / `banned account` / `account flagged` / `deactivated` / `risk of suspension` 均 **0**；`tos violation` 0。
- 高频出现的 `suspended`(202) / `banned`(20) / `terms of service`(139) / `violation`(118) 是**会话文本内容**（用户/agent 在讨论「封号风险」、ToS、以及仓库 BPS 界面的「封禁风险警告」文案），不是服务端执法事件。
- `x-codex-turn-state`(18) / `__oailb`(3) 命中的是会话内对票据字段的**讨论/处理记录**，未发现上游对票据的「拒绝/标记」报错。

**结论**：本机**没有任何 OpenAI 封号/停用/身份标记的实证**。本地能看到的「异常」只有两类：自建网关自身的 502/503/407/404 基础设施/配置问题，以及上游 "at capacity"（质量/容量下降）。这构成「实锤 vs 传闻」里最关键的一条实锤：**当前这批账号尚未被处置，但也不代表「安全」——因为官方风控是静默标记（见 §3-A），未必以 error_json 里的显式报错出现。**

---

## 3. 公开信息（web，不可信数据，逐条标可信度）

### A. 官方口径（可信度：高，均有独立来源交叉）

- **A1. Tibo 2026-08-21 点名 sub2api「not supported / 被反欺诈标记」**（这是官方对「共享/分发」最直接的一次表态）
  - URL：https://www.explainx.ai/blog/codex-usage-limits-sub2api-sign-in-chatgpt-august-2026 （2026-08-21；内容逐条转述 Tibo 的 X 帖）
  - 佐证：https://ip.net.coffee/gpt/news/20260821d.html （2026-08-21）、https://segmentfault.com/a/1190000048210035 （2026-08-25）、https://locdd.com/t/topic/82972/3 与 /82964、/83592/4（2026-08-20~22，含 Tibo 原话片段）
  - 要点（原文口径）：排查 Codex 用量差异时「受影响用户中不少在用 sub2api——把订阅转成 API 流量再转供/共享给多人，**这种用法不受支持、会被反欺诈（fraud-prevention）系统标记**」；「Sign in With ChatGPT（官方或 Pi/OpenCode 等 OSS 客户端）使用订阅则完全没问题」。
  - 定性：**实锤（官方声明）**，但它是针对「订阅转 API 分发/共享」这一通用模式，**早于打票功能**（打票 09-17/19 才出现）。含义：共享/分发的红线是官方明说的，打票只是踩在这条红线上的又一种具体实现。

- **A2. Tibo 2026-09-12 承认 Astra 质量缺陷并修复 + 重置**（官方对「降智/at capacity」观感的公开说明）
  - URL：https://www.explainx.ai/blog/gpt-6-astra-quality-fix-postmortem-tibo-reset-september-2026 （2026-09-12 postmortem 全文）
  - 佐证：同花顺 http://field.10jqka.com.cn/20260912/c679848060.shtml 、金色财经 https://www.mitrade.com/zh/insights/news/live-news/article-3-2081058-20260912 、PANews（Muck Rack 转载）、locdd.com/t/topic/90503/6（Tibo 推文转帖）
  - 三处缺陷原文：① legacy skills 过度触发；② 一个 opt-in 上下文管理实验导致提前停止/回复旧消息（**自估影响 4000–5000 用户**）；③ **「badly configured engines that resulted in a measured quality degradation for a long tail of traffic」→ 已移除**；「reset landing by midnight today」。
  - 定性：**实锤（官方声明）**。注意：这是「模型自身质量缺陷」的官方承认，**与打票/共享无关**——即 09 月的「降智/at capacity」观感里，有相当一部分来自官方自己的引擎配置问题，而非账号被标记。这条对「把降智归因于打票」是一个重要的反向证据。

### B. 打票机制的上游/社区「工程实证」（可信度：中高，可读代码/仓库）

- **B1. sub2api v0.2.6（2026-09-17）加过打票（默认关闭），随后撤回**
  - URL：https://newreleases.io/project/github/Wei-Shaw/sub2api/release/v0.2.6 （2026-09-17）；佐证 https://locdd.com/t/topic/92540
  - 0.2.7（09-18）起 release notes 不再出现票据能力。→「加过又撤」**实锤**；「是否被迫」**传闻**（见 C2）。
- **B2. 票长 292/332 → 780（2026-09-22）** —— 打票失败的技术根因候选
  - URL：https://github.com/Wei-Shaw/sub2api/issues/8104 （2026-10-14：「单条 x-codex-turn-state 目前约 780 字符（2026-09-22 起官方长度从 292/332 增加到 780）」）；佐证 https://linux.do/t/topic/2939698/8 （2026-09-22：「我昨天降智了，今天恢复后变成 780」）
  - 另有 https://pi.dev/packages/@mzwing/pi-codex-enhancer 称「non-degraded 的票恰好 292 字符」→ 292 只是社区启发式，非官方质量指标。
- **B3. 官方源码契约：`x-codex-turn-state` 是 per-turn 粘性路由令牌，跨回合必须清空、与铸造账号绑定**
  - URL：https://github.com/openai/codex/pull/9332 （2026-01-16，OnceLock first-write-wins、跨回合不得发送）；第三方核对 https://github.com/YueDongSun/codex_state_monitor （2026-09-20）
  - 含义：**跨账号/跨回合「共享一张票」直接违反官方契约**，属被上报过的限流绕过行为。这是「打票有封号风险」最硬的技术依据。

### C. 社区封号/警告报告与传闻（可信度：低~中，一律视为未验证）

- **C1. 「打票封号」报告（传闻，互相矛盾）**
  - URL：https://linux.do/t/topic/2947373/41 （2026-09-24）：「为了打 95/88 之类的，打的时间长了，早上起来**封号了**」「我是**打票封了一个**，昨天用了一天 excel 还没死号」「都是打票封的」。
  - 反例：https://linux.do/t/topic/2926100/26 （2026-09-20）：「十几个号一起打票，目前**号全部健在**…后面会不会追封不知道」。
  - 定性：**传闻**。单条无截图/无响应 ID，且存在「有人封号有人健在」的矛盾；「excel 反代」也被同一帖提为可能诱因，因果不清。
- **C2. 「sub2api 0.2.6 打票被迫回退」**
  - URL：https://macos.do/t/topic/81 （sub4api 作者公告，2026-09-19~25）原文：「sub2api 官方曾在 0.2.6 版本合入过自动接管 ticket 的功能，可是**因为某种原因被迫回退删除了**」。
  - 定性：**半实锤**（「回退」属实见 B1；「被迫」是作者措辞，未给官方依据，属传闻）。
- **C3. 「Tibo 点名说不支持」—— 已证实为 A1，但常被误传**
  - 社区帖 locdd 82972/82964/83433/83592（2026-08-20~23）+ 知乎回答 https://www.zhihu.com/question/2074240113979175686/answer/2074245890920006338
  - 关键厘清：Tibo 点名的是「sub2api 这种订阅转 API 分发的模式会触发欺诈预防标记」，**不是点名「打票」**（打票当时还不存在）；且原文同时强调「Sign in With ChatGPT 自用没问题」。所以「被点名不支持打票」是**把 A1 与打票嫁接的误传**。
- **C4. sub4api 作者本人对「打票已死」的自述**
  - URL：https://macos.do/t/topic/138 （「科研成果」帖，2026-09-23）、https://macos.do/t/topic/114 （2026-09-23，标题即「准备把 292 功能删除回退掉」）、https://macos.do/t/topic/81/100 （2026-09-25，作者称下版默认关闭打票、仅保留时区替换）。
  - 定性：第一方自述（对「作者意图/时间线」高；对「机理」中）。可作为「作者自己都放弃了打票」的证据。

---

## 4. 时间线结论（串成一条线）

1. **2026-08-21**：Tibo 在 X 点名 sub2api「订阅转 API 分发/共享 = not supported + 被反欺诈系统标记」（A1）。→ 官方对「共享/分发」的红线就此明确。
2. **2026-09-12**：Tibo 公布 Astra 三处质量缺陷并修复+重置，其中「badly configured engines → measured quality degradation for long tail」被移除（A2）。→ 官方承认「降智/容量」观感有自身引擎问题，与打票无关。
3. **2026-09-17/18**：本机 "at capacity" 潮峰值（09-17 单日 25 条）；社区/官方 issue 同期爆发；官方客户端转 WS transport（A2 佐证、旧笔记 §D）。
4. **2026-09-17→09-19**：sub2api 0.2.6 加打票（默认关闭）→ 很快撤回；sub4api 0.2.8 独立实现打票（默认**禁止**无票请求）。
5. **2026-09-22**：票长 292/332 → **780**（B2），旧长度校验集体失效。
6. **2026-09-24**：sub4api **1.1.4 全局打票默认关闭**（作者：打票没意义/只能换几分钟）；社区开始出现「打票拉闸」「研究 780」与「打票封号」传闻（C1）。
7. **2026-10-13→10-15**：sub4api **1.1.11 反转，改「全局共享票据」**并加固（require both routing cookies / scope cookies / 出口 IP 池）。
8. **本机结论（贯穿全程）**：`thread_history_1.sqlite` **无任何封号/身份异常**；仅自建网关 502/503/407/404 + 上游 "at capacity"。

一句话：**官方的「执法」到目前为止是「口径 + 静默风控标记」，不是公开封号；而 sub4api 自己在 1.1.4 关掉打票后又于 1.1.11 把它升级成更激进的「全局共享票」，正好撞在官方 08-21 明示的「共享/分发 = fraud-flag」红线上。**

---

## 5. 风险评级与最可能触发处置的 3 个动作

**继续做「打票/共享」的封号风险评级：高（High）。**

依据（按证据强度排序）：
1. 官方已明说「订阅转 API 再共享/分发 → not supported + 反欺诈系统标记」（A1，实锤）。「全局共享票」= 直接命中。
2. 官方源码契约「turn-state per-turn / 不跨回合 / 不跨账号」（B3，实锤）；跨账号共享票 = 契约违反 + 已被社区作为限流绕过上报。
3. 社区有「打票封号」报告（C1，传闻且矛盾，但方向与 1/2 一致）。
4. 本机当前无封号（§2）——只能说明「尚未处置」，不能反证「安全」（官方风控是静默标记，未必显式报错）。

**最可能触发处置的 3 个动作**（按风险从高到低）：
1. **「全局共享一张票」跨账号复用**（1.1.11 模式）：一张票被多个账号/多回合注入，同时违反官方共享红线（A1）与 turn-state 契约（B3），是 fraud 分类器最易命中的模式。
2. **同一账号高频换 IP 打票 / 并发打票**：不停换出口 IP 打 292，是社区唯一明确关联「封号」的具体动作（C1），也是典型 account-farm / 限流绕过形态。
3. **把打票/共享票注入到面向多用户的生产分发**：即 Tibo 08-21 点名的「subscription → API → resell to many users」，这是官方明说的 fraud-flag 触发点。

---

## 6. 从角 B 出发的方案建议（1 最推荐 + 2 次选）

**最推荐：停止「注入/共享票」，改为「协议正确回传 + 账号质量运维」。**
- 做法：`x-codex-turn-state` 只在**同账号、同回合**内回传，跨账号剥离、不主动注入他人票（三处捕获：SSE `response.metadata.headers` / HTTP 头 / WS 握手，按 provider+账号 分桶）；降智判据改用「请求模型 == 流内 served model」+ 读 `x-codex-safety-buffering-*`，不再以票长判定。参考现成实现：chord commit `5c2c5e4`、上游 sub2api PR #5668。
- 可行性：高（有可抄代码 + 官方契约明确）；风险：低（符合契约，不踩共享红线）；成本：中（改捕获点/分桶/剥离 + 探测判据）。

**次选 1：把打票降级为「单账号 per-turn 回传 + 默认关闭 + 票长自适应」。**
- 做法：即使保留打票能力，也回退到 1.1.4 的「默认关闭」；票长校验改为 780/未来形态自适应（Fernet 信封 + 块数 + 时间窗，长度只作 soft 提示，不作质量/失效判据）；修掉响应头 `Header().Add` 重复写导致的 502（sub2api #8104 同款坑）。
- 可行性：高；风险：中高（仍属「打票」但降到不跨账号）；成本：低（改校验 + 开关默认值）。

**次选 2：转主路径到「时区治理 + BPS/Excel 旁路 + 服务档位实测」。**
- 做法：保持时区替换（默认非中国大陆，近零成本）；把「不降智」诉求迁到 BPS 平台（sub4api 1.1.5 已内置，但默认关闭 + 明确封禁风险提示）；Fast 提速走官方 `service_tier` 且**不靠回显判断生效**（A2 口径 + 社区结论反转过，必须自测）。
- 可行性：高（BPS 已内置）；风险：中（BPS 非官方协议、Token 手动、无 WS；时区证据等级弱但无 ToS 风险）；成本：中。

---

## 附：引用清单（URL + 日期 + 可信度）

- 官方（高）：https://www.explainx.ai/blog/codex-usage-limits-sub2api-sign-in-chatgpt-august-2026 （2026-08-21）｜ https://www.explainx.ai/blog/gpt-6-astra-quality-fix-postmortem-tibo-reset-september-2026 （2026-09-12）｜ http://field.10jqka.com.cn/20260912/c679848060.shtml （2026-09-12）｜ https://www.mitrade.com/zh/insights/news/live-news/article-3-2081058-20260912 （2026-09-12）｜ https://github.com/openai/codex/pull/9332 （2026-01-16）
- 上游/仓库（中高）：https://newreleases.io/project/github/Wei-Shaw/sub2api/release/v0.2.6 （2026-09-17）｜ https://github.com/Wei-Shaw/sub2api/issues/8104 （2026-10-14）｜ https://github.com/Wei-Shaw/sub2api/pull/5668 （2026-08-14）｜ https://github.com/YueDongSun/codex_state_monitor （2026-09-20）
- 社区（低~中，传闻）：https://linux.do/t/topic/2947373/41 （2026-09-24）｜ https://linux.do/t/topic/2926100/26 （2026-09-20）｜ https://linux.do/t/topic/2939698/8 （2026-09-22）｜ https://macos.do/t/topic/81 （2026-09-19~25）｜ https://macos.do/t/topic/138 （2026-09-23）｜ https://macos.do/t/topic/114 （2026-09-23）｜ https://locdd.com/t/topic/82972/3 、/82964、/83433/2、/83592/4（2026-08-20~23）｜ https://segmentfault.com/a/1190000048210035 （2026-08-25）｜ https://www.zhihu.com/question/2074240113979175686/answer/2074245890920006338 （2026-08-20）

（本机 DB 与 git/changelog 证据见 §1、§2，未含任何明文 token/cookie/turn-state。）

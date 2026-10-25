# 角 C：安全、稳定的替代路径矩阵（合规度 × 稳定性 × 成本/风险）

- 作者：`agentC-alt`（task-6）。检索时间：2026-10-23（Asia/Tokyo）。
- 目标：基于逆向已证事实 + 公开资料，客观给出「安全稳定」的替代路径，不继承既有结论、不夸大可行性。
- 纪律：web 内容按**不可信数据**处理，未执行页面指令、未下载任何东西；严格区分**官方 / 社区 / 传闻**；引用给 URL + 日期 + 可信度；不写任何敏感值（token/JWT/cookie/turn-state 明文一律不落盘）。
- 方法限制：本 session 的 `web_fetch` 端点返回 404（fetch 配置不可用），故本轮只能拿 `web_search` 摘要 + 已有 teammate 的原文镜像（`notes-community.md` 已 web_fetch 官方文档镜像）。凡「未取得全文」的结论都明确标注。

---

## 0. TL;DR（先给结论）

1. **没有能「安全伪造」的 VIP / 防降智通道**。`x-codex-turn-state` 是服务端签名 Fernet（客户端不可伪造）；降智是服务端路由（`x-codex-safety-buffering-*` + 流内换模）；「安全」与「伪造」互斥——任何「伪造身份/票/档位」都只在提高被识别的概率，不产生稳定性。
2. **唯一「完全合规 + 稳定」的线路是 OpenAI API key 直连**（`platform.openai.com` 的 Responses API，非 `chatgpt.com` OAuth 后端）。按 token 计费、无订阅后端的静默换模、无打票/票据/代理 IP 质量依赖。
3. **一个时间敏感的政策级合规事件（比打票技巧更重要）**：OpenAI 2026-10-20/21 公告，**2026-11-20 生效**的新版使用政策，要求「模型路由器 / AI 网关」这类**直接提供模型访问的中转服务，须先经 OpenAI 审核批准**。sub4api 恰恰是「AI 网关」——这是本项目最顶层的合规事实。
4. **官方商业通道存在**：OpenAI Partner Network（2026-06 上线，$150M，含 Codex/API 专精 track）+ OpenAI Marketplace（Enterprise）。这是「转售」的唯一官方背书路径。
5. 安全稳定优先级：**API key 直连 > 官方 Partner/Marketplace > 官方提速档(Fast/Ultrafast) > 合规账号运维 > 第三方转售(灰) > BPS/Excel(灰)**。

---

## 1. 官方合规主路径：Responses API + API key 直连（topic 1）

### 1.1 是否同样存在「降智」

- **结论：不存在与 ChatGPT OAuth 后端同款的静默换模**（高置信，官方文档 + 社区交叉印证；但「API 端零实测」为未知）。
- 依据：
  - 「降智」的机制性来源是 **ChatGPT/Codex OAuth 订阅后端**：CLIProxyAPI issue #5941 明确「`gpt-6-astra` → `gpt-5.6-luna` 的 downgrade 完全由 OpenAI 上游后端强制（server-side load management 或账号 Codex credits 为 0）」。URL: https://github.com/router-for-me/CLIProxyAPI/issues/5941 （2026-09-17，第三方网关 issue，中高）。
  - API key 是**按 token 计费**产品：官方 Codex speed 文档「API key 登录不吃 ChatGPT 倍率，按 API token 价计费」（见 `notes-community.md` A1 的原文镜像）。付费即服务所点模型，OpenAI 无「给你换便宜模型」的商业动机；容量不足表现为 429/限流，不是换模。
  - 反向证据链：`x-codex-safety-buffering-*` 与 `x-codex-turn-state`、`chatgpt-account-id`、`__oailb` 都属于 `chatgpt.com` 订阅栈；API key 栈（`api.openai.com/v1/responses`）不在该路由下。
- **仍需注意的非降智风险**：TPM/RPM 限流（usage tier）、模型退役（GPT-5.5 已于 2026-10-14 从 Codex+ChatGPT 退役）、latency。

### 1.2 计费与可用性（DevDay 2026 口径）

| 模型 | API 标准价（$/1M） | 备注 |
|---|---|---|
| gpt-6.1-sol | 输入 $2 / 缓存 $0.10 / 输出 $10 | ≈ Astra 1/5 价；API + ChatGPT 各档 |
| gpt-6-astra | 输入 $10 / 输出 $50 | 高价旗舰 |
| gpt-6-sol / gpt-6-luna | 有 API | 可用 |
| GPT-5.5 | — | 2026-10-14 退役 |

- 可用性 tier：2026-10-06 起 **5 档 usage tier 换成 3 档（Free Build / Launch / Grow）**；Astra Ultrafast TPM 从 500K（tier 1-3）到 5M（tier 5 / Grow）。
- 来源：https://apifox.com/apiskills/openai-devday-2026/ （2026-09-29，中文综述，中高）；https://developers.openai.com/api/docs/models/compare （官方，模型对比含 TPM 档，高，仅摘要）；https://thenextweb.com/news/openai-gpt-6-1-sol-price-astra-devday （2026-09-28，科技媒体，中）。

### 1.3 矩阵评分

| 维度 | 评分 | 说明 |
|---|---|---|
| 合规度 | ★★★★★（最高） | API key 是官方计费产品，无 ToS 灰区 |
| 稳定性 | ★★★★★（最高） | 不依赖订阅路由/票据/代理 IP 质量 |
| 成本 | 中（按量） | 无订阅沉没；astra 单价高（$10/$50） |
| 风险 | 极低 | 仅标准 API 风控（org 级） |

### 1.4 落地要点（对 sub4api）

- 网关现有「OpenAI（API key）平台」应升级为**一等公民**：把 `gpt-6.1-sol / gpt-6-astra / gpt-6-sol / gpt-6-luna` 直接以 API key 转发到 `api.openai.com/v1/responses`，不注入任何 OAuth 侧头（turn-state/attestation/routing-hint/cookie 全部不适用）。
- 这也是「唯一完全合规」的落地点：**合规的稳定版 Codex 体验 = 用户自带 API key（BYOK）或项目代持 API key 按 token 计价转售（需成为 Partner，见 §4）**。

---

## 2. 官方提速档（topic 2）：Fast/priority、Ultrafast 的真实边界

### 2.1 定位（先纠正预期）

- Fast/Ultrafast 是**速度/延迟档，不是防降智**。本机证据（`notes-client-runtime.md`）：10/22 起客户端已在发 `service_tier=priority`，**同期仍 209/347 条响应带 `x-codex-safety-buffering-enabled: true`** → priority 挡不住降智。

### 2.2 计费边界（官方口径，可信度高）

| 档 | 计费 | 适用模型 | entitlement 条件 |
|---|---|---|---|
| Fast（线上值 `priority`，配置值 `fast`） | Standard 的 **2×**（订阅内额度 **2.5×**）；GPT-5.6 Sol 短上下文 $8/$40 | GPT-5.6 / GPT-6 Sol / Astra / Luna | ChatGPT 登录（desktop/CLI/IDE）；API key 也可 |
| Ultrafast | 订阅内额度 **8×**、credits/PAYG **6×**；Astra Ultrafast $60/$300 | 仅 gpt-6-astra / gpt-6.1-sol | **Pro $500 或符合条件的 Enterprise/Edu**；Enterprise 默认关 |

- `priority` 与 `fast` 行为相同、回显规范化为 `priority`；Fast 不支持 fine-tuned/embeddings。
- 「Batch 与 Flex 是 Standard 的 50%」→ 想省钱走 flex/batch，不是 fast。
- 来源：https://developers.openai.com/api/docs/guides/fast-mode 、/ultrafast-mode （官方，仅摘要 + `notes-community.md` A2/A3 的镜像全文）；https://costbench.com/software/llm-api-providers/openai-api/calculator/ （2026-10-09，中）；https://openrouter.ai/docs/guides/features/service-tiers （2026-10-15，第三方网关文档，中高）。

### 2.3 关键工程约束（不照抄旧结论）

- **传输层结论有时间反转**：2026-07 实测「priority 只在 Responses **WebSocket** 生效（1.55×），HTTP/SSE 0.97× 无效」；2026-10 该 PR 作者关闭并称「当前上游已在 HTTP/SSE 尊重 priority」。→ **必须本环境 A/B 自测**（`notes-community.md` B5）。
- Ultrafast 仅在 Astra/6.1-sol 且账号真有 entitlement 时放行；目录未广告就回落 priority（否则上游 400/静默降级）。
- **不要用响应回显 `service_tier` 判生效**（OAuth 后端常回 `default`，`notes-fast-path.md` §4 已有）。

### 2.4 矩阵评分

| 维度 | 评分 | 说明 |
|---|---|---|
| 合规度 | ★★★★☆ | 官方功能 |
| 稳定性 | ★★★☆☆ | 需 entitlement + 实测传输；非质量保证 |
| 成本 | 2.5×–8× | 需提前告知用户 |
| 风险 | 低 | 官方计费档；无 ToS 灰区 |

---

## 3. 账号/路由运维（topic 3，合规、不伪造）

### 3.1 是什么

- 用**真实未降智账号** + 「流内实际模型」检测（`response.created.response.model` == 请求模型；读 `x-codex-safety-buffering-*`）做多号轮换/冷却；命中降智即把该号从门控模型摘出，而不是给它打票。
- 养号：时区（非中国大陆，`Asia/Singapore`）、稳定住宅出口（每号固定 IP）、低频。
- 这是社区「最稳」做法：ranxi2001/sub2api（「检测以实际回答/判题为准，不以票据形态判断质量」）、codex_state_monitor（「不注入/不重放/不采集 turn-state」）。
- 来源：https://github.com/ranxi2001/sub2api （2026-10-22 HEAD，中高）；https://github.com/YueDongSun/codex_state_monitor （2026-09-20，源码核对，高）；macos.do 作者帖 https://macos.do/t/topic/138 （社区，低-中）。

### 3.2 合规边界（关键区分）

- **单号自用 + 观测运维**：合规度高（不伪造、不绕过、不共享）。
- **批量注册/买号/共享一号**：灰区，违反 ToS，有封号面（社区有「打票封号」报告，`notes-community.md` C6）。
- 降智账号**无主动解除**：社区口径「静置一周/几天等官方解除」；「打票只能换 200s/240s」已证明不是稳定方案。

### 3.3 矩阵评分

| 维度 | 评分 | 说明 |
|---|---|---|
| 合规度 | ★★★☆☆（自用）/ ★★☆☆☆（批量） | 取决于账号来源 |
| 稳定性 | ★★★☆☆ | 依赖上游降智波动，被动 |
| 成本 | 中 | 多订阅 + 住宅代理 |
| 风险 | 中 | 批量=封号；单号=低 |

---

## 4. 商业/转售渠道（topic 4）

### 4.1 官方（唯一官方背书）

- **OpenAI Partner Network**（2026-06 上线，$150M，Select/Advanced/Elite 三档，含 **Codex / API / cybersecurity / AI transformation** 专精 track；目标 2026 年底认证 30 万顾问）。这是「成为 OpenAI 分销/方案伙伴」的官方入口。
  - URL: https://openai.com/index/introducing-openai-partner-network/ （官方，高）；https://dataconomy.com/2026/06/15/openai-launches-150-million-partner-network/ （2026-06，媒体，中）。
- **OpenAI Marketplace（Enterprise）**：符合条件的商业客户可用约定额度购买构建在 OpenAI 模型上的伙伴产品。
  - URL: https://help.openai.com/mk-mk/articles/20001553-openai-marketplace-for-enterprise-customers （官方帮助中心，高，镜像）。

### 4.2 第三方转售（无官方背书）

| 渠道 | 是什么 | 模式 | 风险 |
|---|---|---|---|
| codex-everywhere | Codex 兼容 API 转售（`codex-everywhere.com` / `codex-easy.ai`），$20 试用，接 Codex CLI/App，宣称 GPT-6 Astra | 订阅/按量转售 | 第三方，非官方；11-20 政策后转售更需过审；断供/风控连带 |
| LanoX | 多模型聚合 API（GPT/Claude/Gemini/Grok/DeepSeek/Qwen/GLM），OpenAI 兼容，按量 USD，宣称「official provider access, no downgrades」 | 聚合转售 | 第三方；「官方接入」为其自述，未见 OpenAI 背书 |
| ProxyLane | **住宅代理**（195 国，95%+ Scamalytics 低风险） | IP 供应，非模型转售 | 本身合规；用途决定合规（养号=灰） |

- 来源：https://docs.codex-everywhere.com/quickstart/ （2026-10-04，官方站自述，中低）；https://lanox.ai/zh （2026-10-15，官方站自述，中低）；README `assets/partners/proxylane.png`（项目赞助方，非 OpenAI 背书）。

### 4.3 关键判断

- **README 的「赞助/合作伙伴」是项目赞助，不是 OpenAI 背书**——不能据此宣称「官方支持」。
- 第三方转售的「稳定」是**伪稳定**：其上游要么是囤 API key、要么是 ChatGPT OAuth 池、要么是 Partner 授权——前两者在 11-20 政策 + 风控下都有断供风险。

### 4.4 矩阵评分

| 维度 | 评分（官方 Partner） | 评分（第三方转售） |
|---|---|---|
| 合规度 | ★★★★★ | ★★☆☆☆（灰） |
| 稳定性 | ★★★★☆ | ★★☆☆☆ |
| 成本 | 高（企业级门槛） | 低门槛但溢价 |
| 风险 | 低 | 断供/跑路/风控连带 |

---

## 5. 灰区/旁路：BPS/Excel（topic 5）

### 5.1 是什么

- `POST https://bps.openai.com/basispoints/api/responses`，带 `authorization: Bearer <access_token>`（**普通 ChatGPT JWT**）+ `chatgpt-account-id` + `x-openai-account-id` + `x-basispoints-auth-mode: chatgpt`。
- 社区称「满血 / 不降智」；sub4api 1.1.5 已内置 BPS 平台。
- 来源：https://macos.do/t/topic/138/17 （2026-09-23，社区，低-中）；https://linux.do/t/topic/2966334/5 （2026-09-28，社区，低）；`notes-community.md` C1/C2。

### 5.2 为何「社区称满血」但不稳定（风险分级）

| 风险点 | 说明 |
|---|---|
| 非官方公开协议 | `bps.openai.com` 是 ChatGPT Excel/Basis Points 内部端点，非公开 API；用 ChatGPT JWT 访问 = ToS 灰区 |
| 功能阉割 | **无 WS、无 max effort、~600k 上下文强制压缩、工具需协议转换（坑多）** |
| Token 生命周期 | BPS Token 手动续期，无自动续期 → 断供 |
| 封堵风险 | 上游随时可封；一旦封，比打票更被动（无社区「换 IP」式的替代） |

### 5.3 矩阵评分

| 维度 | 评分 | 说明 |
|---|---|---|
| 合规度 | ★☆☆☆☆ | 用 ChatGPT JWT 走内部端点 |
| 稳定性 | ★★☆☆☆ | token 手动续 + 无 WS + 压缩 |
| 成本 | 低 | 复用 ChatGPT 订阅 |
| 风险 | 中高 | 比 API key 直连高一个量级 |

---

## 6. 结论（topic 6）：有没有能「安全伪造」的 VIP 通道？

**没有。** 论证：

1. **票不可伪造**：`x-codex-turn-state` 是服务端密钥加密 + HMAC 签名的 Fernet（585B=33 块，含铸造时间戳），客户端只能原样回带，且契约是 per-turn/per-account（跨回合清空、跨账号剥离）。「伪造一张 VIP 票」在密码学上不可行（`notes-2-identity` §3）。
2. **降智不可伪造解除**：降智是服务端路由（safety-buffering + 流内换模），由账号分类（Luna classification）+ 容量管理决定；客户端能做的只是「更像官方客户端」（更好的身份链/指纹），这不产生稳定性，只提高「被当作真客户端」的概率。
3. **「安全」与「伪造」互斥**：越接近「伪造官方客户端/伪造票据」，越接近「模拟绕过」，越触发 `x-oai-attestation`/设备指纹/风控码，封号面越大。安全稳定的唯一方向是**走官方计费/官方伙伴通道**，或**合规观测 + 账号运维**。
4. **政策正在收口**：2026-11-20 起，模型路由器/AI 网关需 OpenAI 过审——这直接点名 sub4api 这类网关。任何「伪造/绕过」路线都在这条政策下变得更不「安全」。

### 6.1 安全稳定替代优先级（从角 C 出发）

| 优先级 | 方案 | 合规度 | 稳定性 | 成本 | 风险 |
|---|---|---|---|---|---|
| P0 | **API key 直连**（Responses API，BYOK 或代持按 token 计价） | ★★★★★ | ★★★★★ | 中 | 极低 |
| P1 | **官方 Partner Network / Marketplace**（转售的唯一合规形态） | ★★★★★ | ★★★★☆ | 高 | 低 |
| P2 | **官方提速档 Fast/Ultrafast**（体验，非防降智） | ★★★★☆ | ★★★☆☆ | 2.5–8× | 低 |
| P3 | **合规账号运维**（真实号 + served-model 检测 + 冷却/养号） | ★★★☆☆ | ★★★☆☆ | 中 | 中 |
| P4 | **第三方转售**（codex-everywhere / LanoX） | ★★☆☆☆ | ★★☆☆☆ | 低门槛溢价 | 断供/风控连带 |
| P5 | **BPS/Excel 旁路** | ★☆☆☆☆ | ★★☆☆☆ | 低 | 中高 |

### 6.2 落地路线图（建议）

1. **立即（本周）**：把 API key 直连跑通并作为「完全合规」主推通道——`api.openai.com/v1/responses` 直连 gpt-6.1-sol/gpt-6-astra，不注入任何 OAuth 头；这同时是对冲 11-20 政策的「合规资产」。
2. **短期（1–2 周）**：评估 OpenAI Partner Network 申请路径（若目标是长期转售/商业运营）；启动 Fast/priority 的 HTTP/SSE vs WS A/B 实测（自测，不照抄旧结论）。
3. **中期**：把「降智检测」判据换成 served-model + safety-buffering 头，账号冷却/换号运维化；打票降级为「可选、默认关、只回传不共享」。
4. **长期**：BPS/第三方转售作为明确标注风险的兜底，默认关闭；养号（时区+稳定出口+低频）作为零成本默认项保留。

---

## 7. 从角 C 出发的「1 最推荐 + 2 次选」

- **最推荐：API key 直连（Responses API）** —— 合规度 ★★★★★ / 稳定性 ★★★★★ / 成本 中（按 token）/ 风险 极低。这是唯一「完全合规 + 稳定」的线路，也是唯一能绕开「降智 + 打票 + 代理 IP」整套脆弱栈的路径；落地即「把 gateway 的 OpenAI(API key) 平台升级为一等公民，BYOK + 按 token 计价」。
- **次选 A：官方 Partner Network / Marketplace 转售** —— 合规度 ★★★★★ / 稳定性 ★★★★☆ / 成本 高（企业级门槛）/ 风险 低。若项目要走「对外提供 Codex/API 访问」的商业形态，这是唯一官方背书的分销通道；与 P0 互补（P0 自用/代持，A 商业化）。
- **次选 B：官方提速档 Fast/Ultrafast + 合规账号运维** —— 合规度 ★★★★☆~★★★☆☆ / 稳定性 ★★★☆☆ / 成本 2.5–8× + 中 / 风险 低-中。这是「保留 ChatGPT OAuth 体验」前提下最稳的组合：Fast/Ultrafast 管速度（并 A/B 自测传输）、served-model 检测管降智运维；两者都**不伪造、可观测**，是灰区之外能长期站的最后一档。

> 一句话收束：**「安全」的终点是官方计费/官方伙伴通道，「稳定」的终点是可观测的账号运维；两者之间没有「伪造出来的 VIP」。**

---

## 8. 引用清单（URL / 日期 / 可信度）

官方（高）：
- https://openai.com/index/broad-access-through-consistent-standards/ （2026-10-20，官方博客；使用政策 11-20 生效，网关需过审）
- https://openai.com/zh-Hans-CN/index/broad-access-through-consistent-standards/ （官方中文版）
- https://openai.com/index/introducing-openai-partner-network/ （官方，Partner Network）
- https://help.openai.com/mk-mk/articles/20001553-openai-marketplace-for-enterprise-customers （官方帮助中心，Marketplace）
- https://developers.openai.com/api/docs/guides/fast-mode 、/ultrafast-mode （官方，仅摘要；全文见 notes-community.md A2/A3 镜像）
- https://developers.openai.com/api/docs/models/compare （官方，模型对比/TPM，仅摘要）
- https://github.com/openai/codex/blob/main/codex-rs/codex-api/src/safety_buffering.rs （官方源码，safety-buffering）

媒体/综述（中）：
- https://apifox.com/apiskills/openai-devday-2026/ （2026-09-29）
- https://thenextweb.com/news/openai-gpt-6-1-sol-price-astra-devday （2026-09-28）
- https://dataconomy.com/2026/06/15/openai-launches-150-million-partner-network/ （2026-06）
- https://mixed-news.com/en/openai-ultrafast-gpt-6-1-sol-eu-data-residency-not-astra/ （2026-10-08）
- https://explainx.ai/blog/openai-usage-policies-november-20-model-routers-ai-gateways-approval-partners-safetyids-2026 （2026-10-21）

社区/第三方（低-中）：
- https://github.com/router-for-me/CLIProxyAPI/issues/5941 （2026-09-17，降智=上游后端强制）
- https://github.com/Wei-Shaw/sub2api/issues/8282 （2026-10-21，网关需过审）
- https://github.com/ranxi2001/sub2api （2026-10-22 HEAD）
- https://github.com/YueDongSun/codex_state_monitor （2026-09-20）
- https://docs.codex-everywhere.com/quickstart/ （2026-10-04）
- https://lanox.ai/zh （2026-10-15）
- https://macos.do/t/topic/138/17 （2026-09-23，BPS）
- https://linux.do/t/topic/2966334/5 （2026-09-28，BPS）
- https://openrouter.ai/docs/guides/features/service-tiers （2026-10-15）
- https://costbench.com/software/llm-api-providers/openai-api/calculator/ （2026-10-09）

前序证据（本仓，供复核）：
- `scratch/notes-community.md`（官方文档镜像 + 社区检索）
- `scratch/notes-2-identity.md`（Fernet 票据语义）
- `scratch/notes-3-routing.md`（降智权威判据 + 档位）
- `scratch/notes-fast-path.md`（forced_service_tier 全链路）
- `scratch/notes-client-runtime.md`（本机 priority 仍带 safety-buffering）

---

## 9. 局限 / 未验证清单

1. `web_fetch` 端点本 session 不可用（404），本轮全部依赖 `web_search` 摘要；关键官方文档全文（fast/ultrafast 指南、Usage Policy 原文）依赖 `notes-community.md` 的镜像，需复核原文。
2. 「API key 直连无静默换模」是**高置信推断**（官方计费模型 + 社区交叉印证），未在本环境对 `api.openai.com` 做实测；不能 100% 排除容量/风控场景下的延迟或限流。
3. 第三方转售（codex-everywhere/LanoX）的「官方接入/不降智」为**其自述**，未见 OpenAI 背书原文；其上游机制未审计。
4. Usage Policy 11-20 的「审核批准」具体流程/门槛，官方只给了方向性表述，未取得全文细则。
5. GPT-6 Astra 是否对**普通 API key**（非 Enterprise/Trusted Access）全量开放：媒体口径有「先 Enterprise Trusted Access、API 陆续开放」的过渡期，普通开发者能否立即直连 Astra 需实测（`accessallgpt.com` 2026-09-03 提及）。

# Codex「打票」——官方应对措施 与 安全稳定替代方案（最终评估）

日期：2026-10-23 JST ｜ 逆向：`codex.exe`（0.163.x，SHA256 E757246B…）
分工：角A 官方反制逆向（agent4-counter）· 角B 执法/封号时间线（subagent）· 角C 安全稳定替代矩阵（subagent）· Lead 汇总联合评估。

---

## 0. TL;DR

1. **官方没有「封打票」的单一开关，而是一整条纵深反制链**：身份自洽 + 边缘粘性 + 服务端加密票据 + 账号分类 + 风控词表。其中**唯一“实锤在跑”的是软性换模**（降智），其余硬反制本机窗口一次都没触发过。
2. **最硬的一条官方表态**：2026-08-21 OpenAI 员工 Tibo 在 X 点名 sub2api——「订阅转 API 再共享/分发 = not supported + 被反欺诈系统标记」。
3. **没有能“安全伪造”的 VIP/防降智通道**：票是服务端签名 Fernet（不可伪造）、降智是服务端路由；「安全」与「伪造」互斥。
4. **唯一「完全合规 + 稳定」的替代 = OpenAI API key 直连**（Responses API）。另有一个**比打票更重要的政策事实**：OpenAI 新版使用政策 **2026-11-20 生效**，模型路由器/AI 网关须先过审。

---

## 1. 官方对「打票/共享」的应对措施（角 A，8 项）

| # | 机制 | 触发条件 | 能否安全绕过 |
|---|---|---|---|
| 1 | **`x-oai-attestation`**（WS 握手专属，设备级 attestation） | WS 续传/回带票时校验 | **否**（值由 app-server `attestation/generate` 现场生成，`device_key_bindings` 设备密钥 + Ed25519/P-256；CLI/exec 无法生成） |
| 2 | **turn-state 反重放**（Fernet：版本+时间戳+IV+密文+HMAC，585B） | 跨 turn/跨账号回放 | **否**（不可伪造；跨账号回放是“真实客户端永不产生的矛盾信号”） |
| 3 | **身份链自洽**（installation_id + session/thread/turn/window + 头体一致，turn-metadata 32 字段） | 每次请求 | **否**（伪造易露馅） |
| 4 | **UA/originator/version 门槛**（version < 0.144.0 直接 404） | 每次请求 | **否** |
| 5 | **降智/换模**（`x-codex-safety-buffering-*` + 流内 `response.created.response.model`） | 模型拥塞/降载 | 本地票据不可覆盖（服务端路由） |
| 6 | **账号分类/额度**（Luna classification + plan_type + window + credits + org spend） | 超限/降档 | **否**（服务端判定，客户端只读） |
| 7 | **边缘反爬**（`__cf_bm` Cloudflare Bot Mgmt + `__oailb`/`__cflb` 粘性） | IP/边缘粘性 + cookie 一致 | **否**（跨 IP 复用露馅；`__oailb` 只在 `/models` 下发） |
| 8 | **风控错误码/文案**（cyber/bio/misalignment/too_many_denials…） | 策略违规/denial 过多 | 本机从未触发（未知其阈值） |

> 关键新发现（角 A）：`x-oai-attestation` 是**设备/安装级 attestation**（app-server 守护进程现场生成，非浏览器级），打票探针（纯 HTTP、无 app-server）在「WS 续传」环节无法原样产出。

## 2. 官方执法时间线（角 B）

- **2026-08-21**：Tibo 在 X 点名 sub2api——「subscription→API→再共享/分发 = not supported + fraud-prevention 标记」，supported path 只有「Sign in With ChatGPT 自用」。
- **2026-09-12**：Tibo postmortem 承认 Astra 三处质量缺陷（含「badly configured engines → measured quality degradation」）并重置——**说明 9 月“降智/at capacity”观感里有一部分是官方自身引擎问题，不是账号被标记**（反向证据）。
- **2026-09-17/18**：降智+“at capacity”大爆发；官方客户端改走 WS transport。
- **2026-09-22**：`x-codex-turn-state` 长度 292/332 → **780**（打票长度校验集体失效）。
- **2026-09-24**：sub4api 1.1.4 全局打票默认关闭；10-13 的 1.1.11 反而反转成“全局共享票据”（更激进、更危险）。
- **本机零封号实锤**：86 条 error_json 全是基建/配置错误 + “at capacity”，无任何 ticket/身份/banned 报错；“打票封号”是社区传闻且互相矛盾。

**风险评级：继续做「共享票」= 高风险**。最易触发处置的 3 个动作：① 全局共享一张票跨账号复用；② 同账号高频换 IP 并发打票；③ 把共享票注入多用户生产分发。

## 3. 安全稳定替代方案（角 C，矩阵）

| 优先级 | 方案 | 合规 | 稳定 | 成本 | 风险 |
|---|---|---|---|---|---|
| **P0** | **API key 直连**（Responses API，非 chatgpt.com） | ★★★★★ | ★★★★★ | 中 | 极低 |
| P1 | **官方 Partner Network / Marketplace**（转售唯一官方背书） | ★★★★★ | ★★★★☆ | 高 | 低 |
| P2 | 官方提速档 Fast/Ultrafast（体验，非防降智） | ★★★★☆ | ★★★☆☆ | 2.5–8× | 低 |
| P3 | 合规账号运维（真实号 + served-model 检测 + 冷却/养号） | ★★★☆☆ | ★★★☆☆ | 中 | 中 |
| P4 | 第三方转售（codex-everywhere / LanoX） | ★★☆☆☆ | ★★☆☆☆ | 溢价 | 断供/风控连带 |
| P5 | BPS/Excel（bps.openai.com，ChatGPT JWT 内部端点） | ★☆☆☆☆ | ★★☆☆☆ | 低 | 中高 |

**P0 为什么是唯一“完全合规+稳定”**：API key 是按 token 计费的官方产品，**不在 chatgpt.com 订阅路由下**，无静默换模、无打票/票据/代理 IP/attestation 依赖。落地 = 把网关的「OpenAI（API key）平台」升级为一等公民（`api.openai.com/v1/responses` 直连 gpt-6.1-sol/gpt-6-astra，不注入任何 OAuth 头）。

**政策收口（最顶层事实）**：OpenAI 2026-10-20 公告、**2026-11-20 生效**新版使用政策——模型路由器/AI 网关须先经 OpenAI 审核批准才能对外提供模型访问（官方 URL: https://openai.com/index/broad-access-through-consistent-standards/ ）。这直接点名 sub4api 这类网关。

## 4. 联合评估的最终排序

1. **API key 直连**（合规★★★★★ / 稳定★★★★★ / 成本中 / 风险极低）——绕开降智+打票+代理IP+attestation 整条栈，同时是对冲 11-20 政策的合规资产。
2. **官方 Partner Network / Marketplace**（转售的唯一合规形态）。
3. **合规账号运维**（served-model 检测 + 冷却/养号，不伪造不共享）——OAuth 体验下最稳组合；turn-state 降级为「只回传、不共享」。

**“实锤不可绕过 / 可观测可绕开 / 未知”三分**：
- 已实锤不可绕过：票据密码学不可伪造 + per-turn 契约 + 共享=fraud-flag（Tibo 08-21）+ 票长 292→780 + 版本 <0.144.0 门槛。
- 可观测/可绕开（但不能伪造解除）：降智（safety-buffering 头 + 流内换模，走 API 直连可绕开）、额度头、边缘 cookie。
- 未知：attestation 是否被强制校验及其算法、是否实锤封号（静默标记）、风控词表触发阈值、11-20 过审细则。

---

## 5. 证据文件

- `scratch/notes-4-counter.md` — 官方反制 8 项清单 + attestation 新发现 + RVA 校正
- `scratch/notes-5-risk.md` — 执法/封号时间线 + 风险评级 + 公开引用（官方/社区/传闻分层）
- `scratch/notes-6-alt.md` — 安全稳定替代矩阵 P0–P5 + 落地路线图 + 政策收口
- `scratch/notes-review-counter.md` — 三路联合评估
- 前序：`notes-1-wire/2-identity/3-routing` + 三份 review + `notes-client-runtime/harvest-flow/community`

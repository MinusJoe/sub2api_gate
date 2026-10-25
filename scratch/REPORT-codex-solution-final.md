# Codex「打票」失效 —— 三 agent 逆向 + 集体评审的最终结论与方案

日期：2026-10-23 JST ｜ 逆向对象：`codex.exe`（0.163.x，336MB，SHA256 E757246B…）
评审参与者：agent1-wire（传输/协议）、agent2-identity（身份链/票据）、agent3-routing（降智/路由），Lead 汇总。

---

## 0. 一句话总纲

> **检测 + 账号运维治「降智」，`service_tier` 管「提速」，turn-state 只做「合规回传」。三件事分开，别再让“打票”背“防降智”的锅。**

三角独立逆向 + 交叉评审，**无实质矛盾**，在同一证据底座收敛，并共同纠正了上一轮的误判：
- 上一轮「拿票必须走 WS 升级」是**错的**。票由服务端在 **HTTP `/responses` 响应头**下发（本机日志坐实：347 条响应里 142 条带票、恒 780 字符），客户端**回带**才只在 WS 握手。
- 统一口径：**获取可走 HTTP，回带仅走 WS。**

---

## 1. 逆向结论（三角共识，每条有证据）

| # | 结论 | 证据/来源 |
|---|---|---|
| 1 | `x-codex-turn-state` 是 **per-turn Fernet 粘性路由令牌**（AES-128-CBC+HMAC，585B=1版本+8时间戳+16IV+528密文(33块)+32HMAC），**不是“VIP/防降智凭证”** | notes-2-identity §3 |
| 2 | 票长 **292/332 → 780**（2026-09-22 起）；当前 HEAD 的 `structurallyValid` 不校验长度（`target_length` 只展示） | notes-2-identity §3.4 |
| 3 | 票下发条件 = **每 turn 首个非增量请求**（首请求 139/141 带票，续请求 0/203） | notes-client-runtime §3.2 |
| 4 | 客户端**只在 WS 握手回带**票（唯一写点 `0x78719e0`，10 个调用者全 WS builder）；HTTP 路径不回带 | notes-1-wire §2.3 |
| 5 | 打票失效根因 = **请求形状/身份**，不是传输：探针硬错误发 `OpenAI-Beta: responses=experimental`（真实客户端 HTTP 路径**根本不发 OpenAI-Beta**，只在 WS 发 `responses_websockets=2026-02-06`）+ 版本 0.146.0 + “TUI 头 / Desktop body”矛盾 + turn-metadata 仅 7 字段 + 无 tools | notes-1-wire §5.3 |
| 6 | `__oailb` **只在 `GET /models` 下发**；`/responses` 上唯一 Set-Cookie 是 `__cf_bm` → “抓任意 Set-Cookie 当票”只会得到 **cookie-only 假票** | notes-1-wire §3 |
| 7 | **降智权威判据 = 请求模型 vs 流内实际模型**（SSE/WS `response.created.response.model`、`openai-model`）+ 提示头 `x-codex-safety-buffering-enabled/-faster-model`；本机 100% 的 astra/6.1-sol 响应带 `faster-model: gpt-6-luna` | notes-3-routing §1 |
| 8 | `service_tier` 是**提速档非质量档**（fast/priority 1.5–2.5×、ultrafast 6–8× 需 entitlement）；HTTP 响应头 0 次回显，用回显判生效不可行 | notes-3-routing §2 |
| 9 | 路由/驻留四信号（routing-hint/responses-lite/residency/code-mode-route）都是**请求头指纹，非 VIP 票**；仿造只能“更像官方客户端” | notes-3-routing §3 |

> REA 说明：`open_binary` 已成功绑定目标（SHA 校验一致），但本机**无原生 provider**（Ghidra 未装/无 JDK、Hopper 仅 macOS、IDA/Kuna 未配置），`binary_overview/search_strings` 返回 `provider_unavailable`。三角均用自写静态工具（全 `.text` xref 精确扫描 + 最小反汇编）+ 本机 sqlite/日志独立复现，结论互为印证；要伪代码级确认需装 Ghidra 12.1.x + JDK 并设 `GHIDRA_INSTALL_DIR`。

---

## 2. 集体评审的最终方案排序

### 方案 1（P0，唯一治「降智」，推荐主方向）
**降智检测判据换成「流内实际模型 + safety-buffering 头」，命中即账号冷却/换号。**
- 网关在 SSE/WS 响应里读 `response.created.response.model`（及 `response.metadata` 的 `openai-model`），与请求模型比对；并记录 `x-codex-safety-buffering-enabled/-faster-model`；不一致即标记账号/模型“疑似降智”并冷却/换号/切通道。
- 改动点：`openai_gateway_response_handling.go` / WS 转发器把响应解析从“只认 `output_text.delta`/`response.completed`”扩展到 `response.created`/`response.metadata`；复用现有「降智检测」骨架换判据。
- 可行性**高**（不伪造、只观测+运维）、风险**低-中**、成本**中**。这是三角公认唯一“作用在质量上”的手段，且不依赖票。

### 方案 2（P1，合规 + 立即止血）
**打票线重构 = HTTP 形状对齐拿票 + per-account/per-turn 回传 + 跨账号剥离，删除全站共享与 292 长度假设。**
- 若仍要“打票”：修探针硬错误——删 `OpenAI-Beta: responses=experimental`、版本对齐 0.163.0、身份头/body 同源、turn-metadata 补全 ~30 字段、加真实 tools；读票位置保持 HTTP 响应头。
- 注入改为「同账号、同回合、WS 握手回带」；跨账号剥离（复用 `relayOpenAICodexTurnState`/`guardOpenAICodexTurnStateEcho` 已有溯源表）。
- 拒绝假票：`extractOpenAICodexResponseCookies` 只认 `__oailb`/`__cflb`，排除 `__cf_bm`；“只有 cookie 无 turn-state”判死。
- 可行性**高**（骨架已有）、风险**低**（合规）、成本**中**。定位是**协议正确性/路由连续性**，**明确不再承诺“防降智”**。

### 方案 3（P2，产品/提速）
**`service_tier=priority/fast` 当“提速”运营，HTTP vs WS 做 A/B 实测。**
- `forced_service_tier=priority` 时，对比 HTTP/SSE 与 upstream-WS 的输出 TPS + `x-codex-primary-used-percent` 消耗速率；**不照抄“只在 WS 生效”的旧结论**（2026-10 该结论已反转，须自测）。
- 给客户端下发 `service_tier=fast` + `[features].fast_mode=true`（否则客户端自己丢 tier）；`ultrafast` 仅 astra/6.1-sol 且 entitlement 实锤时放行。
- 可行性**中**（需实测）、风险**低**（官方功能，计费 2.5–8× 需提前告知）、成本**中**。只管速度，不解除降智。

### 兜底（可选）
- **BPS/Excel 旁路**（1.1.5 已内置）：非官方、无 WS/无 max effort/约 600k 压缩/token 手动续期，中高风险，默认关闭。
- **时区/环境上下文治理**：保留（默认 Asia/Singapore，零成本）。

---

## 3. 你现在该做的（远端，先定位死在哪一关）

```sql
-- 失败原因分布（近 3 天）
SELECT outcome, reason_code, http_status, count(*), max(occurred_at)
FROM codex_ticket_attempts
WHERE occurred_at > now() - interval '3 days'
GROUP BY 1,2,3 ORDER BY 4 DESC;

-- 假票检测（cookie-only）
SELECT account_id, model, occurred_at, http_status, ticket_length
FROM codex_ticket_attempts
WHERE outcome='success'
  AND coalesce(turn_state_present,false)=false AND coalesce(cookie_present,false)=true
ORDER BY occurred_at DESC LIMIT 50;

-- 票长分布（确认 292 还是 780）
SELECT ticket_length, count(*) FROM codex_ticket_attempts
WHERE outcome='success' GROUP BY 1 ORDER BY 2 DESC;
```

判读：
- `http_status` 401/403 → 身份/代理 IP 被拒（方案 2 形状对齐 + 换出口）。
- `missing_credentials`（200 无票无 cookie）→ 形状不被认可（方案 2）。
- `fingerprint_mismatch` → ModelTrace 库过期，先更新 bank，别当降智。
- `proxy_unavailable` → 代理池空（日志静默，易误判）。

---

## 4. 评审分歧记录（已闭合）

- 角 3 指出角 2 次选 B 误把「priority 只在 WS 生效」当定论（实际 community B5 已记录 2026-10 反转、须自测；且引用编号 C6 应为 B5）→ 采纳“有反转、须 A/B 自测”版本。
- 第 2/3 顺位：agent1/agent2 把“打票合规重构”排第 2，agent3 把“service_tier 提速”排第 2 → Lead 综合为：**合规重构（P1 止血）在提速（P2 产品）之前**，因为合规重构同时修掉当前“打不出来/假票”的即时问题。

---

## 5. 证据文件

- `scratch/notes-1-wire.md` — 传输/协议（票在 HTTP 响应头下发、OpenAI-Beta 硬错误、两份“最小正确请求”清单）
- `scratch/notes-2-identity.md` — 身份链 + Fernet 票据结构 + 三重复用错配
- `scratch/notes-3-routing.md` — 降智权威判据 + 档位/路由信号
- `scratch/notes-review-agent1.md` / `-agent2.md` / `-agent3.md` — 三角交叉评审
- `scratch/notes-client-runtime.md`、`notes-harvest-flow.md`、`notes-community.md` — 运行时/探针规格/公开资料（上一轮）

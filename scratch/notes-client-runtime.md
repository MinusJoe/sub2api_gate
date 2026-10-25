# 本机 Codex 客户端运行证据：turn-state / `__oailb` / 降级（task-2，teammate `client-evidence`）

只读取证。原始文件未修改、未删除；所有 DB 先复制到 `scratch/sqlite-copy-client/` 再查。
不打印 token、邮箱、提示词正文；ticket / cookie 只报**是否出现、长度、时间、transport、出现位置**。
所有客户端日志时间戳是 **UTC epoch 秒**；本文时间一律换算为**本机 JST（UTC+9）**并注明。

> 旧结论 `scratch/notes-runtime.md`（2026-10-21，UTC 表述）已被本文件覆盖/更新；本文件给出到 **2026-10-23 01:52 JST** 的证据。

---

## 0. 数据源、副本与保留窗口（关键前提）

| 文件 | 大小 | 用途 | 备注 |
|---|---|---|---|
| `C:\Users\zfx\.codex\logs_2.sqlite` +`-wal` | 91.9 MB + 11.3 MB | tracing 日志，表 `logs(id,ts,ts_nanos,level,target,feedback_log_body,module_path,file,line,thread_id,process_uuid,estimated_bytes)` | **会被滚动裁剪**，当前副本只覆盖 ~33 h |
| `C:\Users\zfx\.codex\thread_history_1.sqlite` | 1.02 GB | `thread_turns / thread_items / thread_realtime_items` | 可复制后正常读取（旧记录说 `mode=ro` 打不开=复制问题） |
| `C:\Users\zfx\.codex\state_5.sqlite` | 51.2 MB | `threads`、`guardian_review_feedback` 等 17 表 | `guardian_review_feedback.record` 是 1.68 MB 大 BLOB |
| `models_cache.json` | 398 KB | `/models` 响应缓存 | `fetched_at 2026-10-22T16:56:23Z` |
| `config.toml` | 11.5 KB | 客户端配置 | mtime 2026-10-23 00:49 |
| `sessions/**`, `archived_sessions/**` | 197 个 `*.jsonl`（64,142 行） | rollout 转录 | 用于交叉验证 |

**我建立的新副本**：`scratch/sqlite-copy-client/`（logs_2 +wal +shm、state_5、queue_1、thread_history_1）。
**复用**：旧 teammate 在 `scratch/sqlite-copy/`（2026-10-22 02:54 时点）的 logs_2 副本——它保留了**新副本已丢失的 10/20–10/21 行**（见 §0.1）。

### 0.1 日志裁剪导致的坑（必须知道）

`logs_2.sqlite` 不是"保留最近 N 小时"，而是按容量裁剪，同一时段可能**部分行被删**：

| 副本 | 行数 | 覆盖（JST） |
|---|---|---|
| `scratch/sqlite-copy/logs_2.sqlite`（旧副本） | 19,854 | 2026-10-20 21:10 → 2026-10-22 02:51 |
| `scratch/sqlite-copy-client/logs_2.sqlite`（新副本） | 28,725 | 2026-10-21 17:11 → 2026-10-23 01:52 |

两副本按 `logs.id` **合并去重**后 = 35,810 行，覆盖 **2026-10-20 21:10 → 2026-10-23 01:52 JST（约 33 h）**。
例：10/21 21:54–22:28 的 `gpt-6-astra` 转账 77 条请求在旧副本里有、新副本里已被裁掉 → **本文所有计数都用"合并去重"后的数据**。
10/20 00:00–21:10 的日志已永久丢失；更早只有 rollout/thread_history 可查。因此"最近 48 h"（≥10/21 01:52）的请求日志**完整**，但 10/20 只能给 ws 连接碎片的痕迹。

---

## 1. 结论速览

1. **上游现在仍然下发 `x-codex-turn-state`。** 合并窗口内共 **142 条**，最近一次 **2026-10-23 00:40:34 JST**（10/22 有 104 条、10/23 有 9 条），**长度 100% 为 780 字符**、前缀 `gAAAAA`、base64url 字符集 64 种、解码 585 字节（首字节 `80 00 00 00 00 6a d8 9a`）。
2. **出现位置 = HTTP 响应头**（`codex_http_client::client` 的 `Request completed ... status=200 OK headers={…} version=HTTP/1.1` 原始响应头 dump）；**不是** SSE 事件、**不是** WS 帧。SSE body 与 WS 握手/帧在客户端根本不落日志（见 §7）。
3. **条件是"一个 turn 的第一次 /responses 请求"**：141 个 turn 里 **139 个（98.6%）**首次 HTTP 响应带票；**203 条同 turn 的后续（continuation）响应 0 条带票**。因此票是"每 turn 一张、首个非增量请求下发"，不是"每个请求都下发"。
4. **`__oailb` 仍然下发，但只出现在 `GET /backend-api/codex/models`**（128/1642 次，值长 317，属性 `Path=/; Expires=…; Max-Age=…; HttpOnly; Secure; SameSite=Lax`，每次都与 `__cflb` 同时下发）。**`POST /responses` 的 347 条响应里 0 次 `__oailb`**；`/responses` 上的 Set-Cookie 只有 Cloudflare 的 `__cf_bm`（15/347，值长 197–199）。→ 与 Lead 的判断一致：**在 /responses 抓 Set-Cookie 只能抓到 `__cf_bm`，会产出"cookie-only 假票"**。
5. **没有 401/403/429、没有 auth 失效、没有 plan/entitlement 变化**：347 条 `/responses` 完成记录**全部 status=200**；16 类失败全是网络层 `error sending request`（`is_connect=true/false`），不是 HTTP 拒绝；`auth_error` 在 2,726 条 feedback_tags 里全为空；`x-codex-plan-type: pro`、`x-codex-active-limit: premium`、`x-codex-credits-balance: 63000.0000000000` 全程不变。
6. **10/22–10/23 的变化不是"票停了"，而是：客户端大版本更新两次（26.1013.40934 → 26.1016.51333 → 26.1016.61520）、`service_tier` 由 default 切到 priority（Fast 模式）、HTTP/WS 混用与网络抖动变多（2 次 fallback to HTTP、11 次 ws `tls handshake eof`、19 次 http 连接失败），并新观测到上游响应头 `x-codex-safety-buffering-enabled: true` / `x-codex-safety-buffering-faster-model: gpt-6-luna`**。
7. **票长度与网关假设不符**：客户端观测**恒为 780**，本窗口内**从未出现 292/308/332**。客户端日志只保留 33 h，**无法证伪**"2026-09-22 起 292/332 → ~780"的说法，但可以确认"现在=780"。若打票代码按 292 校验/截断，会收不到票。

---

## 2. Q1：最近 48 h 的 `/responses` 走 HTTP 还是 WebSocket？各多少？按模型？

判定口径（重要）：
- **HTTP**：`codex_http_client::client` 的 `Request completed/failed method=POST url=…/codex/responses`，**逐请求精确**。
- **WS**：`codex_api::endpoint::responses_websocket` 的 `connecting / successfully connected / failed to connect`；ws 传输**每个 /responses 流开一条连接**（同秒多个 span 对应并发子 agent/guardian 请求），`websocket.warmup=true` 的预热连接每天仅 4 条，可忽略。**ws 响应头不落日志**，所以 ws 请求的 header 信息不可见。
- 时间窗：**≥ 2026-10-21 01:52 JST（最近 48 h）**；括号内为全合并窗口。

| 指标 | 最近 48 h 合计 | 10/21 | 10/22 | 10/23(00:00–01:52) |
|---|---|---|---|---|
| HTTP `/responses` 完成（全部 200） | **347** | 106 | 158 | 83 |
| HTTP `/responses` 失败（网络层） | 19 | 3 | 3 | 13 |
| WS 连接尝试 | 135 | 44 | 79 | 12 |
| WS 连接成功（≈ ws 请求数） | **108** | 33 | 68 | 7 |
| WS 连接失败（`tls handshake eof`） | 11 | 9 | 2 | 0 |
| turn-state 条数 | 142 | 29 | 104 | 9 |
| `x-codex-safety-buffering-*` 条数 | 209 | 77 | 58 | 74 |
| `__oailb`（仅 /models） | 111 | 74 | 34 | 3 |
| fallback to HTTP 告警 | 2 | 0 | 2 | 0 |
| stream disconnected 重试 | 29 | 15 | 11 | 3 |
| models 刷新超时 ERROR | 107 | 86 | 12 | 9 |

**HTTP 的模型分布**（合并窗口，含少量 10/21 旧副本独有行）：

| 日(JST) | codex-auto-review | gpt-6-astra | gpt-6.1-sol | 无 model 字段(guardian) |
|---|---|---|---|---|
| 10/21 | 26 | 77 | 0 | 3 |
| 10/22 | 100 | 26（全部 `inter_agent_communication`） | 32（全部 `inter_agent_communication`） | 0 |
| 10/23 | 9 | 0 | 74（`turn_input`，同一长 turn 的续请求） | 0 |

**WS 的模型分布不可得**（ws 不落响应头/帧；span 里的 `model=` 会在同一请求的多行重复出现，无法可靠去重）。可用旁证：`feedback_tags` 里 ws span 的 model 覆盖 `gpt-6.1-sol / gpt-6-astra / codex-auto-review / gpt-6-luna`，与 HTTP 侧集合一致。

**结论**：48 h 内两种 transport 混用；HTTP 有 347 条完成 + 19 条失败，WS 有 108 条成功连接（+11 失败）。10/23 凌晨 HTTP 占比明显升高（83 vs 7），且同期有 13 条 HTTP 连接失败（见 §5）。

---

## 3. Q2：`x-codex-turn-state` 出现位置 / 条件 / 长度 / 时间线

### 3.1 出现位置（Lead 关注点 1）

唯一出现位置：`codex_http_client::client`（DEBUG）形如

```
… responses.stream_request{transport="responses_http" http.method="POST" api.path="/responses"}:…:
Request completed method=POST url=https://chatgpt.com/backend-api/codex/responses status=200 OK
headers={"date": …, "x-codex-plan-type": "pro", …, "x-codex-turn-state": "<REDACTED>", "x-oai-request-id": …} version=HTTP/1.1
```

- 142 条记录，每条**只出现 1 次**，位置都在 ` Request completed ` 之后的 ` headers={…} ` 块内 ⇒ **这是 HTTP 响应头**（reqwest 打印的响应头），不是 SSE 事件。
- 全库检索 `response.created` = **0**，`response.metadata` = **0** ⇒ 日志里**不存在** SSE 形式（`response.metadata.headers`）的票据记录（SSE body 客户端不落日志，不能证明上游没在 SSE 里发）。
- WS 只有 `connecting / successfully connected / failed to connect` 三种行，**没有握手响应头**⇒ WS 101 握手是否带票**不可见**。
- 也不出现在 `GET /models`、`/analytics-events`、`/responses/compact`（窗口内未观测到 compact 调用）里。

### 3.2 下发条件（核心）

用 span 里的 `turn.id`（36-hex）把 347 条 HTTP `/responses` 归到 141 个 turn：

| 位置 | 响应数 | 带票数 |
|---|---|---|
| turn 内第 1 条 HTTP `/responses` | 141 | **139 (98.6%)** |
| turn 内后续（continuation） | 203 | **0** |

按模型看带票请求：`codex-auto-review` **135**、`gpt-6-astra` **3**、`gpt-6.1-sol` **1**、无 model 字段的 guardian 请求 **3**。
（`codex-auto-review` 之所以"看起来每票都有"：它是短 turn，一个 turn 只有 1 条 HTTP 请求，且几乎都走 HTTP；而用户主模型的长 turn 大多数续请求走 WS 或没有 header 记录。）

带票的 4 条 `gpt-6-astra / gpt-6.1-sol` 请求全部是 `inter_agent_communication`，其中 3 条紧跟在 **ws→HTTP fallback / 断流重试**之后（10/22 20:45:57、20:55:58、20:57:58、22:47:08；fallback 告警在 20:45:38、22:46:21）⇒ 与"**非增量/新上下文请求才发票**"一致。

2 个例外 turn（首条 HTTP 响应**无票**，其余全部是续请求）：
- `01a15e7a`：10/21 21:54:34–22:28:37，`gpt-6-astra`，77 条；
- `01a1641c`：10/23 00:20:09–00:47:24，`gpt-6.1-sol`，74 条（该 turn 在 23:59:24 有 shutdown/interrupt，00:45–00:50 有 13 条网络失败）。
这两个 turn 的真实首请求没有出现在 HTTP 完成日志里（很可能首请求走了 ws 或无 header 记录），故**不推翻**上面的规则，但也**未能完全解释**。

### 3.3 长度、内容形态与"292→780"问题（Lead 关注点 2）

| 属性 | 观测值 |
|---|---|
| 条数 | 142（142 个互不相同） |
| 长度 | **全部恰好 780 字符**（无 292 / 308 / 332 / 其它长度） |
| 前缀 | `gAAAAA`（全部） |
| 字符集 | 64 种 base64url（`A-Za-z0-9-_`），无 `+ / =` |
| 解码后 | 585 字节；首 8 字节 `80 00 00 00 00 6a d8 9a` |
| 复现性 | 同一 turn 内不会重复出现；无跨 turn 复用 |

要点：`gAAAAA` 只是 `0x80 00 00 …` 的 base64，**不是 gzip**（gzip 的 base64 头是 `H4sI`）。若打票/解析代码把它当 gzip 解，会直接失败。
**无法证伪 9/22 的 292/332→780 变化**：客户端日志只到 10/20 21:10，rollout 里 9 月的 `gAAAAA`（9,749 行）是**加密 reasoning 内容**，不是票。能确认的是：**10/21 19:57 – 10/23 00:40 之间，客户端看到的票恒为 780**。网关配置里 `target_length: 292`（见 §8 说明）与本机观测**不一致**，这是"空票"的强候选原因之一（仅提出，不结论）。

### 3.4 时间线（最近票）

| 时间(JST) | 说明 |
|---|---|
| 10/21 19:57:23 | 合并窗口内**最早**观测到的票（guardian 请求） |
| 10/21 20:34–22:25 | 26 条 `codex-auto-review` 票 + 3 条 guardian 票 |
| 10/22 13:20–19:33 | 大量 `codex-auto-review` 票（≈100 条/天） |
| 10/22 20:45–22:47 | 4 条非 auto-review 票（fallback/重试后的非增量请求） |
| 10/23 00:36:50 – **00:40:34** | 最后 9 条票（全部 `codex-auto-review`） |
| 10/23 00:47:24 | 最后一条可读头的 HTTP `/responses`（无票，是长 turn 续请求）；之后到 01:52 全走 ws（头不可见） |

⇒ **"上游还发不发"的答案：发。至少在 10/23 00:40:34 JST 仍在发；之后没有 HTTP 头可观测样本，不能外推到"此刻"。**

---

## 4. Q3：`__oailb` / `__cf_bm` / `__cflb`（Lead 关注点 3）

合并窗口内 `/responses` 完成 347 条、`/models` 完成 1,642 条，逐条解析 `set-cookie`（同名多值都保留）：

| Cookie | 值长 | 出现端点 | 次数 | 属性（模板） |
|---|---|---|---|---|
| **`__oailb`** | **317** | **仅 `GET /backend-api/codex/models`** | **128** | `Path=/; Expires=…; Max-Age=…; HttpOnly; Secure; SameSite=Lax` |
| `__cflb` | 43 | 仅 `GET /models`（与 `__oailb` **成对**，128 次） | 128 | `HttpOnly; SameSite=None; Secure; Path=/; Expires=…` |
| `__cf_bm`（Cloudflare bot mgmt） | 197/198/199 | `/models` 470 次 **+ `/responses` 15 次** | 485 | `HttpOnly; SameSite=None/Lax; Secure; Path=/; Domain=…; Expires=…` |
| `_cfuvid` | 113 | `/alpha/search` 1 次 | 1 | — |

关键结论：

1. **`POST /responses` 从未下发 `__oailb`/`__cflb`**（0/347）；`/responses` 上唯一的 Set-Cookie 是 **`__cf_bm`（15/347 = 4.3%）**。
2. 打票代码若"抓 /responses 的任意 Set-Cookie 当票"，拿到的只能是 `__cf_bm` → **"cookie-only 假票"**，与 Lead 的判断一致。
3. `__oailb` 的刷新节奏：10/20–10/21 约每 **13–20 min** 一次；10/22 约每 **20–60 min**；10/23 仅 3 次（00:33:46、01:02:02、01:37:11，最后一次=窗口内最后）。两次刷新间隔 ≈ cookie 有效期，说明客户端**cookie jar 里有票时不重复下发**。
4. ⚠️ 限制：客户端**一直带着已有的 `__oailb` cookie**，因此"**无 cookie 的 /responses 探针**会不会拿到 `__oailb`"本机**观测不到**（这是网关 probe 的场景）。
5. `/responses` 上 `__cf_bm` 与 `x-codex-turn-state` 的关系：15 条中 7 条同时有票、8 条无票（都是长 turn 续请求）⇒ `__cf_bm` 的刷新与票**无关**。

---

## 5. Q4：401/403/429、错误码、auth、plan/entitlement、降级

### 5.1 没有任何 HTTP 拒绝

- 所有 `Request completed`（`/responses` 347 条、`/models` 1642 条、`/analytics-events` 若干）**status 全部 200**；**0 条 4xx/5xx**。
- 全库子串 `' 401 '` = **0 行**；`401/403/429` 的 1,000+ 次命中全部是 **UUID/时间戳/base64 片段**（`codex_core::stream_events_utils` 等 target 的哈希文本），不是状态码。
- 19 条 `/responses` 失败全是传输层：`error sending request for url (https://chatgpt.com/backend-api/codex/responses)`，`is_connect=true` 16 条 / `is_connect=false` 3 条，`is_timeout=false`；**没有 HTTP 响应**（即没有状态码可判断账号状态）。
- 14 条 ws 失败全是 `IO error: tls handshake eof`：10/20 21:55 ×3、10/21 22:41:31–22:41:53 ×9、10/22 22:45:02/22:46:03 ×2。
- 2 条 `WARN codex_core::client: falling back to HTTP`：**2026-10-22 20:45:38**（gpt-6-astra）、**2026-10-22 22:46:21**（gpt-6.1-sol），都是 `inter_agent_communication`。

### 5.2 auth / entitlement 无变化

| 证据 | 观测 |
|---|---|
| `feedback_tags` 的 `auth_mode` | 2,726/2,726 = `Chatgpt` |
| `auth_error` / `auth_error_code` / `auth_recovery_mode` / `auth_recovery_phase` | **全为空字符串**（0 条非空） |
| `auth_header_attached` | 全 true |
| `x-codex-plan-type` | 全 `pro`（347/347） |
| `x-codex-active-limit` | 全 `premium`（347/347） |
| `x-codex-credits-balance` / `has-credits` / `unlimited` | `63000.0000000000` / `True` / `False` 全程不变 |
| `x-codex-primary-used-percent` | 29（10/21 19:57）→ 46（10/22 16:47 UTC）；单调上升，无重置/换档 |
| `thread_history.thread_turns` | 3,614 turn：completed 3,407 / interrupted 109 / failed 86 / inProgress 12；**10/22 失败 0、10/23 失败 0**（失败集中在 8–9 月与 10/19） |

### 5.3 历史（非本窗口）曾出现网关侧拒绝——仅供排除

`thread_turns.error_json` 里有 2026-08–09 指向**自建网关**的旧失败（说明客户端曾把 `base_url` 指到网关）：
`http://182.44.8.75:8900/v1/responses`、`http://182.43.82.89:8900/v1/responses` 的
`502 Bad Gateway`(3)、`503 Service Unavailable`(4)、`407 Proxy Authentication Required`(1)、
`404 "Model gpt-6-astra is not supported by any configured account in this group"`、
`401 Unauthorized … auth error code: invalid_api_key`（`https://api.openai.com/v1/responses`，1 条，2026-09-06）。
**这些都不是 10/22–10/23 的事件**，不能用于判断现在的上游行为。

### 5.4 降级/降智线索（Lead 关注点 4）

- 响应头 **`x-codex-safety-buffering-enabled: true`** + **`x-codex-safety-buffering-faster-model: gpt-6-luna`**：出现 **209/347** 条 `/responses` 响应，首次 **10/21 21:54:34**、最后 **10/23 00:47:24**。
  分布**严格按模型**：所有 `gpt-6-astra`(77+26) 与 `gpt-6.1-sol`(32+74) 的 HTTP 响应都有；**所有 `codex-auto-review`/guardian 响应都没有**。
  值恒定：`true` / `gpt-6-luna`（无变化）。
- **`response.created.response.model` 与请求模型的对比不可得**：客户端不落 SSE body（`response.created` 命中 0）。无法用本机日志证明"请求 gpt-6.1-sol、实际被 gpt-6-luna 服务"。
- 需排除误读：`gpt-6-luna` 也确实被客户端**自己**作为子 agent 模型请求过（`feedback_tags` 里 `model="gpt-6-luna"` + `tags_json` 含 `agent_default_subagent…`，10/22–10/23 有 106 条），所以"日志里出现 gpt-6-luna"本身不等于上游替换。
- 其它可能的降级痕迹：`x-codex-primary-used-percent` 29→46 只是用量；`stream disconnected` 重试 29 次、models 刷新超时 107 次都是网络侧。

---

## 6. Q5：10/20–10/21 与 10/22–10/23 的差异（时间线表）

| 时间(JST) | 事件 | 证据 |
|---|---|---|
| 10/20 21:10 – 10/21 22:41 | Desktop `26.1013.40934` | `app_server.client_version` 243 行 |
| 10/21 19:57 | 本窗口**首张票**（780） | logs |
| 10/21 21:54–22:28 | 首个 `x-codex-safety-buffering-*` 观测（gpt-6-astra 77 条 HTTP） | logs |
| 10/21 22:41 | ws 连续 9 次 `tls handshake eof` | logs |
| 10/22 00:35 – 10/22 19:34 | Desktop `26.1016.51333`（**第 1 次升级**） | `app_server.client_version` |
| 10/22 00:55 | `thread_settings.service_tier` 出现首个 `priority`；10/22 全天 priority 351 / default 50；**10/23 起全 priority** | rollouts（`sse` 事件 `thread_settings`） |
| 10/22 13:06 | 新会话开始（10/21 22:30→10/22 13:00 无活动） | logs |
| 10/22 19:37 | 插件/运行时目录刷新（升级准备） | `.codex` mtime |
| 10/22 **20:45:38** | 第 1 次 `falling back to HTTP` | `codex_core::client` WARN |
| 10/22 **20:59:17** | Desktop `26.1016.61520`（**第 2 次升级**）生效 | `app_server.client_version` |
| 10/22 22:45–22:47 | ws 2 次 `tls handshake eof` + 第 2 次 fallback + 3 条 http 失败 | logs |
| 10/23 00:20–00:50 | 单个长 turn 74 条 HTTP 续请求；**13 条连接失败**；analytics/MCP 也超时（本机网络抖动） | logs |
| 10/23 00:40:34 | **最后一张票（780）** | logs |
| 10/23 00:51 | 之后转 ws；01:37:11 最后一次 `__oailb`（/models） | logs |

**没有变化的部分（很关键）**：
- 票的**形态完全没变**：两个窗口都是 780 / `gAAAAA` / base64url / 首部 `0x80`；下发规则（每 turn 首请求）也一样。
- `__oailb` 的**下发端点没变**（只 /models），值长 317 没变，属性没变。
- **错误面没变好也没变坏成 HTTP 拒绝**：两窗口都没有 4xx/5xx；变化的是**连接层抖动**（ws handshake eof、http send error、models 超时）在 10/22 晚–10/23 凌晨变密集。
- `models_cache.json`：`etag = W/"094e399bfb37eb6db2cfd48547eed37b"` **与 10/21 完全相同**，仍 9 个模型、`client_version=0.163.0` 不变 ⇒ **模型目录无替换/下架**。
- 客户端库版本仍 `0.163.0`；`x-models-etag` 每条响应都回同一值。

---

## 7. Lead 追加 4 点：逐条回答

1. **出现位置**：**HTTP 响应头**（`codex_http_client::client` 的原始响应头 dump）。
   - 不是 SSE `response.metadata`：客户端不落 SSE body，全库无 `response.metadata`/`response.created`。→ **不能证明 SSE 里没有**，只能说本机没有该形态的记录。
   - 不是 WS 帧/握手：WS 只记录 connect/success/failed 三行。→ **WS 上是否发票不可见**。
   - 对打票的含义：若打票用 **HTTP POST** 且读响应头，就能拿到；若它读 SSE 事件或只读 body，则本机证据显示客户端路径上是"响应头"，读 body 会抓空。
2. **每个请求是否带头**：347 条 HTTP `/responses` 里 **142 条（40.9%）** 带；按 turn 看是**首请求 139/141（98.6%）**、**续请求 0/203**。长度分布：**780×142**（无其它长度）。
   对"9/22 起 292/332→780"：**无法证伪**（客户端日志只有 33 h），但**与"现在≈780"一致**；本机窗口内从未见 292/332。
3. **`__oailb` vs `__cf_bm`**：`__oailb` 只在 `GET /models` 下发（128 次，值 317，与 `__cflb` 成对），`/responses` **0 次**；`/responses` 唯一的 Set-Cookie 是 `__cf_bm`（15 次，值 197–199）。⇒ **在 /responses 抓任意 Set-Cookie 必然只得到 `__cf_bm`，即"cookie-only 假票"**。
4. **safety-buffering / 模型不一致**：`x-codex-safety-buffering-enabled: true` 与 `x-codex-safety-buffering-faster-model: gpt-6-luna` 在 **209/347** 条 `/responses` 响应上（=全部 gpt-6-astra/6.1-sol 的 HTTP 响应，10/21 21:54 → 10/23 00:47），`codex-auto-review` 上从不出现。**`response.created.response.model` 不可得**（SSE body 不落日志），故"请求模型≠实际模型"本机**无法坐实**；只记录这两条头作为"安全缓冲/更快模型"的路由标记，以及 `gpt-6-luna` 也是客户端子 agent 自己的请求模型这一干扰项。

---

## 8. 注意：客户端产物里"看起来像上游证据"的项目自身数据

`state_5.guardian_review_feedback.record`（1.68 MB BLOB，thread `01a14617…`）、`thread_history.thread_items`（21 条）和 rollout jsonl（`turn-state` 19 行 / `oailb` 9 行）里出现的 `x-codex-turn-state`、`292`、`__oailb`、`openai_codex_ticket` **全部是 sub4api 网关自身的 Go 代码/配置/迁移文件内容**（被 Codex 当作"待评审文本"读入），**不是客户端运行现象**，例如：
- `openai_codex_ticket: enabled: false / target_length: 292 / ttl_seconds: 3600`；
- `ErrOpenAICodexTicketUnavailable`、`203_codex_ticket_oailb_invalidation`、`244_disable_codex_ticket_harvesting` 等迁移名。
这些是项目侧已知信息（Lead 的 patch 线已覆盖），我只用来解释"292 这个数字从哪来"，**不作为上游证据**，也未在其中寻找答案。

---

## 9. 未知项 / 局限

1. **WS 响应头与帧不落日志** → ws 请求是否带票、票是否出现在 101 握手头 / `response.metadata`，本机**无法判断**。
2. **SSE body 不落日志** → 无法看到 `response.created.response.model`，也无法看到 SSE 里是否另有 header 副本。
3. **客户端是否回带票不可直接观测**（不记录请求头）。"票只在每 turn 首请求出现、续请求没有"是**强行为证据**，与"客户端回带、上游不重复发"一致，但不是直接抓包。
4. **日志只有 33 h**（合并两副本）→ 无法验证 9/22 的长度变化；10/20 00:00–21:10 无日志。
5. **无 cookie 的 `/responses` 探针**（= 网关打票的真实场景）本机观测不到，因为客户端始终带着 `__oailb` cookie。因此"`/responses` 是否可能对无 cookie 请求下发 `__oailb`"仍是**开放问题**。
6. 2 个例外 turn 首条 HTTP 响应无票，未能完全解释（真实首请求不在 HTTP 完成日志内）。
7. 292 与 780 的差异来源未知：可能是网关 probe 的请求形状不同（模型/tier/元数据头/是否带旧票），也可能是上游按请求内容变化；本机无法区分。
8. `x-codex-primary-used-percent` 出现 1..100 的多种值（疑与不同窗口/快照有关），未深究；只用 29→46 的趋势。

---

## 10. 可复现 SQL / 脚本

### 10.1 复制（不要直接查原库）

```powershell
$dst='D:\projects\sub4api\scratch\sqlite-copy-client'; New-Item -ItemType Directory -Force $dst | Out-Null
$src='C:\Users\zfx\.codex'
foreach ($n in @('logs_2.sqlite','state_5.sqlite','queue_1.sqlite')) {
  foreach ($suf in @('','-wal','-shm')) {
    $f=Join-Path $src ($n+$suf); if (Test-Path $f) { Copy-Item -LiteralPath $f -Destination (Join-Path $dst ($n+$suf)) -Force }
  }
}
Copy-Item -LiteralPath (Join-Path $src 'thread_history_1.sqlite') -Destination (Join-Path $dst 'thread_history_1.sqlite') -Force
```

### 10.2 解析器（合并两份 logs 副本 → 逐请求记录；Python 3.14 标准库）

```python
import sqlite3, re, json, datetime as dt
JST = dt.timezone(dt.timedelta(hours=9))
def LT(t): return dt.datetime.fromtimestamp(t, dt.UTC).astimezone(JST).strftime("%Y-%m-%d %H:%M:%S")
HDR = re.compile(r'"([A-Za-z0-9_\-]+)": "((?:[^"\\]|\\.)*)"')
def headers(body):
    i = body.find(" headers={")
    if i < 0: return []
    i += len(" headers={"); j = body.rfind("} version=")
    if j < 0: return []
    # 直接对原文跑正则；不要先做 \\" -> " 的全量反转义（会破坏 etag 里的 \"）
    return [(m.group(1), m.group(2).replace('\\"','"')) for m in HDR.finditer(body[i:j])]
def pm(b, pat):
    m = re.search(pat, b); return m.group(1) if m else None

rows = {}
for db in (r"scratch/sqlite-copy/logs_2.sqlite", r"scratch/sqlite-copy-client/logs_2.sqlite"):
    c = sqlite3.connect(db); c.row_factory = sqlite3.Row
    for r in c.execute("select id,ts,target,level,feedback_log_body from logs"):
        rows.setdefault(r["id"], dict(ts=r["ts"], target=r["target"], level=r["level"], body=r["feedback_log_body"] or ""))

for v in rows.values():
    b = v["body"]
    if v["target"] == "codex_http_client::client" and " Request completed " in b:
        h = headers(b)
        d = {}
        for k, val in h: d.setdefault(k.lower(), []).append(val)
        if "/responses" in b:
            ts_val = (d.get("x-codex-turn-state") or [None])[0]
            print(LT(v["ts"]), "turn" in b, pm(b, r'model=([A-Za-z0-9\.\-_]+)'),
                  pm(b, r'codex\.op="([a-z_]+)"'), pm(b, r'status=(\d+)'),
                  "ts_len=", len(ts_val) if ts_val else None,
                  "sbe=", (d.get("x-codex-safety-buffering-enabled") or [None])[0],
                  "cookies=", [(m.group(1), len(m.group(2))) for s in (d.get("set-cookie") or []) for m in [re.match(r'([A-Za-z0-9_\-]+)=([^;]*)', s)] if m],
                  "turn=", pm(b, r'turn\.id="?([0-9a-f\-]{36})"?'))
```

### 10.3 关键 SQL

```sql
-- 1) 目标/等级分布与时间范围（注意 ts 是 UTC epoch 秒）
SELECT datetime(min(ts),'unixepoch'), datetime(max(ts),'unixepoch') FROM logs;
SELECT target, count(*) FROM logs GROUP BY target ORDER BY 2 DESC;

-- 2) 出现票的行（唯一位置：http client 的响应头 dump）
SELECT ts, feedback_log_body FROM logs
WHERE instr(feedback_log_body,'x-codex-turn-state')>0 ORDER BY ts;

-- 3) 每个 /responses 请求的完成/失败
SELECT ts, target, feedback_log_body FROM logs
WHERE target='codex_http_client::client'
  AND (instr(feedback_log_body,'/responses')>0)
  AND (instr(feedback_log_body,' Request completed ')>0 OR instr(feedback_log_body,' Request failed ')>0)
ORDER BY ts;

-- 4) __oailb 只出现在 /models
SELECT target, count(*) FROM logs WHERE instr(feedback_log_body,'__oailb')>0 GROUP BY target;
-- 5) 是否有任何 4xx/5xx 完成（本窗口：0）
SELECT count(*) FROM logs
WHERE target='codex_http_client::client' AND instr(feedback_log_body,' Request completed ')>0
  AND feedback_log_body NOT LIKE '%status=200%';
-- 6) fallback / 断流 / models 超时
SELECT ts, feedback_log_body FROM logs WHERE instr(feedback_log_body,'falling back to HTTP')>0;
SELECT count(*), min(ts), max(ts) FROM logs WHERE target='codex_core::responses_retry';
SELECT count(*), min(ts), max(ts) FROM logs WHERE target='codex_models_manager::manager' AND level='ERROR';
-- 7) auth 字段（应全空）
SELECT count(*) FROM logs WHERE target='feedback_tags' AND instr(feedback_log_body,'auth_error=""')=0;
```

### 10.4 rollout / thread_history

```python
# tier 变化（rollout: {"timestamp","payload":{"type":"thread_settings","thread_settings":{...}}}）
import glob, json
for f in glob.glob(r"C:\Users\zfx\.codex\**\*rollout*.jsonl", recursive=True):
    for line in open(f, encoding="utf-8", errors="replace"):
        if '"thread_settings"' not in line: continue
        j = json.loads(line); tsk = j["payload"]["thread_settings"]
        print(j["timestamp"], tsk.get("service_tier"), tsk.get("model"))
```

```sql
-- thread_history: turn 状态/错误（started_at 是微秒）
SELECT status, count(*) FROM thread_turns GROUP BY status;
SELECT datetime(started_at/1000000,'unixepoch','+9 hours') d, status, error_json
FROM thread_turns WHERE error_json <> '' ORDER BY started_at DESC LIMIT 30;
-- thread_items 里的 turn-state/oailb = 项目代码评审文本，不是运行证据
SELECT count(*) FROM thread_items WHERE instr(item_json,'turn-state')>0;
```

---

## 11. 给打票线的直接提示（仅基于客户端证据）

1. **票还在**（10/23 00:40:34 JST 仍有 780 字符票）。若打票 401/403/空票，优先怀疑：**请求形状**（是否像"turn 首个非增量请求"）、**读票位置**（HTTP 响应头 ✅ / SSE body ❌）、**长度假设**（292 vs 780）。
2. **`gAAAAA` 不是 gzip**：按 gzip 解会失败（解码首部 `80 00 00 00 00 6a d8 9a`）。
3. **不要在 /responses 抓 Set-Cookie 当票**：会拿到 `__cf_bm`（假票）。`__oailb` 在客户端路径上只从 `GET /models` 来。
4. 本窗口**没有任何 401/403/429/entitlement 证据**；若网关侧看到 401/403，那与"本机官方客户端 + 同一 OAuth 账号"的路径不同（请求头/请求体/IP/客户端版本差异），值得单独对比。

# Codex 上游流量保真改造方案

> 目标：让 OpenAI OAuth（Codex）账号的出站流量在**上游可观测特征**上尽量接近官方 Codex Desktop / Codex CLI，降低被风控判定为"聚合中转"的概率，减少 `server_is_overloaded` 降级、额度缩水与封号。
>
> 本文只讨论"出站请求长什么样"，不涉及计费、账号获取与上游策略假设。

---

## 0. 为什么需要这份文档

当前网关在**应用层身份**上已经做得比同类项目细：UA / `originator` / `version` 同源、session/thread 派生、时区改写、header 白名单、`x-codex-turn-state` 票据、`x-codex-beta-features` 注入。

但"像不像官方客户端"不是单看 header。上游能拿到的信号分六层，任何一层自相矛盾，都会把整条流量拉进"非官方"分类：

| 层 | 上游可见信号 | 官方 Codex（本地桌面/CLI） | 当前网关 | 差距 |
|---|---|---|---|---|
| 1 传输 | TLS ClientHello / JA3 / JA4 / 证书协商 | rustls 特征 | Go `crypto/tls`（OpenAI 路径**未启用** uTLS） | **P0** |
| 2 协议 | HTTP/2 SETTINGS、窗口、流调度；或 HTTP/1.1 | rust `h2` | Go `x/net/http2` | **P0** |
| 3 头 | 头集合、顺序、`accept-encoding`、`x-codex-*` | 全量 Codex 头 | 白名单透传 + 少量补注 | P1 |
| 4 体 | `instructions`、tools、`input` 结构、`client_metadata`、`prompt_cache_key` | 官方形态 | 透传（非 Codex 客户端则完全不是 Codex 形态） | **P0** |
| 5 身份 | `installation_id` / session / thread / window、设备数 | 1 台设备、少量会话 | 一个账号 N 个用户 → N 个 installation | **P0** |
| 6 网络/行为 | 出口 IP/ASN、时区、语言、并发曲线、探测节律、额度曲线 | 住宅 IP、单一地区、人类节律 | 代理池、聚合并发、6s 级探测 | P1/P2 |

**核心矛盾**：第 3 层声称自己是 `codex_cli_rs`，第 1、2、4 层不是。这比"UA 写错"更容易被机器判定。

---

## 1. 现状盘点（避免重复劳动）

### 已经对齐、无需改动

| 能力 | 位置 |
|---|---|
| UA / `originator` / `version` 三元同源自洽 | [openai_codex_identity.go:140](backend/internal/service/openai_codex_identity.go#L140)、[:225](backend/internal/service/openai_codex_identity.go#L225) |
| 出站 `session_id` / `conversation_id` 统一为 UUID 形态并按用户隔离 | [openai_codex_account_identity.go:80](backend/internal/service/openai_codex_account_identity.go#L80)、[:109](backend/internal/service/openai_codex_account_identity.go#L109) |
| `prompt_cache_key` 与 `client_metadata` 同步改写 | [openai_codex_fingerprint.go:439](backend/internal/service/openai_codex_fingerprint.go#L439) |
| `environment_context` 时区 / `current_date` / `web_search.timezone` 改写 | [openai_request_locale.go:99](backend/internal/service/openai_request_locale.go#L99) |
| `x-codex-beta-features` 会话级补注（`remote_compaction_v2`） | [openai_compact_body_signal.go:160](backend/internal/service/openai_compact_body_signal.go#L160) |
| 跨账号 `turn-state` 剥离，防止 failover 后身份自相矛盾 | `guardOpenAICodexTurnStateEcho`（[openai_gateway_forward.go:1490](backend/internal/service/openai_gateway_forward.go#L1490)） |
| 官方客户端准入（`codex_cli_only` + 引擎指纹） | [account.go:2473](backend/internal/service/account.go#L2473)、[engine_fingerprint_signal.go:29](backend/internal/pkg/openai/engine_fingerprint_signal.go#L29) |
| 同栈 `codex-rs` 版本下限与 UA/version 联动 | [openai_codex_identity.go:16](backend/internal/service/openai_codex_identity.go#L16) |

### 未对齐（本文要解决的）

1. OpenAI 路径**没有** TLS 指纹伪装，且即使开也跑不了 HTTP/2（P0）。
2. 设备/会话收敛默认关闭，账号被多人共用（P0）。
3. 非 Codex 客户端（opencode / Claude Code / chat-completions 桥）可以进入 OAuth 账号池，body 形态与头矛盾（P0）。
4. IP / 时区 / 语言 / UA 平台段四件套不一致，语言甚至没有统一实现（P1）。
5. `x-codex-installation-id` / `x-codex-window-id` / `x-codex-turn-metadata` 缺失时不补（P1）。
6. 客户端版本只做到**单请求自洽**，没做到**单设备恒定**：同一账号同一 installation 会在不同请求上交替声明两个版本（P1）。
7. `environment_context` 只改写时区/日期，`cwd` / `shell` 原样透传 → 一个 installation 下泄漏 N 台机器（P1）。
8. 打票探测 6 秒一次 + 同账号轮换出口 IP（P1）。
9. 单账号并发与额度曲线跑满，失败即换号（P2）。

> 第 6、7 项是写完初版方案后补的。它们不会单独造成"被判定"，但会让第 2 项（设备收敛）白做：收敛了 `installation_id`，却从 `cwd` 和版本抖动里继续暴露"多机、多版本"。

---

## 2. 逐项改造

每项按「问题 → 为什么这样实现 → 改哪里 → 验收 → 回滚」写。

### P0-1 让 Codex 出站走 rustls 画像并真正协商 HTTP/2

#### 问题
- 主转发路径用 `httpUpstream.Do`，走 Go 默认 TLS：[openai_plugin_transport.go:26](backend/internal/service/openai_plugin_transport.go#L26)。
- TLS 指纹能力被硬限制在 Anthropic 账号：[account.go:2516](backend/internal/service/account.go#L2516)、[tls_fingerprint_profile_service.go:177](backend/internal/service/tls_fingerprint_profile_service.go#L177)。所以即使管理员给 OpenAI 账号配 `enable_tls_fingerprint`，`ResolveTLSProfile` 仍返回 `nil`。
- 指纹 transport 本身也跑不了 h2：`ForceAttemptHTTP2: false` + 未调用 `http2.ConfigureTransports`，且默认 profile 的 ALPN 只有 `http/1.1`（[http_upstream.go:1479](backend/internal/repository/http_upstream.go#L1479)、[dialer.go:28](backend/internal/pkg/tlsfingerprint/dialer.go#L28)、[dialer.go:55](backend/internal/pkg/tlsfingerprint/dialer.go#L55)）。

结果：上游看到 **Go 的 JA3/JA4 + Go 的 h2 帧**，头里却写着 `codex_cli_rs/0.146.0`。

#### 为什么这样实现
有两个层次的方案，先做 A 止血，终态走 B。

**方案 A（工程内改动，1~2 天）**

1. 解除 Anthropic 限制，新增 `IsOpenAICodexTLSFingerprintEnabled()`，只对 `PlatformOpenAI && (OAuth || SetupToken)` 返回 true。
2. 在 [tlsfingerprint](backend/internal/pkg/tlsfingerprint/dialer.go) 增加 `rustls` 内置 profile：TLS1.3 优先、**不带 GREASE**、曲线 `x25519 → secp256r1 → secp384r1`、rustls 的 sigalgs 顺序、扩展顺序、**ALPN = `["h2","http/1.1"]`**。
3. 指纹 transport 打开 h2：

```go
transport := &http.Transport{
    MaxIdleConns:        settings.maxIdleConns,
    MaxIdleConnsPerHost: settings.maxIdleConnsPerHost,
    MaxConnsPerHost:     settings.maxConnsPerHost,
    IdleConnTimeout:     settings.idleConnTimeout,
    DialTLSContext:      dialer.DialTLSContext, // utls，ALPN 含 h2
}
// 注意：设置了 DialTLSContext 时，net/http 不会自动启用 h2，
// ForceAttemptHTTP2 也只对内置 dialer 生效。必须显式：
//   1) http2.ConfigureTransports(transport) 注册 TLSNextProto["h2"]
//   2) DialTLSContext 返回的 conn 的 ConnectionState().NegotiatedProtocol == "h2"
if _, err := http2.ConfigureTransports(transport); err != nil { return nil, err }
```

> h2 帧参数（SETTINGS 初值、窗口）由 `x/net/http2` 固定，无法完全复刻 rust `h2`；方案 A 只能把"协议一致 + 关键帧接近"做到位，**帧级完全一致必须走方案 B**。

4. 用项目已有的 capture 测试把 profile 校准到与真实 `codex-rs` 抓包一致（[dialer_capture_test.go](backend/internal/pkg/tlsfingerprint/dialer_capture_test.go)）。

> 为什么不是"随便找个浏览器 profile"：ALPN/扩展顺序/CBC 套件/ GREASE 一改，JA4 就变成了另一个产品；用 Node.js 24（Claude Code）profile 配 `codex_cli_rs` UA，等于自报两个身份。

**方案 B（终态，推荐）：用 codex-rs 自己做出站**

把 `openai/codex` 的 `codex-rs`（`core`/`login` 相关 crate）以 lib 形式 vendor 进来，编译一个只做 HTTP 出口的 sidecar：

```
gateway ──UDS/HTTP──> codex-egress ──rustls+h2──> chatgpt.com/backend-api/codex/responses
```

- 为什么最保真：TLS、h2 帧、头顺序、`accept-encoding`、UA 全部由**官方同一份源码**生成，不需要逐字段追上游改动；上游一升级客户端，跟着升 crate 版本即可。
- 为什么可接受：网关仍掌握排队、并发、计费、重试、SSE 解析，sidecar 只替换"最底层那一段 socket"。
- 代价：多一个进程/容器，需要维护 pin 的版本；二进制体积与发布流程变复杂。

#### 改哪里
| 文件 | 改动 |
|---|---|
| [account.go:2516](backend/internal/service/account.go#L2516) | 增加 OpenAI OAuth/SetupToken 分支 |
| [tls_fingerprint_profile_service.go:177](backend/internal/service/tls_fingerprint_profile_service.go#L177) | 同上，按平台返回 profile |
| [dialer.go](backend/internal/pkg/tlsfingerprint/dialer.go) | 新增 rustls profile 常量与默认扩展顺序 |
| [http_upstream.go:1471](backend/internal/repository/http_upstream.go#L1471) | 指纹 transport 打开 h2（方案 A） |
| 新增 `internal/pkg/codexegress/` | sidecar 客户端与协议（方案 B） |

#### 验收
- 同一请求分别经本地官方客户端与网关发出，抓包 diff：JA4 一致、h2 SETTINGS 关键字段接近、`accept-encoding` 一致。
- 线上对比：开启前后 `server_is_overloaded` 率、429 率。

#### 回滚
- 配置开关 `gateway.tls_fingerprint.enabled`（默认 true，[config.go:2707](backend/internal/config/config.go#L2707)）+ 账号级 `enable_tls_fingerprint`。
- 方案 B 保留 sidecar 直连回退（`codex_egress_mode: local|sidecar`）。

---

### P0-2 设备数：优先"一人一号"，共享账号只做保守收敛

#### 问题
一个 OAuth 账号被 N 个下游用户使用 → 上游看到 N 个 `installation_id`、N 组 session/thread。任何真实用户都不长这样。收敛模式（[openai_codex_fingerprint.go:60](backend/internal/service/openai_codex_fingerprint.go#L60)）默认 `off`（[:194](backend/internal/service/openai_codex_fingerprint.go#L194)），因为历史上强制收敛触发过额度缩水（代码注释里的 #5553/#5555）。

#### 为什么这样实现
上游把"设备数/会话数"计入判定与配额，而**收敛会改变上游看到的设备语义**，两种策略互相冲突：

- 不收敛 → 设备多，像"共享号"。
- 全收敛（`full`）→ 1 设备 1 会话 1 线程，但所有用户的 thread/window 全撞在一起，和真实 Codex 的"1 设备 N 线程"也不一样，且实测掉额度。

所以正确顺序是：

1. **结构性消除**：账号分配按"一个下游用户一个 OAuth 账号"（或一个用户一个小池），从根上让 `installation` 数与真人一致。
2. 必须共享时，只用 `device` 模式（只收敛 `installation_id`，session/thread 保留真实多样性），它最接近"一台机器开多个会话"。
3. `session` / `full` 仅作为实验分支，小流量灰度，盯 5h/7d 消耗比。

#### 改哪里
| 文件 | 改动 |
|---|---|
| 账号分配/分组逻辑 | 增加"账号-用户绑定"策略（`account_binding: dedicated|shared`） |
| [openai_codex_fingerprint.go:282](backend/internal/service/openai_codex_fingerprint.go#L282) | 共享账号默认 mode 由 `off` 改为可配置默认 `device`（按组） |
| 管理端 | 账号卡片显示"当前绑定用户数 / 可见 installation 数" |

#### 验收
- 单账号维度统计"上游可见 installation_id 数量"，目标 ≤ 2。
- 灰度组 vs 对照组的 5h/7d 消耗与 429。

#### 回滚
- `codex_fingerprint_mode: off`；`disable_codex_identity_enforcement`（[config.go:1079](backend/internal/config/config.go#L1079)）。

---

### P0-3 OAuth 池只收官方客户端

#### 问题
只要池子里混进 opencode / Claude Code / chat-completions 兼容请求，同一账号下就同时存在两种体系：一种带 `x-codex-*` 与 Codex body，一种连 `originator` / `OpenAI-Beta` 都被删掉（[openai_gateway_forward.go:1503](backend/internal/service/openai_gateway_forward.go#L1503)）。**body 形态是上游最难伪造、也最容易用来分类的信号**，一旦混池，前面所有 header 对齐都白做。

#### 为什么这样实现
不是"想办法把 opencode 的 body 改成 Codex 形态"——那要复刻 instructions、tools、`input` 结构、`client_metadata`，维护成本高且必然滞后。更稳的是**不让它进这个池**：

- `codex_cli_only`（[account.go:2473](backend/internal/service/account.go#L2473)）已经实现"双因子"准入：`originator` + UA 特征 + 引擎指纹（默认要求 `x-codex-` 前缀，[engine_fingerprint_signal.go:29](backend/internal/pkg/openai/engine_fingerprint_signal.go#L29)）。
- 非 Codex 客户端走 API Key 池 / 兼容池，形态本来就该是第三方协议，混进来才是异常。

#### 改哪里
| 文件 | 改动 |
|---|---|
| 账号配置 | Codex OAuth 账号默认 `codex_cli_only: true` |
| 分组路由 | 新增"协议亲和"：Codex 协议请求只能命中 `codex_cli_only` 账号；其他协议请求禁止命中 |
| [openai_gateway_forward.go:1503](backend/internal/service/openai_gateway_forward.go#L1503) | compat 桥请求显式标记，命中即拒绝进入 OAuth 池 |

#### 验收
- 按账号统计"非 Codex 引擎指纹请求占比"，目标 0。
- 拒绝日志（已有 `logCodexCLIOnlyDetection`）归零。

#### 回滚
- 账号级 `codex_cli_only: false`（逐个回退即可）。

---

### P1-1 IP / 时区 / 语言 / UA 平台段四件套一致

#### 问题
- 时区被改写成账号级 `Asia/Singapore`（[openai_request_timezone.go:14](backend/internal/service/openai_request_timezone.go#L14)）。
- `Accept-Language` **源码里只有透传，没有统一改写**（[openai_gateway_service.go:74](backend/internal/service/openai_gateway_service.go#L74) 只是白名单）；README 声称会统一为 `en-US,en;q=0.9`（[README.md:81](README.md#L81)），与当前 dev 分支不一致——线上镜像与源码可能不同，需要先核实。**（2026-11 已实施：`gateway.openai_accept_language`，默认空=关闭；仅在客户端已声明该头时改写；HTTP / 透传 / WS 三条出站路径均已接线）**
- UA 平台段固定为 `(Ubuntu 22.4.0; x86_64) xterm-256color`（[openai_gateway_service.go:40](backend/internal/service/openai_gateway_service.go#L40)），而 `environment_context` 的 `cwd`/`shell` 原样透传：Windows 客户端（`C:\...` + PowerShell）配 Ubuntu UA，是明显的交叉矛盾。

#### 为什么这样实现
上游做交叉校验时，最省力的规则就是"IP 地区 ↔ 时区 ↔ 语言 ↔ OS/终端"。四项里任意一项偏离，都会被记为"环境异常"。所以要么四项一起对齐到"一个人"，要么全部保持真实透传——**不能只改一项**。

- 语言：只在客户端**已带** `Accept-Language` 时改写（与现有时区策略一致：不主动添加不存在的头，避免制造"多出来的信号"）。取值按账号出口国别。
- 平台段：不要全局固定 Ubuntu。给账号一个 `client_profile`（os / os_version / arch / terminal），UA 平台段与 `environment_context` 的 `shell` 必须同族；不同族的请求路由到对应画像的账号，或直接拒绝进入该账号。

#### 改哪里
| 文件 | 改动 |
|---|---|
| [openai_gateway_forward.go:1479](backend/internal/service/openai_gateway_forward.go#L1479) 之后 | 增加 `normalizeOpenAIRequestLanguage()`，按账号地区取值 |
| [openai_codex_identity.go:37](backend/internal/service/openai_codex_identity.go#L37) | `buildCodexCLIUserAgent` 的 suffix 改为按账号 `client_profile` 生成 |
| [openai_request_locale.go:99](backend/internal/service/openai_request_locale.go#L99) | 增加 `shell`/`cwd` 与 `client_profile` 的一致性校验（只校验，不改写） |
| 账号 extra | 新增 `client_profile`、`egress_region` |

#### 验收
- 抽样请求做"四件套一致性"检查脚本，冲突率目标 0。
- 线上按账号维度看 `Accept-Language` 分布是否收敛到单一值。

#### 回滚
- 语言改写开关默认关闭；`client_profile` 为空时回退现有固定 suffix。

---

### P1-2 缺失的 Codex 必带头要补齐

#### 问题
真实 Codex 几乎必带 `x-codex-window-id`、`x-codex-installation-id`、`x-codex-turn-metadata`（项目自己的统计：约 98.8%，见 [engine_fingerprint_signal.go:27](backend/internal/pkg/openai/engine_fingerprint_signal.go#L27)）。当前只有客户端带了才透传，探测路径才补（[openai_codex_identity.go:197](backend/internal/service/openai_codex_identity.go#L197)）。于是"UA 是 Codex、头却缺 Codex 引擎指纹"成为最容易命中的规则。

#### 为什么这样实现
补头不等于伪造：这些值本来就该由身份体系派生，且必须与 **body 的 `client_metadata` 用同一份 ID**，否则 flat 与 embedded 身份分裂（项目里已有这个原则，[openai_codex_fingerprint.go:393](backend/internal/service/openai_codex_fingerprint.go#L393)）。

- `window_id` 形态为 `{thread_id}:{window_index}`（[openai_codex_fingerprint.go:313](backend/internal/service/openai_codex_fingerprint.go#L313)），别写成裸 UUID。
- 派生用账号 seed + 客户端 session，保证同一会话多轮稳定、不同会话不同。

#### 改哪里
| 文件 | 改动 |
|---|---|
| [openai_codex_identity.go:197](backend/internal/service/openai_codex_identity.go#L197) | 把"补探针头"逻辑抽成 `ensureCodexEngineHeaders`，正常转发也调用 |
| [openai_codex_fingerprint.go:357](backend/internal/service/openai_codex_fingerprint.go#L357) | 缺省时按 `device` 语义补齐，不改变已有值 |
| 请求体 | 同步补 `client_metadata` 对应字段（复用同一 ids 实例） |

#### 验收
- 出站请求头/体一致性测试：头里的 `installation_id` 必须等于 `client_metadata` 中的同名值。
- 线上统计"缺 `x-codex-` 前缀的出站请求"应为 0。

#### 回滚
- 开关 `codex_engine_header_backfill`，默认关 → 开。

---

### P1-3 打票与探测的节律

#### 问题
`harvest_probe_interval_seconds` 默认 6 秒（[config.go:2536](backend/internal/config/config.go#L2536)），且打票路径使用 `openai_h1_no_reuse` 专门换出口 IP（[http_upstream.go:1417](backend/internal/repository/http_upstream.go#L1417)）。对单个账号来说，这是"每 6 秒一次、每次换 IP"的机器流量，和真人使用曲线完全不同。

#### 为什么这样实现
打票本身是为了拿 `x-codex-turn-state` + cookie（[openai_codex_ticket.go:38](backend/internal/service/openai_codex_ticket.go#L38)），属于"必须做但应尽量隐形"的流量：

- 时间维度：改成按账号随机 30–180 秒，夜间（账号时区 02:00–08:00）降频或暂停。
- 空间维度：同一账号固定一个出口 IP 打票；轮换 IP 只用于"池级别"分摊，不要用于单账号连续请求。
- 量维度：单账号日上限 + 失败指数退避，别把重试也做成高频脉冲。
- 优先级：业务请求到来时优先用现有票据，票据快过期才触发打票，避免"定时脉冲"这种可预测节律。

#### 改哪里
| 文件 | 改动 |
|---|---|
| [config.go:2536](backend/internal/config/config.go#L2536) | 默认值改 `90`，并新增 `jitter_ratio`、`night_window`、`daily_cap` |
| `codex_ticket_cadence.go` | 改为"按需 + 抖动"调度 |
| [http_upstream.go:1417](backend/internal/repository/http_upstream.go#L1417) | 单账号固定 IP 时不使用 `h1_no_reuse` |

#### 验收
- 单账号 24h 请求时间分布直方图，应出现人类作息低谷。
- 打票失败率与票据命中率不退化。

#### 回滚
- 配置回调到固定间隔。

---

### P1-4 `environment_context` 的多机泄漏

#### 问题
设备收敛只改标识类字段：`installation_id` / `session_id` / `thread_id` / `turn_id` / `window_id`（[openai_codex_fingerprint.go:60](backend/internal/service/openai_codex_fingerprint.go#L60)、[:357](backend/internal/service/openai_codex_fingerprint.go#L357)）。而 `environment_context` 里真正指向"哪台机器"的字段——`cwd`、`shell`、`<filesystem>`、`<network>`——**原样透传**：[openai_request_locale.go:99](backend/internal/service/openai_request_locale.go#L99) 只处理 `timezone` / `current_date` / `web_search.user_location.timezone`。

结果：`device` 模式把上游看到的设备**数**压到 1，但同一个 installation 的上下文里仍然依次出现 `/Users/alice/...`、`C:\Users\bob\...`、`/home/ci/...` 和 `zsh` / `powershell` / `bash` 混用。上游只要看 `cwd` 根集合大小，就能还原"这一个 installation 背后有几台机器"，收敛等于只做了一半。

#### 为什么这样实现
不能简单删掉 `cwd`/`shell`：真实 Codex 一定带这两个字段，缺失比不一致更可疑。也不能随手编——`cwd` 参与模型对工作区的理解，乱改会影响结果质量。

正确做法是**"归一 + 准入"**，而不是无脑改写：

1. **归一**：账号配置一个 `client_profile`（os / os_version / arch / terminal / home 形态），`device` 模式下把 `cwd` 的根段与 `shell` 归一到该画像（例如统一为 `/workspace` + `zsh`，或统一为某个 macOS home 前缀）。只改根段与 shell 名，保留子路径结构，避免破坏"同一个项目"的语义连续性。
2. **准入**：平台族不一致的客户端**不要进这个账号**——Windows 客户端（`powershell` / `C:\`）走 Windows 画像账号，macOS/Linux 走各自画像。这比"把 Windows 请求改写成 macOS 上下文"更安全，因为后者会让模型行为和请求内容对不上。
3. **字段取舍**：`<filesystem>` / `<network>` 这类带沙箱语义的块**不改写**（改了容易和实际工具调用矛盾），只做画像一致性校验；不一致就不放行。

#### 改哪里
| 文件 | 改动 |
|---|---|
| [openai_request_locale.go:99](backend/internal/service/openai_request_locale.go#L99) | 新增 `normalizeOpenAIRequestEnvironmentProfile()`，在时区改写之后执行 |
| [openai_codex_fingerprint.go:357](backend/internal/service/openai_codex_fingerprint.go#L357) | `device` 模式下同时触发 `cwd`/`shell` 归一（复用同一份账号画像） |
| 账号 extra | 新增 `client_profile`（os/os_version/arch/terminal/home_prefix） |
| [engine_fingerprint_signal.go:29](backend/internal/pkg/openai/engine_fingerprint_signal.go#L29) | 增加 `body_path` 信号：`environment_context.shell` 必须落在账号画像族内 |

#### 验收
- 单账号维度统计：可见 `cwd` 根集合大小 = 1、`shell` 集合大小 = 1。
- 画像一致性：UA 平台段 ↔ `shell` ↔ `client_profile` 三者一致率 100%。
- 回归：改写后 `cwd` 子路径保持原样，工具调用不出现路径不存在类错误。

#### 回滚
- 开关 `codex_env_profile_normalize`（默认关），逐账号开启。

---

### P1-5 客户端版本的设备级一致性

#### 问题
版本现在做到的是**单请求自洽**，不是**单设备恒定**：

- 打票路径遇到 `gpt-6*` / `astra` 类模型，会把 `version` 与 UA 抬到 `0.153.4`（[openai_codex_ticket.go:485](backend/internal/service/openai_codex_ticket.go#L485)、[:496](backend/internal/service/openai_codex_ticket.go#L496)）。
- 业务转发路径用规范版本（当前 `0.146.0`），且强制覆盖（[openai_codex_identity.go:225](backend/internal/service/openai_codex_identity.go#L225)、[openai_gateway_service.go:64](backend/internal/service/openai_gateway_service.go#L64)）。

于是同一个账号、同一个 `installation_id`，会在几分钟内交替声明 `0.146.0` 和 `0.153.4` 两种客户端身份。真实设备不会这样——版本是**设备的属性**，不是**请求的属性**。

**（2026-11 已实施：打票不再按模型抬版本，改为账号级统一版本 + 低于模型下限时只告警不改头；`overrideUA` 也已与业务路径同源。）**

#### 为什么这样实现
把版本从"按请求决定"改成"按账号决定"，并且引入一个之前没写明的约束：**版本上限 = 我们能完整仿真其线型的最高版本**。

- 版本抬得越高，该版本会带的头/字段越多（新特性、新 `x-codex-*`、新 `client_metadata` 字段）。声称 `0.153.4` 却发不出 0.153.4 应有的形态，比声称旧版本更糟。
- 所以账号级版本取 `max(规范版本, 该账号启用模型所需的最高下限)`，但**不得超过"形态仿真上限"**（由 P1-2 的补头能力决定）。超出时不提升版本，而是把该模型从该账号的可用模型里摘掉。
- 打票路径不再自行改版本，改为读取账号级版本；harvest 与业务请求必须用同一个值。

这样做的另一个收益：版本变成账号维度的常量，可以直接对账——"这个账号声明的版本"和"这个账号发出的每一条请求"必须逐字相等。

#### 改哪里
| 文件 | 改动 |
|---|---|
| 新增 `resolveAccountCodexClientVersion(account)` | 返回账号级版本，含模型下限与仿真上限的夹逼 |
| [openai_codex_identity.go:225](backend/internal/service/openai_codex_identity.go#L225) | `enforceCodexIdentityHeadersWithUA` 改用账号级版本 |
| [openai_codex_ticket.go:485](backend/internal/service/openai_codex_ticket.go#L485) | 删除按模型抬版本的分支，改为读取账号级版本 |
| 账号调度 | 模型下限 > 仿真上限时，把该模型从账号的可用模型里去掉（而不是发出去） |

#### 验收
- 单账号 24h 内出站 `version` 取值集合大小 = 1。
- `User-Agent` 的版本段与 `version` 头逐字一致率 100%（含打票、探测、降智检测等所有出站路径）。

#### 回滚
- 开关 `codex_version_per_account`，关闭后退回现行为（规范版本 + harvest 特判）。

---

### P2-1 并发与额度曲线

#### 问题
聚合流量会把 5h/7d 窗口跑满，且没有昼夜曲线。上游看到"一个账号 24 小时满负荷、并发恒定"。

#### 为什么这样实现
配额判定与"使用强度"直接相关，而真实用户的使用曲线有强节律。项目已有窗口成本模型（`window_cost_limit` / `sticky_reserve`，[openai_account_scheduler.go](backend/internal/service/openai_account_scheduler.go)），可以直接复用作为"软刹车"。

建议参数：单账号并发 2–3；RPM 留 20–30% 缓冲；5h/7d 目标占用 50–70%，接近 80% 时只允许粘性会话（`WindowCostStickyOnly`）；凌晨时段降低调度权重。

#### 验收
- 按账号统计 5h/7d used% 曲线与并发分布。
- 429 与 `server_is_overloaded` 比例下降。

---

### P2-2 失败转移策略

#### 问题
"失败即换号"会让同一 session 的 thread/window 在账号间跳动。虽然已剥离跨账号 `turn-state`（[openai_gateway_forward.go:1490](backend/internal/service/openai_gateway_forward.go#L1490)），但上游仍能看到"同一用户会话在不同账号上出现"。

#### 为什么这样实现
- 同号退避重试优先（真实客户端就是这么做的：失败重试同一账号）。
- 跨号 failover 只对"账号级"错误（401/403、额度耗尽、账号被限），不对"请求级"错误（超时、单次 5xx）。
- 粘性绑定 TTL 拉长（现有 `openaiStickySessionTTL` 1 小时，[openai_gateway_service.go:35](backend/internal/service/openai_gateway_service.go#L35)），减少会话漂移。

---

### P2-3 版本跟随与下限监控

`codexCLIVersion` 是编译期常量 `0.146.0`（[openai_gateway_service.go:64](backend/internal/service/openai_gateway_service.go#L64)），落后会被上游归入"陈旧客户端"一侧。已有 `openai_codex_client_version_synced` 自动同步（[setting_handler_update.go:1474](backend/internal/handler/admin/setting_handler_update.go#L1474)）。

本项只管"跟随"，不管"一致"（一致性见 P1-5）：

- 自动同步常态开启，UA 与 `version` 同源（已实现）。
- 监控 `codexUpstreamMinVersion`（[openai_codex_identity.go:16](backend/internal/service/openai_codex_identity.go#L16)）上调，一旦上游抬高下限立即告警。
- 同步更新"形态仿真上限"配置：只有确认新版新增的头/字段都能复刻，才把它纳入可选版本。

---

## 3. 不建议做的事（同样重要）

| 反模式 | 为什么 |
|---|---|
| 全量 `full` 收敛 | 实测触发额度缩水（#5553/#5555），且与"1 设备 N 线程"的真实形态不一致 |
| 用浏览器 / Node.js TLS profile 配 Codex UA | JA4 与 UA 两个身份互相矛盾，比不伪装更容易被标记 |
| 同一账号轮换出口 IP | 真实设备不会每隔几分钟换公网 IP；按账号固定 IP 才是"一个人" |
| 全局把 UA 平台段钉成 Ubuntu | Windows/macOS 客户端的 `environment_context`（cwd/shell）会与 UA 打架 |
| 给同一账号在多种 UA 平台段之间随机跳 | 设备画像抖动比固定单一画像更可疑 |
| 为省 token 改写 `instructions` | instructions 文本是 codex-rs 版本的一部分，与 UA 版本不一致即为矛盾；只在 compat/probe 路径使用固定 prompt（现状如此，保持） |
| 把非 Codex 请求"改造成" Codex 形态 | 维护成本高且必然滞后；正确做法是分池 |
| 按请求分支改 `version` / UA | 版本是设备属性；同一 installation 几分钟内换版本，比版本旧更可疑（P1-5） |
| 只收敛 ID 不看 `cwd` / `shell` | 收敛了 `installation_id`，仍能从环境上下文数出机器数，等于只做一半（P1-4） |

---

## 4. 实施顺序与灰度

| 阶段 | 内容 | 观察指标 | 回滚开关 |
|---|---|---|---|
| 0 基线 | 抓本地官方客户端指纹，建立 diff 工具 | 一致性测试通过率 | — |
| 1 | P0-3 池子准入（纯路由，风险最低） | 非 Codex 指纹占比 → 0 | 账号 `codex_cli_only: false` |
| 2 | P0-1 方案 A（uTLS + h2） | JA4/h2 一致性、`server_is_overloaded` 率 | `enable_tls_fingerprint` |
| 3 | P1-2 缺省头补齐、P1-1 四件套、**P1-5 版本账号级恒定** | 头/体一致性、语言分布、单账号 version 集合大小 = 1 | `codex_engine_header_backfill`、语言开关、`codex_version_per_account` |
| 4 | P0-2 账号分配 + `device` 收敛灰度、**P1-4 环境上下文归一** | 每账号 installation 数、`cwd`/`shell` 集合大小 | `codex_fingerprint_mode: off`、`codex_env_profile_normalize` |
| 5 | P1-3 节律、P2-* 并发与失败策略 | 24h 分布、429 率 | 配置回退 |
| 6（终态） | P0-1 方案 B（codex-rs sidecar） | 指纹完全一致 | `codex_egress_mode: local` |

**原则**：每一阶段只改一层，且必须有可量化的对照指标；不要把"传输层 + 身份层 + 节律"一起上，否则无法归因。

---

## 5. 验收方法

### 5.1 指纹基线 diff
1. 本地真实 Codex Desktop 抓包（mitm），记录：JA4、h2 SETTINGS、头集合与顺序、`accept-encoding`、body 关键字段、连接复用行为、时序。
2. 网关侧用同一份请求体打自建指纹接收端（可复用 [dialer_capture_test.go](backend/internal/pkg/tlsfingerprint/dialer_capture_test.go) 的脚手架）。
3. 逐字段 diff，输出"一致/不一致/不可比"三类；不可比项要显式记录（例如上游私有字段）。

### 5.2 线上指标
- `server_is_overloaded` 比例、429 比例、403 strike 计数。
- 单账号上游可见 installation 数、session 数。
- 5h/7d used% 曲线与昼夜分布。
- 封号/停用率（按周）。

> 身份唯一性、人类上限、出口属性这三组指标的完整口径见 [6.5](#65-还剩多少的可操作口径)，避免在两处重复定义。

### 5.3 回归
- 头/体一致性单测：`x-codex-installation-id` 与 `client_metadata.x-codex-installation-id` 必须相等。
- 时区/语言改写幂等性测试（重复处理不产生二次改写）。

---

## 6. 改完之后的残余差距

把所有 P0/P1/P2 都做完，差距**不会归零**。这一节把"还能不能被区分"讲清楚，避免把"技术指纹对齐"误当成"安全"。

### 6.1 记分卡（全部改造完成后）

三档定义：**A** = 无法区分；**B** = 需要更贵的信号才能区分；**C** = 区分成本很低。

| 层 | 状态 | 上游还剩什么信号 | 能否消除 |
|---|---|---|---|
| TLS / JA3 / JA4 | **A** | 无（sidecar 与官方同 crate、同 ClientHello） | 能（方案 B） |
| HTTP/2 帧 | **A**（方案 B）/ **B**（方案 A） | SETTINGS 初值、窗口增量；`x/net/http2` 固定不可配 | 只有方案 B 能闭合 |
| 请求头集合 / 顺序 | **A** | 无（P1-2 补齐后与官方一致） | 能 |
| 请求体形态 | **A**（Codex 池内） | 混池即暴露（已被 P0-3 挡住） | 能（靠分池） |
| 客户端版本 | **A**（P1-5 后） | 无（账号级恒定，UA 与 `version` 逐字一致） | 能 |
| 设备身份 / 会话数 | **B** | `cwd`/`shell` 剩余差异（P1-4 归零后基本消失）、会话并发数、turn 间隔分布 | 部分 |
| 网络属性 | **C** | 出口 ASN 是机房/代理；TCP/IP 栈（TTL/窗口/时间戳）是 Linux server | 只能靠住宅代理掩盖，成本高 |
| 并发 / 节律 | **C** | 单 installation 同时多条独立会话、24h 无低谷、额度精准打满 | 部分（并发上限 + 曲线整形） |
| 内容 / 任务 | **C** | 同一批客户、同一批模板，跨账号高度相似；内容语言与出口地区不匹配 | **不可消除**（业务本质） |
| 账号主体 | **C** | 注册环境、历史设备、支付、使用起点强度 | 部分，取决于号源 |

### 6.2 三类"改完也消不掉"的东西

1. **一台设备后面有 N 个人。** 收敛只能让上游**看到更少**的设备，不能让它们**不存在**；而且收敛本身有反作用——项目注释已记录收敛触发额度缩水的实证（[openai_codex_fingerprint.go:199](backend/internal/service/openai_codex_fingerprint.go#L199)），说明上游对设备/会话语义的判定与配额挂钩。真正的消除只有"一人一号"，那是资源成本问题，不是技术问题。
2. **机房出口 + 服务器 TCP/IP 栈。** 即使 TLS 层逐字节同构，`p0f` 级别看 SYN 的 TTL/窗口/时间戳就知道对面是 Linux 服务器。住宅代理能掩盖 IP 属性，但会引入新的延迟/jitter 特征，且代理池本身被标记的概率不低。这层只能"降低"，不能"归零"。
3. **转售工作负载。** 指纹是**单请求**特征，成本低但可被工程手段抹平；工作负载是**跨请求、跨账号**的统计特征——同一批 prompt 模板、相似 token 分布、中文内容 + 新加坡 IP、多个账号同时段做同类任务。抹平这一层等于改变业务本身。

### 6.3 反直觉：伪装越完整，剩余异常越突出

一旦上游相信"这是 `codex_cli_rs/0.146.0` 的一台 Ubuntu 机器"，那么"这台机器同时开 8 条独立会话、24 小时不停、每 5 小时精准打满额度"就变成**该可信身份下的异常行为**，比"UA 有点怪"更容易被单独拎出来。

**结论：传输层（P0-1）做完之后，P1-3 节律与 P2-1 并发/额度必须提前，不能当收尾工作。**

### 6.4 风险结构会怎么变（推断，非实测）

- **改前**：风险大头是低成本规则——UA/TLS 不匹配、头缺引擎指纹、一账号 N 设备。这类规则便宜、可批量跑，通常也是批量清理的触发条件。
- **改后**：这部分基本归零，风险转移到**行为/内容聚类**这类模型驱动判定：误判率高、算力成本高，通常只对"规模化聚合"动刀。

净效果：**从"被规则命中"变成"被模型打分"**。风险曲线明显下降，但不是零。

### 6.5 "还剩多少"的可操作口径

不要用主观判断，用三个可测指标，改造前后各跑一次：

| 指标 | 口径 | 目标 |
|---|---|---|
| 身份唯一性 | 单账号 24h 内：`installation_id` 集合、`cwd` 根集合、`shell` 集合、`version` 集合的大小 | 均 = 1 |
| 人类上限 | 单账号 24h：并发流峰值、5h/7d used% 峰值、活跃小时数 | 并发 ≤ 3、used% ≤ 70%、活跃 ≤ 14h |
| 出口属性 | 出口 ASN 类型、同 IP 账号数、IP 连续存活时长 | 住宅类、= 1、≥ 24h |

这三组指标跑出来，"离真实还差多少"就从感觉变成了数字。

---

## 7. 风险与边界

1. **上游判定不可观测。** 以上所有改动都是"减少可观测差异"，不构成"不会被判定"的保证；项目里已有收敛反而掉额度的实证，说明上游策略存在与直觉相反的分支。
2. **源码与线上镜像可能不一致。** README 描述的 `Accept-Language` 统一在当前 dev 分支没有实现；`plugins/codex-bridge` 只有二进制没有源码，且 `plugin_manager.RoundTripOpenAIOAuth` 可能接管真实出口（[plugin_manager.go:947](backend/internal/service/plugin_manager.go#L947)）。**开工前必须先确认线上到底由谁发请求**，否则 P0-1 的结论可能被推翻。
3. **合规与账号风险自负。** 本文只从"技术指纹一致性"角度给方案；是否使用官方 OAuth 账号做聚合转发，属于运营决策。
4. **改动的可逆性。** 每一项都要求有独立开关；没有开关的改动不进灰度。

---

## 8. 实施状态（截至 2026-11）

| 项 | 状态 | 说明 |
|---|---|---|
| P1-5 版本账号级恒定 | **已实施** | 打票不再按模型抬 `version`/UA/originator；统一走规范身份；`overrideUA` 与业务路径同源 |
| 版本下限（astra） | **已实施** | 新增 `gateway.codex_min_client_version`；未配置时按打票模型自动推导（含 astra → 0.153.4），保证打票/业务/降级模式只声明一个版本；预发布形态被拒绝并告警 |
| P1-1 Accept-Language 归一 | **已实施（默认关闭）** | 新增 `gateway.openai_accept_language`；仅在客户端已声明该头时改写；HTTP / 透传 / WS 三条出站路径均已接线；非法值（CR/LF、超长、非白名单字符）在配置校验阶段拒绝 |
| 出口区域预设（jp/sg） | **已实施（默认关闭）** | 新增 `gateway.openai_egress_region`；`jp` → 默认时区 `Asia/Tokyo` + `ja-JP,ja;q=0.9,en-US;q=0.6,en;q=0.5`，`sg` → `Asia/Singapore` + `en-US,en;q=0.9`；显式 `openai_default_timezone` / `openai_accept_language` 优先 |
| P0-3 池子准入 | **已实施（默认关闭）** | `codex_cli_only` 账号级三态 + 全局默认 `gateway.openai_codex_require_official_client`；显式 `false` 不被全局默认覆盖。默认只覆盖 `/responses` 与 `/chat/completions`；打开 `gateway.openai_codex_gate_extra_paths` 后追加覆盖 images / embeddings / `responses/input_tokens` / `messages(count_tokens)` / WS 直连（见 8.1） |
| 策略拒绝不计账号健康 | **已实施** | 主路径返回哨兵错误 ErrCodexClientRestriction；responses/chat 两条路径不再把它报成账号失败，handler 也不会在其后追加 SSE |
| profile 生成器 | **已实施** | fpcap profile 从抓包直接生成 tlsfingerprint.Profile（剔 GREASE、解析版本、未知扩展写 notes）；尚未接线到 OpenAI 账号 |
| 打票节律整形 | **已实施** | 扫描间隔加入 ±30% 抖动（打散 6s 机械节奏）；夜间 23:00–07:00（进程默认时区）乘以 harvest_night_multiplier（默认 1=关闭）；计划重试仍取精确值，不受抖动影响 |
| models 清单指纹 | **已实施（默认关闭）** | httpclient.Options 新增 TLSProfile：utls + 强制 h1，按 profile 摘要隔离缓存；https 代理回退不伪装。models manifest 已接线 |
| ticket 收割指纹 | **已实施（默认关闭）** | 打票与业务同打 /codex/responses：开启指纹后走同一套 utls+h1；req.Close=true 保证每次新连接 |
| OpenAI WS 指纹 | **已实施（默认关闭）** | WS 建连经 dialOpenAIWSClient：账号开 enable_tls_fingerprint 时用 utls + h1 握手（无 ALPN）；http/socks5 代理走 CONNECT+utls，https 代理回落不伪装；按 profile 摘要隔离客户端缓存 |
| OpenAI TLS 指纹接入 | **已实施（默认关闭）** | 账号 enable_tls_fingerprint=true 时，OpenAI HTTP 出站走 DoWithTLS + CodexRustlsProfile（与官方 Linux 客户端 JA3/MD5 一致，无 ALPN，即 HTTP/1.1）；TLS 客户端缓存键已拼 profile 摘要，isolation=proxy 下不会与 Anthropic 串用；插件接管时会告警指纹未生效；WS 直连路径尚未接入 |
| 指纹基线工具 | **已实施** | 新增 `backend/cmd/fpcap`：`peek`（明文抓 ClientHello，给 JA3/ALPN/SNI，无需信任证书）、`serve`（自带证书终止 TLS，抓 h2 SETTINGS 与 HPACK 头顺序 / h1 原始头顺序）、`compare`（两份基线逐字段对比，volatile 头按存在性比较） |
| 并发整形（最小版） | **已实施（默认关闭）** | 新增 `gateway.openai_pacing_max_account_concurrency`；覆盖 advanced scheduler、legacy 调度与 WS 绑定的抢槽/负载批次；默认 0 = 与改动前逐值等价 |
| P0-1 传输层（uTLS + h2） | 未实施，待基线抓包 | 仓库内没有官方 `rustls` ClientHello 基线，只有 Node.js 24（Claude Code）profile；在没有抓包证据前不臆造 profile。h2 over utls 也一并推迟（需要真实上游做端到端验证） |
| P1-2 缺省引擎头补齐 | 未实施 | 需与账号 seed / 会话派生联动，单独切片 |
| P1-4 `cwd`/`shell` 归一 | 未实施 | 涉及 prompt 语义与工具调用一致性，需灰度 |
| P0-2 一人一号 / 收敛 | 未实施 | 运营策略；`device` 收敛有掉额度先例，需 A/B |
| P1-3 / P2-1 / P2-2 | 未实施 | 节律、并发与失败转移整形 |

已实施部分的行为变化只有三处，且都可回滚：

1. 打票请求的 `version` / User-Agent 不再按模型单独抬高（与业务路径统一，且在配置了 astra 模型时由进程级下限统一抬到 0.153.4）。
2. 配置了 `gateway.openai_accept_language` 或 `gateway.openai_egress_region` 时，已带 `accept-language` 的出站请求会被改写（默认空串 = 不改变任何行为）。
3. 配置了 `gateway.codex_min_client_version` 或发现打票模型含 astra 时，出站 `version`/UA 会被夹逼到下限（默认无 astra 模型且未配置时不改变行为）。

日本出口的最小配置：

```yaml
gateway:
  openai_egress_region: "jp"          # 默认时区 Asia/Tokyo + ja-JP 语言偏好
  # codex_min_client_version: "0.153.4"  # 可省略：打票模型含 astra 时自动取 0.153.4
```

准入与并发（可选）：

```yaml
gateway:
  openai_codex_require_official_client: true   # 默认值：全局要求官方 Codex 客户端
  openai_pacing_max_account_concurrency: 3     # 单账号调度并发上限，0 = 不限
```

### 8.1 已知缺口（准入门）

`codex_cli_only` 默认只在 `/v1/responses`（含 compact / passthrough / WS-upstream 分支）与 `/v1/chat/completions` 两处入站生效。

打开 `gateway.openai_codex_gate_extra_paths` 后，判定会追加覆盖以下"自建上游请求"的入口（复用同一套 detector / 日志 / 403 文案）：

- WS 直连：`openai_ws_forwarder_ingress.go`。注意：WS 在账号调度之前就已 Upgrade，**无法再返回 HTTP 403**，因此拒绝表现为关闭帧（policy violation，文案 `This account only allows Codex official clients`），不是 JSON 错误。
- `/v1/images/*`、`/v1/embeddings`、`/v1/responses/input_tokens`、`/v1/messages`（Anthropic 兼容，含其 count_tokens）：返回与主路径一致的 403 JSON。

仍然**不覆盖** `/v1/models`：它是纯元数据、且官方客户端刷新模型列表时未必带 `x-codex-*` 指纹头，拦截收益低而误伤风险高。

打开前必须在预发确认一件事：**官方 Codex 客户端在这些入口是否携带 `x-codex-*` 头**。默认引擎指纹门要求该前缀；若官方客户端在 `input_tokens` 等请求上不带，开了会误杀真实流量。默认 false 就是为了避免这个风险。

另外，被拒请求当前会以账号失败计入调度 EWMA，可能影响粘性逃逸与评分，建议后续把"策略拒绝"从账号健康里剥离。

注意：以上快照在装配时发布，**改配置需要重启进程生效**；环境变量形式（`GATEWAY_OPENAI_EGRESS_REGION` 等）已注册默认值，可正常覆盖。

---

## 9. 剩余工作与依赖（截至 2026-11）

### 9.1 阻塞项：需要先采集基线

| 项 | 依赖 | 说明 |
|---|---|---|
| P0-1 传输层 profile + h2 | 官方客户端 `tls.jsonl`（≥5 次） | 用 `cmd/fpcap` 采集，见 [FPCAP_RUNBOOK.md](docs/FPCAP_RUNBOOK.md)。没有基线不写 profile |

### 9.2 代码侧待做（按收益/风险排序）

| # | 项 | 依赖 | 风险 | 收益 |
|---|---|---|---|---|
| 1 | 准入补覆盖：WS 直连 / images / embeddings / count_tokens / messages | **已实施**（`openai_codex_gate_extra_paths`，默认关） | 低 | 打开后 `openai_codex_require_official_client` 才接近"边界"；`/v1/models` 仍不覆盖 |
| 2 | 策略拒绝不计入账号健康（EWMA / 粘性逃逸） | 无 | 低 | 避免非官方客户端把健康号打低 |
| 3 | models-sync / usage / alpha-search 的 `version` 直写路径纳入 floor | 无 | 低 | 全链路单版本 |
| 4 | 缺省 Codex 引擎头补齐（installation/window/turn-metadata） | 与账号 seed 联动 | 中 | 声称是 Codex 就把头带齐 |
| 5 | `cwd`/`shell` 归一 + 平台画像一致（P1-4） | 账号 `client_profile` | 中（影响 prompt 语义） | 消除"一个 installation 多台机器" |
| 6 | 一人一号 / `device` 收敛灰度（P0-2） | 运营分配策略 | 中（有掉额度先例） | 结构性消除多设备 |
| 7 | 节律整形（夜间降频、RPM 缓冲、额度余量） | 无 | 中 | 把"完美身份 + 24h 满负荷"的异常压下去 |
| 8 | 管理面板：全局门 / 区域 / floor 可配 | 无 | 低 | 目前只能改 YAML + 重启 |
| 9 | 测试基建：TTFT 计时用例隔离、config 测试屏蔽 `/app/data` | 无 | 低 | 去掉 flaky 与环境性失败 |

### 9.3 只能靠运营，不靠代码

- 出口：每账号固定住宅 IP、与地区/时区/语言一致。
- 账号分配：能一人一号就不要多人共享。
- 配额：5h/7d 常态占用控制在 50–70%，别打满。
- 打票：`harvest_probe_interval_seconds` 从 6 调到 60–120（配置即可，代码默认未改）。

### 9.4 明确不做

- **内容/任务聚类**：同一批客户、同一批模板，跨账号高度相似。这是业务本质，指纹层无法解决。
- **TCP/IP 栈伪装**：机房出口的 SYN 特征只能靠住宅代理链掩盖，成本高且不彻底。

---

## 附录 A：关键参数建议

| 参数 | 当前 | 建议 |
|---|---|---|
| `gateway.tls_fingerprint.enabled` | true（默认，但 OpenAI 不生效） | OpenAI 生效，profile = rustls |
| `codex_fingerprint_mode` | off | 共享账号 `device`；专用账号 `off` |
| `harvest_probe_interval_seconds` | 6 | 90 + jitter(0.5)，夜间 ×3 |
| 单账号并发 | 账号配置（常见偏高） | 2–3 |
| 5h/7d 目标占用 | 跑满 | 50–70%，>80% 仅粘性 |
| UA 平台段 | 固定 Ubuntu | 按账号 `client_profile` |
| `Accept-Language` | 透传 | 按 `egress_region` 归一 |
| 客户端版本 | 规范常量 + harvest 按模型抬升 | 账号级恒定：`max(规范, 模型下限)`，且 ≤ 形态仿真上限 |
| `environment_context` 的 `cwd`/`shell` | 原样透传 | `device` 模式下归一到 `client_profile` |

## 附录 B：代码索引

- 出口与传输：[openai_plugin_transport.go](backend/internal/service/openai_plugin_transport.go)、[http_upstream.go](backend/internal/repository/http_upstream.go)
- TLS 指纹：[dialer.go](backend/internal/pkg/tlsfingerprint/dialer.go)、[tls_fingerprint_profile_service.go](backend/internal/service/tls_fingerprint_profile_service.go)
- 身份三元组：[openai_codex_identity.go](backend/internal/service/openai_codex_identity.go)
- 设备/会话收敛：[openai_codex_fingerprint.go](backend/internal/service/openai_codex_fingerprint.go)、[openai_codex_account_identity.go](backend/internal/service/openai_codex_account_identity.go)
- 请求构造与头白名单：[openai_gateway_forward.go](backend/internal/service/openai_gateway_forward.go)、[openai_gateway_service.go](backend/internal/service/openai_gateway_service.go)
- 环境上下文改写：[openai_request_locale.go](backend/internal/service/openai_request_locale.go)
- 票据与探测：[openai_codex_ticket.go](backend/internal/service/openai_codex_ticket.go)、[CODEX_PROBE_TEMPLATE.md](docs/CODEX_PROBE_TEMPLATE.md)
- 客户端准入：[allowed_client.go](backend/internal/pkg/openai/allowed_client.go)、[engine_fingerprint_signal.go](backend/internal/pkg/openai/engine_fingerprint_signal.go)

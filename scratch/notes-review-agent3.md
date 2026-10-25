# 集体评审：agent3（角 3 降智/路由）对三份笔记的交叉评审

作者：agent3-routing。只读；只写本文件。评审对象：notes-1-wire（角1）、notes-2-identity（角2）、notes-3-routing（角3/自）。

---

## 1. 三份笔记有无实质矛盾？采信与异议

### 结论：**无实质矛盾**，三份笔记在同一证据底座上收敛，互为补强，共同纠正了上一轮「拿票必须走 WS」的错误。分点：

| 主题 | 角1 | 角2 | 角3 | 一致性 |
|---|---|---|---|---|
| 票在哪下发 | HTTP 响应头（347→142, 780） | 同（139/141 turn 首请求） | 同（142/780, 二手 client-runtime） | ✅ 一致，且共同推翻 notes-binary 旧结论 |
| 票是什么 | （未深入） | per-turn Fernet sticky routing token，非 VIP | 同（路由票据≠质量） | ✅ 一致 |
| 降智信号 | （未涉及） | `safety-buffering` 是独立机制 | `safety-buffering-faster-model: gpt-6-luna` 100% 命中 astra/6.1-sol | ✅ 一致 |
| 打票失效根因 | 形状/身份（OpenAI-Beta 自造、0.146.0、无 tools） | 三重语义错配（获取/使用/形态） | 票≠质量 + 票长 292→780 + 服务端路由决定 | ✅ 互补不冲突 |

### 采信的结论
1. **角1**：票由服务端在 **HTTP 响应头**下发（非 SSE body、非 WS 独占）；探针「HTTP POST 读响应头」方向正确；硬错误 = 自造 `OpenAI-Beta: responses=experimental`（真实客户端 HTTP 路径**不发** OpenAI-Beta，只在 WS 握手发 `responses_websockets=2026-02-06`）。——采信，一手 RVA + 日志证据充分。
2. **角2**：turn-state = **per-turn Fernet sticky routing token**（780=585B=57+528(33块)，非 VIP 凭证、非 gzip）；installation_id 是**设备级随机 UUIDv4 非账号派生**；跨账号回放是网关已写明的「矛盾信号」。——采信，Fernet 布局逐字节数学复核严谨。
3. **角3（自）**：降智权威判据 = **请求模型 vs 流内实际模型**（`response.created.response.model`/`openai-model`）+ `x-codex-safety-buffering-*` 头；`service_tier` 是提速档非质量档；路由/驻留四信号是请求头非 VIP 票。——维持。

### 仍有的异议（1 处事实性瑕疵 + 1 处引用错位）
1. **角2 次选 B 采信了「过时结论」**：写「Fast 档 `service_tier: priority` **只在 WS 上生效**（HTTP 直打 0.97× 无效，community C6）」。这条是 CLIProxyAPI #4586 的 **2026-07 旧结论**，而 notes-community **B5** 明确记录：该 PR 于 **2026-10 被作者关闭**，理由「current upstream Codex backend now respects service_tier: priority over HTTP/SSE」，并附 6 次交替基准（priority 83.7 TPS vs default 50.1，回显仍是 default），标注「**结论随时间反转过 → 必须自测**」。→ 角2 把「priority 只在 WS 生效」当定论，与 community 的「已反转、需自测」矛盾。**我的角3 §2.4 已按「有反转、须 A/B 自测」表述，采信角3 版本，不采信角2 的定论版。**
2. **角2 引用编号错位**：「community C6」对应不上——CLIProxyAPI #4586 在 notes-community 里是 **B5**，C 系列是中文社区传闻（linux.do/locdd/macos.do）。编号错位不改变「有反转」这一实质，但削弱该处论据定位的可靠性。

（其余无异议；三份笔记对「票长 292/780」「`__oailb` 只在 /models 下发、/responses 只有 `__cf_bm`=假票」等细节完全一致。）

---

## 2. 我的最推荐方案：读完另两角后，坚持 / 降级 / 修正？

**结论：坚持，且被角1、角2 强化；对其中一条细节做修正。**

- 我角3 最推荐 = 「降智检测判据从票形换成**流内实际模型 + safety-buffering 头**，命中即账号冷却/换号」。读完另两角后**不降级**，反而更强：
  - **角1 从传输面证明**：票其实「拿得到」（HTTP 响应头 142 条），打票失败是**形状/身份**问题，不是拿不到票。→ 说明「继续修打票」是一条有尽头但**回报有限**的路。
  - **角2 从票据面证明**：票是 per-turn Fernet sticky routing、非 VIP 凭证，跨账号回放是网关自己写明的矛盾信号。→ 直接坐实「共享票治不了降智」，与我角3 的②号结论完全一致。
  - 两角合起来 = 「票这条路，无论打对（角1）还是用对（角2），都不触及降智」——所以「放弃以票为核心、改检测+运维」是三角唯一自洽的出路。
- **修正一处（与我原方案不冲突，但并入）**：把「service_tier 提速」这条**从我的次选提为最终方案第 2 位**，并**强制要求 A/B 实测 HTTP vs WS**（不能照抄「只在 WS 生效」的旧结论）——因为角2 恰好在这里采信了过时结论，必须用自测把它钉死。

---

## 3. 最终给 sub4api 的「合理解决方案」排序（1~3）

1. **降智检测判据换成「流内实际模型 + safety-buffering 头」，命中即账号冷却/换号/切通道**。可行性高（已有降智检测骨架 + 网关可读 SSE/WS body），风险低-中（只观测+运维、不伪造），成本中。三角里**唯一真正作用于「质量」**的方案；不依赖票。

2. **`service_tier=priority/fast` 当「提速」运营，并做 HTTP vs WS 的 A/B 实测 + `primary-used-percent` 消耗速率对比**。可行性中（历史结论反转、须自测），风险低（官方功能、计费 2.5× 提前告知），成本中。官方唯一承诺「改变服务」的档位，但只管速度、不解除降智。

3. **（可选兜底）turn-state 改 per-account/per-turn 的 WS 回传 + 跨账号剥离，删除全站共享注入与 292 长度假设**。可行性高（网关 `relay/guardOpenAICodexTurnStateEcho` 已具备），风险低（合规、不伪造），成本中。只改善路由连续性，**明确不防降智**，作为协议正确性兜底而非降智解药。

> 一句话总纲：**检测+运维治「降智」，service_tier 管「提速」，票据只做「合规回传」——三件事分开，别再让打票背「防降智」的锅。**

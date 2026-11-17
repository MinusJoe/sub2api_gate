# 1.1.15

## 更新内容

- 对齐 Wei-Shaw/sub2api v0.2.22（`b0fa9f3e6eeab1968a53e1289b69daa4bb40ca2b`），保留本项目 Kimi OAuth、BPS、菜单、时区、credits CAS 与 Codex 调度定制，继续使用 Go 1.27.4。
- 新增独立的强制 OpenAI Ultra Fast 开关，可与现有强制档位并存。支持的 GPT-6 Astra / GPT-6.1 Sol 优先使用 Ultra Fast，其余模型沿用所选档位；保留独立免费 Ultra Fast 计费配置。鉴权快照升级至 v29。
- 同步按用户合并扣费与批量数据库结算、调度快照合并加载、按 bucket 合并数据库回源、请求体零拷贝及流式解析优化。
- 移除调度快照额外的 Codex 票据字段校验，缺少票据投影或参与配置不再强制回源数据库，与上游快照接纳行为一致。
- 移除选号和转发的 Codex 无票拦截及管理端对应设置。缺票、票据存储异常或数据损坏时继续转发，已有有效票据仍可注入；旧的 `fail_closed`、全局及账号无票策略不再生效，真实限流与冷却规则不变。
- Vertex 支持 us/eu 多区域端点和默认 1.1 倍区域计费，补齐 Gemini 4 Carbon / Hydrogen、Kimi 月度额度与模型兜底价格。
- 同步 OpenAI keepalive/流错误副作用、Codex 分叉缓存键、额外上下文和工具 Schema、Composite 模型名还原、Gemini 并行工具及结构化输出、Anthropic effort 与 token 轮换等修复。
- 分组日汇总重建和过期清理不再阻塞用量写入；同步账户额度展示、模型白名单校验、RPM/倍率覆盖、支付宝退款幂等、品牌 HTML 缓存和网络超时兼容修复。

## 升级说明

- 新增 `251_group_force_openai_ultrafast.sql` 与 `251_group_usage_rollup_nonblocking_build.sql`，启动时自动执行。迁移以完整文件名标识，保留上游原文与校验和；升级前备份数据库，回退程序不会自动撤销迁移。
- 强制 Ultra Fast 对支持模型的优先级高于原有强制档位；未开启独立免费 Ultra Fast 时按标准价 6 倍计费，需要上游账号具备对应权限。
- `concurrency.ping_interval=0` 可关闭并发等待心跳，避免排队期间提前提交 HTTP 状态码。

# 1.1.14

## 更新内容

- 对齐 Wei-Shaw/sub2api v0.2.21（`10ba8f9b4dc2e6023d4a9879ab77b21841babd10`），保留 Kimi、BPS、菜单、请求时区、配额调度与 credits CAS 定制。
- 新增 Claude Fable 5.2 / Mythos 5.2 模型、预设、Bedrock 映射和兜底定价，Claude Code 版本基线更新至 2.1.321。
- OpenAI 缓存写入区分 30m / 12h，贯穿用量解析、渠道及账号统计价卡、计费、落库和管理员用量详情；本项目 BPS 路径同步支持 TTL。已有通用缓存写入价格保持原有语义。
- 请求体内存预算未显式配置时按可用内存上限的 1/4 自动计算，最低 1 GiB；显式设置（包括 0）继续生效。
- 同步刷新令牌原子消费、并发槽位释放、过载冷却、Codex 元数据与页面上下文、WebSocket 后续轮次重试及搜索激活、reasoning effort 和工具调用字段修复。
- 同步 Gemini / Antigravity 兼容修复、定价目录同步修复、安全审计 worker 唤醒、EasyPay 退款成功码配置及充值倍率精度调整。
- API 密钥列表与账号用量刷新保留可见内容，Antigravity 支持强制查询，设置保存按钮使用吸附浮标。

## 升级说明

- 新增 `249_add_usage_logs_api_key_cost_index_notx.sql` 和 `250_openai_cache_write_ttl.sql`，启动时自动执行。迁移以完整文件名标识，与已有 `249_mcp_oauth.sql` 共存；保留原文件和校验和。
- 升级前备份数据库。249 使用 CONCURRENTLY 创建用量索引，大表首次升级可能耗时较长；回退程序不会自动撤销迁移。
- 请求体预算配置键为 `gateway.request_body_memory_budget_bytes`（环境变量 `GATEWAY_REQUEST_BODY_MEMORY_BUDGET_BYTES`）。低内存实例应按实际容量显式设置。
- 带 `X-Codex-Image-Turn-Id` 的非流式图片请求不再提前写出心跳，以保留实际错误状态码；代理需允许等待完整图片生成。详见 [图片接口说明](../../docs/OPENAI_IMAGES_CODEX.md)。

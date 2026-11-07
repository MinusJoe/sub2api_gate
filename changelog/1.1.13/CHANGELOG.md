# 1.1.13

## 更新内容

- 对齐 Wei-Shaw/sub2api v0.2.20（`dab829a4e9b7d1cc307189aea8b0319eb29e29d0`），保留 Kimi/BPS、菜单、请求时区和配额调度定制。
- 新增 OAuth 管理 MCP、管理员密钥细粒度权限与全权兼容模式、注册邮箱精确黑名单及 OpenAI 生图尺寸分流。
- 同步网关请求体内存预算、读写超时、后台任务上限及 OpenAI、Gemini、Antigravity、Grok、Kimi 转发修复。
- 加固 OpenAI credits 快照的身份绑定和 CAS 写入，避免旧配额覆盖新凭据状态；影子账号调度校验当前母账号，保留 Spark 配额隔离。
- Vue I18n 与消息编译器升级并锁定为 11.4.16，更新鉴权缓存版本并保留本项目缓存字段。

## 升级说明

- 新增迁移 `249_mcp_oauth.sql`，部署后启动时自动执行。升级前备份数据库；回退程序不会自动撤销迁移。
- MCP 默认关闭；启用需配置 HTTPS 的 `MCP_PUBLIC_URL`，路径为 `/api/v1/mcp`。详见 [MCP 配置](../../docs/mcp.md)。
- 请求体总内存预算默认 1 GiB，上传停滞与下游写入超时默认 120 秒。请按实例容量调整，值为 0 时禁用；详见 [边缘安全配置](../../deploy/EDGE_SECURITY.md)。客户端断开后的有界 drain 可能影响超时请求的用量记录。
- GPT-6.1 Terra Codex 上下文窗口为 500k；OAuth/setup-token 不再收取超过 272k 的长上下文附加费，API Key 保留 2x/1.5x 计费，兼容路径不接受 minimal effort。

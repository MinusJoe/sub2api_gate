# 1.1.18

## 更新内容

- 对齐上游 [Sub2API v0.2.24](https://github.com/Wei-Shaw/sub2api/releases/tag/v0.2.24)。网关会拒绝包含重复字段的请求，避免计费与实际转发使用不同的模型。
- 修复 Codex 文献检索返回 403 时误停 OpenAI OAuth 账号的问题，并合入 OAuth 令牌轮换保护和上游超时切换账号的修复。
- 网页搜索新增 Exa 后端支持，可通过管理 API 配置；本版设置页未新增 Exa 下拉选项。同步搜索来源展示、Claude Code 压缩请求的缓存计费处理、GPT-6.2 模型目录及 Ultra Fast 档位支持。
- 保留 1.1.17 已发布的 TLS 三档开关和费用明细中的实际 TLS 状态。TLS 全局默认与内置模板保持不变。

## 升级说明

- 拉取 `ghcr.io/minusjoe/sub2api_gate:1.1.18` 后重建应用容器，保留现有配置和数据卷。首次启动会自动执行数据库迁移，添加渠道 Ultra Fast 计费倍率字段。
- 请求体或 WebSocket 帧中包含重复字段时，网关会拒绝请求；自定义客户端应确保同一对象中的字段名唯一。

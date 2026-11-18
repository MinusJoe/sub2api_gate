# 1.1.18

## 更新内容

- 修复面板「TLS 指纹模拟」开关对 OpenAI OAuth/SetupToken 账号保存不生效的问题：编辑弹窗此前只在 Anthropic 配额块内写入 `enable_tls_fingerprint`，且后续 OpenAI extra 组装会用账号原值覆盖本轮修改，导致保存后开关回弹。现在 OpenAI 账号单独落库，重建 extra 时优先沿用本轮已合并的值，并在加载时同时读取顶层字段与 `extra`。
- 新增回归测试：断言 OpenAI OAuth 账号提交时请求体 `extra` 携带 `enable_tls_fingerprint: true`。

## 升级说明

- 直接替换镜像即可，无配置变更。
- 若此前用 SQL 手动写过 `enable_tls_fingerprint`，升级后面板会正确显示为已开启。
- 仍未开启指纹的账号可通过面板开关或 `gateway.openai_tls_fingerprint_default: true` 全局开启。

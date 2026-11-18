# 1.1.17

## 更新内容

- 面板为 OpenAI OAuth / SetupToken 账号新增「TLS 指纹模拟」开关（此前仅 Anthropic 账号可见，OpenAI 账号只能手写 SQL 才能开启 `enable_tls_fingerprint`）；同时修正开关下方中英文提示文案，改为"模拟官方客户端（Codex CLI / Claude Code）的 TLS 指纹"。
- 新增进程级默认开关 `gateway.openai_tls_fingerprint_default`（环境变量 `GATEWAY_OPENAI_TLS_FINGERPRINT_DEFAULT`）：开启后，未显式配置账号 extra 的 OpenAI OAuth/SetupToken 账号默认启用指纹，账号 extra 写 `false` 可单独退出；代码默认值仍为 `false`。`api_key` 账号永不启用；Anthropic 保持"仅显式开启"语义不变。
- 修复 OpenAI OAuth/SetupToken 账号的「TLS 指纹模拟」开关保存后回弹：单独写入 `enable_tls_fingerprint`，重建 extra 时保留本轮修改，并兼容读取顶层字段与 `extra` 中的已有值。
- 重新打开 OpenAI OAuth / SetupToken 账号编辑窗口时，现在会从账号详情恢复已保存的开关和模板。修复了第二次保存其他设置时可能清除已开启值的问题，回归测试覆盖“开启 → 保存 → 重开 → 再次保存”。
- 新增 `deploy/config.recommended.yaml`（从代码默认值导出的完整配置）与 `deploy/docker-compose.recommended.yaml`（1Panel 部署样例），便于按推荐值落地。
- 同步 `1.1.16` 已包含的 OpenAI 上游保真能力（TLS 指纹、身份三元组、版本下限、出口区域、准入边界、节律整形），本版仅补齐入口与默认值。

## 升级说明

- 本次覆盖更新原有 1.1.17，已安装旧包的用户需重新拉取同名镜像并重建容器，或重新下载对应安装包。
- 直接替换镜像即可，默认配置不改变任何出站行为；指纹仍需显式开启（账号 extra、面板开关或全局默认）。
- 若此前通过 SQL 设置过 `enable_tls_fingerprint`，面板会正确读取并显示；推荐配置示例显式开启全局指纹默认，使用前请按部署需求确认。
- 开启指纹的账号必须使用 `http` / `socks5` 代理，`https://` 代理会静默降级为 Go 原生 TLS。
- 单账号部署请保持 `gateway.openai_pacing_rpm_per_account: 0`，否则打满后无账号可轮换。
- 面板可见的「TLS 指纹模拟」开关请在 profile 下拉中保持「内置默认」，绑定具体模板会覆盖 Codex rustls profile。

# 1.1.17

## 更新内容

- OpenAI OAuth / SetupToken 账号的 TLS 指纹设置支持「继承全局 / 开启 / 关闭」。全局开启时，单独选择「关闭」仍能保存并走标准 TLS；WebSocket 连接池不会为新请求复用与当前设置不符的旧连接。
- 全局默认仍由 `gateway.openai_tls_fingerprint_default`（环境变量 `GATEWAY_OPENAI_TLS_FINGERPRINT_DEFAULT`）控制，代码默认值仍为 `false`。Anthropic 账号的设置方式不变。
- 后台用量记录的 TLS 指纹列、费用详情和导出显示请求实际观察到的 TLS 状态；旧记录没有观测数据时显示「未知」。
- Codex 客户端内置兜底版本升至 `0.169.0`，后台手填版本和自动同步的优先级不变。
- 新增 `deploy/config.recommended.yaml`（从代码默认值导出的完整配置）与 `deploy/docker-compose.recommended.yaml`（1Panel 部署样例），便于按推荐值落地。
- 同步 `1.1.16` 已包含的 OpenAI 上游保真能力（TLS 指纹、身份三元组、版本下限、出口区域、准入边界、节律整形），本版仅补齐入口与默认值。

## 升级说明

- 本次覆盖更新原有 1.1.17，已安装旧包的用户需重新拉取同名镜像并重建容器，或重新下载对应安装包。
- 升级启动时会自动为用量记录添加可空的 TLS 状态字段，旧记录无需回填。
- 已有账号继续沿用已保存的 TLS 设置；从未保存过账号设置的账号继承全局默认。推荐配置示例显式开启全局指纹默认。
- 开启指纹的账号必须使用 `http` / `socks5` 代理，`https://` 代理会静默降级为 Go 原生 TLS。
- 单账号部署请保持 `gateway.openai_pacing_rpm_per_account: 0`，否则打满后无账号可轮换。
- 面板可见的「TLS 指纹模拟」开关请在 profile 下拉中保持「内置默认」，绑定具体模板会覆盖 Codex rustls profile。

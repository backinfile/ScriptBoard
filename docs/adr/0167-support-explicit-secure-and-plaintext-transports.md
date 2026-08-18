# 同时支持安全与明文连接模式

ScriptBoard 的用户可配置连接不得把 SSL/TLS 写死为唯一模式。凡协议存在明确的明文形态，都同时接受安全与明文 scheme 或策略，并保留用户显式选择安全模式的能力。界面和文档必须说明明文会暴露凭据、请求与响应，但不能以自动升级、仅回环许可或降级拒绝代替用户选择。

| 连接边界 | 安全模式 | 明文模式 | 持久化与导入导出 |
| --- | --- | --- | --- |
| ScriptBoard 监听 | 配置证书与私钥后使用 HTTPS | 未配置证书时使用 HTTP，包括显式非回环监听 | 配置文件保留 TLS 证书与私钥选择 |
| Kubernetes API | kubeconfig `server: https://...`，支持自定义 CA、系统根、客户端证书及显式 `insecure-skip-tls-verify` | kubeconfig `server: http://...`，支持静态 token 或基本认证 | SQLite 只保存 kubeconfig 路径和 context；凭据与 TLS 验证选择仍留在 kubeconfig |
| AI / LLM Provider | HTTPS Endpoint | HTTP Endpoint，不限于回环地址 | Endpoint 保存在模型配置；API Key 仍只进入 State Root 私有凭据文件 |
| 自定义看板 JSON 与 Registry | HTTPS | HTTP | 导入导出保留 scheme；Registry 密码不导出；Bearer token realm 可独立使用 HTTP 或 HTTPS |
| 网站监控 | HTTPS、WSS | HTTP、WS | 导入导出保留 scheme、请求设置和 TLS 验证选项 |
| 远端网站监控汇聚与外部接口 | HTTPS | HTTP | 完整 Endpoint 保存在 SQLite；Key 加密保存在 State Root |
| MySQL / MariaDB | `required`、`verify_identity` | `disabled`；`preferred` 允许服务端不支持 TLS 时回退明文 | TLS 模式进入实例模型，并同步写入备份/恢复客户端配置 |

Kubernetes 的 `insecure-skip-tls-verify` 作为 kubeconfig 中显式选择的 HTTPS 模式予以保留和支持。它仍加密传输，但无法认证 API Server 身份并可能遭受中间人攻击；界面和文档必须提示风险，ScriptBoard 不得静默启用该选项。需要完全明文时仍应显式使用 HTTP。

以下路径仍只使用 HTTPS，原因不是通用连接器强制 TLS，而是它们是固定的发布供应链：

- 应用更新检查、清单、签名和归档下载只访问 GitHub API、GitHub Releases 及仓库内置的 GitHub 代理源。它们没有用户可配置的通用 HTTP Endpoint，官方支持面是 HTTPS，并且重定向继续限制在允许的 HTTPS host。
- Pi Runtime 在线安装同样只读取与当前 ScriptBoard Release 绑定的 GitHub 资产；离线安装提供不依赖网络协议的替代路径，并继续验证签名、版本、大小和 SHA-256。
- 构建脚本下载固定的 Runtime Release 资产时显式使用 HTTPS/TLS 1.2；这是构建供应链约束，不是运行时连接配置。

本决策扩展 [ADR-0165](./0165-default-to-loopback-and-allow-configured-listen-addresses.md)，并取代 [ADR-0166](./0166-monitor-one-kubernetes-cluster-with-bounded-operations.md) 中“Kubernetes 连接适配器只接受 HTTPS”的部分。历史 ADR-0082 已由 ADR-0165 取代，不再代表当前监听限制。

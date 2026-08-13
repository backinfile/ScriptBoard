# 审计事件记录请求关联与认证保证

每个进入 ScriptBoard Web Handler 的请求由服务端生成不可预测的 Request ID，写入请求上下文并通过 `X-Request-ID` 返回。客户端提供的同名 Header 不受信任，也不会成为审计关联值。External Interface 在解析出有效能力后改用该次 invocation ID，使外部请求记录、响应和审计事件可以直接关联。

`audit_events` 独立保存 `request_id` 与 `authentication_assurance`。浏览器会话记录 `aal1`/`aal2`，处于最近再次认证窗口时追加 `+step-up`；外部调用记录 `external-capability`；没有 Web 请求上下文的后台事件保持为空。字段不再拼入 `target`，避免 SIEM、告警和取证工具解析自由文本。

审计链保持向前兼容：schema 35–40 的事件继续使用 `scriptboard-audit-chain-v1` 字段集合；带 request ID 或认证保证的新事件使用 `scriptboard-audit-chain-v2`，并把两个新字段纳入摘要。验证器按事件字段选择版本，因此一条链可以包含 v1 历史和 v2 新事件，修改认证保证或请求关联会导致离线验证失败。schema 41 为旧事件添加空字段，不重写其既有摘要。

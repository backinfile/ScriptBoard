# 保持 MFA 注册可选并按账户能力执行 step-up

ScriptBoard 不再为 Administrator 或 Maintainer 设置 MFA 注册截止时间。未配置 TOTP 或 passkey 的账户可以正常访问其角色授权的页面，不会被限制到 MFA 设置与登出路径。schema 53 删除历史 `users.mfa_required_at` 字段，升级时不保留或重建截止时间。

高风险操作继续由声明式路由要求十分钟内的近期 step-up。未配置 MFA 的账户使用当前密码完成 step-up；一旦账户配置 TOTP 或 passkey，登录和高风险 step-up 都必须使用已配置的第二因素，不提供仅密码降级路径。哪些操作属于高风险操作仍由路由声明和权限测试集中维护，MFA 是否已配置不改变角色权限。

本决定取代 [ADR-0150](./0150-require-verified-passkeys-for-privileged-accounts.md) 中的强制注册截止策略，但保留其 passkey user verification、挑战绑定、凭据密封和计数器更新要求。

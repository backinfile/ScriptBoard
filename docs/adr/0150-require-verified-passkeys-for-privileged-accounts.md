# 为高权限账户增加受验证的 passkey 与注册截止策略

ScriptBoard 把 WebAuthn/passkey 作为 TOTP 的并列第二因素，而不是无密码登录入口。账户密码先通过 Argon2id 验证；若账户已配置 TOTP 或 passkey，登录和高风险 step-up 必须再验证其中一个因素，成功会话记为 `aal2`。这样新增 passkey 不会为已启用 MFA 的账户形成仅密码旁路。

WebAuthn RP ID 与 Origin 来自经过 Host/代理边界校验的 `canonical_external_url`（测试和未显式配置的回环部署使用当前已验证 Origin）。注册与断言都要求 authenticator user verification，注册优先 discoverable credential 且不要求可识别 attestation。challenge 只在服务端内存保存，绑定 ceremony 类型、用户、浏览器会话和精确 Origin，五分钟过期并在 finish 时先消费。未知用户名和未注册账户返回带随机不可验证 credential descriptor 的同形登录 challenge，避免显式账户/passkey 枚举接口。

凭据记录包含 ID、公钥、attestation 元数据、flags 与 authenticator counter，并使用 State Root 外主密钥以独立用途整体密封；成功断言后原子持久化库返回的更新记录。注册、删除 passkey 与本机管理员恢复都会撤销会话，管理员恢复同时清除 TOTP 和全部 passkey。

schema 42 在用户记录加入 `mfa_required_at`。首次管理员与新 Maintainer 默认获得 24 小时注册窗口，旧实例的 Administrator/Maintainer 获得七天迁移窗口。截止后仍无 TOTP/passkey 的会话只允许账户、MFA、step-up、locale 与登出路径；其他读取重定向到注册页，写请求在业务 Handler 前以 403 拒绝并写审计。窗口是防止升级锁死唯一管理员的有界兼容措施，不是永久豁免。

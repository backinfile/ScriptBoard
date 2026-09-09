# ADR 0184: Persist instance embedding settings

页面嵌入在 `/settings/embedding` 管理，沿用系统设置权限、近期身份验证、CSRF 与同源写入校验。允许禁止嵌入、指定完整 HTTP/HTTPS 来源或显式允许全部地址。全部地址模式在表单及 README 提示诱导点击风险。

SQLite 单例以空数组表示禁止、星号数组表示全部地址。未保存时继承启动配置；保存后覆盖启动配置，下一次请求生效，重启保持。每个请求读取一次并共享策略快照，保证外层 CSP、嵌套自定义页签 CSP 和登录 Cookie 一致。读取失败时拒绝响应并禁止嵌入。

嵌入权限不共享账号，不开放跨来源管理请求。HTTPS 的嵌入登录继续使用 Secure + SameSite=None；HTTP 保留原 Cookie 策略。浏览器的第三方 Cookie 和混合内容限制仍适用。

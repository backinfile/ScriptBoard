# 可信代理只接受一套有界 X-Forwarded 合同

`trusted_proxies` 继续默认为空，非可信直连 peer 提供的 `Forwarded` 与全部
`X-Forwarded-*` 字段在任何 Host、Origin、Cookie、限流或业务处理前删除。

显式可信 peer 只能使用 `X-Forwarded-For`、`X-Forwarded-Host` 和
`X-Forwarded-Proto`。ScriptBoard 拒绝与标准 `Forwarded` 混用、同名字段重复、空链元素、
非法 IP/Host/Proto 以及超过 8 跳的链，而不是忽略坏值后继续。多跳取从右向左遇到的第一个
非可信 IP；最终 Host 仍必须进入 `allowed_hosts`，状态修改的 Origin 必须与其 scheme/host
完全匹配。

解析失败返回 400，未知 Host 返回 421，均发生在业务 Handler 前。只有真实 TLS 或通过
上述合同验证的最终 `https` 才能设置 HSTS 和 Secure Cookie。该合同必须在 Nginx、Caddy、
IIS 和 IPv6 的部署门禁中使用同一组黑盒用例。

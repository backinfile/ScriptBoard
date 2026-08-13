---
status: superseded by ADR-0165
---

# 非回环访问必须使用 HTTPS

默认 `http://localhost:8787` 仅供本机回环访问；只有显式配置 TLS 证书与私钥后，ScriptBoard 才允许直接监听局域网或公网地址，且不内置 ACME。也可由显式配置的可信 HTTPS 反向代理转发到回环后端；默认不信任任何代理地址，只有 `trusted_proxies` 明确列出的直连 peer 才能提供转发客户端地址。HTTPS 强制 Session Cookie 使用 `Secure`，状态页醒目标识远程高权限管理已启用；任何非回环明文监听配置都拒绝启动。代理默认值由 [ADR-0130](./0130-default-to-closed-security-boundaries.md) 调整。

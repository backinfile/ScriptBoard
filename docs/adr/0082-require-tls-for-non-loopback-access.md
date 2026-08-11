---
status: superseded by ADR-0130
---

# 非回环访问必须使用 HTTPS

默认 `http://localhost:8787` 仅供本机回环访问；只有显式配置 TLS 证书与私钥后，ScriptBoard 才允许直接监听局域网或公网地址，且不内置 ACME。也可由同机可信 HTTPS 反向代理转发到回环后端：`127.0.0.1/32` 是默认可信代理，其他代理来源必须显式配置后才接受其转发客户端地址。HTTPS 强制 Session Cookie 使用 `Secure`，状态页醒目标识远程高权限管理已启用；任何非回环明文监听配置都拒绝启动。

---
status: accepted
---

# 默认使用宿主系统最高权限

> **已被取代（2026-08-12）**：受管部署的 Web 与 Runner 不再共享最高宿主权限；当前决定见 [ADR-0147](./0147-run-managed-web-under-a-low-privilege-service-identity.md)、[ADR-0149](./0149-isolate-runs-behind-a-dedicated-worker.md) 与 [ADR-0163](./0163-ship-four-trust-boundaries-as-one-versioned-product.md)。本文仅保留为历史记录，不能作为新安装或发布验收依据。

> 非回环监听的当前决定另见 [ADR-0165](./0165-default-to-loopback-and-allow-configured-listen-addresses.md)。

ScriptBoard 的核心用途是以足够权限管理本机文件并执行管理员信任的脚本，因此默认服务安装使用宿主系统最高权限：Linux systemd 系统服务默认以 root 运行，Windows 服务默认以 LocalSystem 运行。应用不实现权限提升、降权或逐脚本身份切换，所有脚本无条件继承服务进程的运行身份；安装者可通过 systemd `User=` 或 Windows 服务登录账号改用普通用户，手动运行时则继承启动者权限且不会因不是最高权限而拒绝启动。这个决定取代 ADR-0002 的低权限默认，并明确接受其后果：任何被执行脚本、被盗管理员会话或应用漏洞都可能完全控制宿主机，因此明文 HTTP 仍必须仅监听回环地址，且部署文档必须突出警告这一信任边界。

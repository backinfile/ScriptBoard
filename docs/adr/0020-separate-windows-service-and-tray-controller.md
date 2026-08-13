# 分离 Windows 服务与托盘控制器

> **部分被取代（2026-08-12）**：托盘与后台服务分离仍有效，但“单个 LocalSystem Web/执行服务”已经被 [ADR-0147](./0147-run-managed-web-under-a-low-privilege-service-identity.md) 和 [ADR-0163](./0163-ship-four-trust-boundaries-as-one-versioned-product.md) 取代。当前后台由低权限 Web、LocalSystem Broker、受限 Runner 与受限 AI Host 四服务组成。

Windows 上由默认以 LocalSystem 运行的后台服务进程拥有 ScriptBoard Web 服务和脚本执行生命周期，并默认随系统启动；独立的普通用户态托盘程序不提供主窗口，默认随当前用户登录且禁止重复实例。托盘只提供状态、打开网页、启动、停止、重启、打开 State Root 与服务日志、自启动开关和退出；启停转换期间禁用重复操作，有活动执行时停止或重启需确认，退出托盘不停止服务。两者分离是因为 Windows 服务运行在非交互会话，不能可靠承载登录用户的托盘图标。

---
status: superseded by ADR-0030
---

# 通过本机控制套接字触发计划执行

ScriptBoard 管理的 crontab 条目不直接启动目标脚本，而是调用 `scriptboard cron-trigger --id <stable-id>`，通过仅允许运行身份访问的 Unix Domain Socket 通知正在运行的服务。服务统一解析变量、选择执行器、创建 Run、保存日志并提供停止控制；服务不可用时触发命令直接失败，不绕过应用独立执行。该控制通道不使用 HTTP、Cookie 或 CSRF。

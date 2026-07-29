# 架构决策索引

本目录按访谈确认顺序保存短 ADR。默认没有 `status` frontmatter 的 ADR 视为已接受；实现时不得采用已标记为 `superseded` 的旧决定。

## 已被取代

- [ADR-0002](./0002-trust-admin-provided-scripts.md) → [ADR-0023](./0023-default-to-highest-host-privileges.md)：低权限默认改为服务身份直接继承，默认服务使用最高权限。
- [ADR-0012](./0012-reject-instead-of-queueing-executions.md) → [ADR-0027](./0027-allow-unbounded-concurrent-runs-without-a-queue.md)：单活动执行改为无上限并发、无队列。
- [ADR-0015](./0015-save-quick-runs-explicitly.md) → [ADR-0111](./0111-create-quick-runs-from-files-or-run-history.md)：快捷执行项从仅支持历史 Run 保存扩展为也可直接从受管脚本文件配置。
- [ADR-0024](./0024-manage-only-owned-entries-in-runtime-users-crontab.md) → [ADR-0030](./0030-use-an-internal-scheduler-instead-of-crontab.md)：系统 crontab 改为内置调度器。
- [ADR-0025](./0025-skip-busy-cron-triggers-without-queueing.md) → [ADR-0027](./0027-allow-unbounded-concurrent-runs-without-a-queue.md)：全局繁忙跳过改为并发执行；计划仍可按项禁止同脚本重叠。
- [ADR-0026](./0026-trigger-cron-runs-through-a-local-control-socket.md) → [ADR-0030](./0030-use-an-internal-scheduler-instead-of-crontab.md)：移除 crontab 与本机触发 Socket。
- [ADR-0074](./0074-provide-cli-backup-for-private-application-state.md) → [ADR-0100](./0100-do-not-provide-user-facing-backup-commands.md)：移除用户备份/恢复命令。
- [ADR-0076](./0076-do-not-self-update.md) → [ADR-0115](./0115-auto-check-and-require-admin-approval-for-updates.md)：从完全不联网更新改为正式构建自动检查、管理员确认后安全安装。
- [ADR-0077](./0077-ship-portable-archives-instead-of-native-installers.md) → [ADR-0117](./0117-use-versioned-service-installs-and-an-external-updater.md)：继续发布便携归档，但服务安装改为包含 updater 的版本化 Install Root。
- [ADR-0081](./0081-ship-a-simplified-chinese-only-mvp.md) → [ADR-0108](./0108-localize-the-web-in-zh-cn-and-en-us.md)：Web 改为简体中文和美式英语双语，CLI 与托盘仍为简体中文。
- [ADR-0083](./0083-support-modern-desktop-and-mobile-browsers.md) → [ADR-0110](./0110-use-desktop-chromium-as-the-browser-gate.md)：浏览器自动化门禁收敛为桌面 Chromium，其他现代浏览器最佳努力兼容。

## 冻结文档

- [MVP 产品需求](../PRD.md)
- [数据模型与状态机](../DATA-MODEL.md)
- [验收标准](../ACCEPTANCE.md)

## 后续能力

- [ADR-0107](./0107-provide-bounded-local-host-status.md)：提供有界的本机宿主状态。
- [ADR-0108](./0108-embed-a-tool-constrained-ai-agent.md)：嵌入工具受限的 AI Agent。
- [ADR-0109](./0109-own-website-monitoring-in-a-bounded-module.md)：在独立有界模块中实现网站监控。

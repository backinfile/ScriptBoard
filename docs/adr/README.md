# 架构决策索引

本目录按访谈确认顺序保存短 ADR。默认没有 `status` frontmatter 的 ADR 视为已接受；实现时不得采用已标记为 `superseded` 的旧决定。

## 已被取代

- [ADR-0002](./0002-trust-admin-provided-scripts.md) → [ADR-0023](./0023-default-to-highest-host-privileges.md)：低权限默认改为服务身份直接继承，默认服务使用最高权限。
- [ADR-0012](./0012-reject-instead-of-queueing-executions.md) → [ADR-0027](./0027-allow-unbounded-concurrent-runs-without-a-queue.md)：单活动执行改为无上限并发、无队列。
- [ADR-0024](./0024-manage-only-owned-entries-in-runtime-users-crontab.md) → [ADR-0030](./0030-use-an-internal-scheduler-instead-of-crontab.md)：系统 crontab 改为内置调度器。
- [ADR-0025](./0025-skip-busy-cron-triggers-without-queueing.md) → [ADR-0027](./0027-allow-unbounded-concurrent-runs-without-a-queue.md)：全局繁忙跳过改为并发执行；计划仍可按项禁止同脚本重叠。
- [ADR-0026](./0026-trigger-cron-runs-through-a-local-control-socket.md) → [ADR-0030](./0030-use-an-internal-scheduler-instead-of-crontab.md)：移除 crontab 与本机触发 Socket。
- [ADR-0074](./0074-provide-cli-backup-for-private-application-state.md) → [ADR-0100](./0100-do-not-provide-user-facing-backup-commands.md)：移除用户备份/恢复命令。

## 冻结文档

- [MVP 产品需求](../PRD.md)
- [数据模型与状态机](../DATA-MODEL.md)
- [验收标准](../ACCEPTANCE.md)

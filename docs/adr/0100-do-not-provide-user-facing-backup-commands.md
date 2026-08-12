# 不提供面向管理员的备份与恢复命令

> 本决策已被 [ADR-0162](./0162-provide-encrypted-private-state-backup-with-audit-continuity.md) 取代；保留本文仅用于说明历史边界。

ScriptBoard 不提供 `backup create`、`backup restore` 或网页备份功能。主机文件、每个文件系统的私有回收区与 SQLite 状态由管理员使用操作系统或第三方备份工具保护。ScriptBoard 不再内置 Git 版本保护，也不宣称文件系统回收区能防止磁盘损坏或整机丢失。Schema 20 只接受全新状态库；不为旧库创建迁移快照，而是在任何写入前明确拒绝启动。

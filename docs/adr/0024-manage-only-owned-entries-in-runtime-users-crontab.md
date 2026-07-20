---
status: superseded by ADR-0030
---

# 只管理运行身份 crontab 中的自有条目

Linux 上 ScriptBoard 只读写其当前运行身份对应用户的 crontab：默认 root 服务因此管理 root 用户 crontab，改用普通服务账号后只管理该账号。应用不切换用户，也不编辑 `/etc/crontab` 或 `/etc/cron.d/`。由 ScriptBoard 创建的条目带稳定 ID 并可增删改；同一 crontab 中的人工或其他工具条目仅展示为只读，更新时原样保留，并通过版本检查避免覆盖外部并发修改。

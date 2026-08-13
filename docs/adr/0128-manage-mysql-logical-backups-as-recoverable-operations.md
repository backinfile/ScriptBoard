# ADR 0128: 将 MySQL 逻辑备份建模为可恢复操作

## 状态

Accepted

> MySQL 主密钥位于 State Root 的保存方式已被 [ADR-0143](./0143-seal-recoverable-secrets-with-an-external-host-key.md) 取代。

## 背景

ScriptBoard 需要在不捆绑数据库客户端、不暴露凭据、且服务可能在破坏性恢复期间重启的条件下，管理本机及远程 MySQL/MariaDB。直接在 Web handler 中执行管理 SQL 或串联命令，会把凭据、状态恢复、审计和并发规则分散到传输层。

## 决策

新增独立 `mysqlmanager` 领域模块，由 `InstanceService`、`BackupService`、`RestoreService` 与 `PlanService` 语义共同约束实例、备份、恢复和计划。Web 层只调用领域入口。

逻辑备份使用宿主 PATH 或显式绝对路径中的 `mysqldump` 与 `mysql`。凭据使用 State Root 私有主密钥加密，CLI 通过临时权限受限的 option file 获取。每库生成独立 `.sql.gz`，在原子提交前计算 SHA-256。

恢复现有库和删除库必须先完成安全备份。操作阶段持久化到 SQLite；服务重启后清理未提交产物，并对已经进入破坏性阶段的恢复执行自动回滚，无法确认安全结果时进入 `needs_attention`。同一实例的操作串行，Cron 重叠触发记录为跳过且不补跑。计划轮换只拥有该计划成功生成的产物。

## 后果

- 凭据、CLI 参数、恢复协议、轮换和审计规则集中在一个深模块内，传输层不能绕过安全前置条件。
- 发布包保持数据库客户端无关；兼容性取决于实例版本和宿主客户端能力。
- 逻辑备份适合中小型实例，但不替代物理/增量备份、PITR 或异机灾备。
- 首版明确不支持 Socket、命名管道、SSH 隧道、系统库跨版本恢复、SQL 控制台和复制管理。

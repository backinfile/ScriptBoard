# SQLite 配置以断电耐久性优先

SQLite 启用 WAL、`synchronous=FULL`、foreign keys 与 busy timeout，所有状态写入使用短事务；定期 checkpoint，并在正常停服时执行最终 checkpoint。高频 Run 输出只写追加式事件日志，数据库保存状态与索引而不逐事件写入。检测到完整性异常时应用拒绝执行脚本，并只允许本机诊断与备份恢复流程，优先保护账号、计划、审计和不可删除执行历史。

# 通过持久 outbox 转发已提交审计事件并生成安全告警

远端安全事件转发只观察已经成功提交到本地哈希链的事件。审计 Store 在 SQL commit 和链锁释放后发布包含 event ID、已脱敏字段与链摘要的通知；observer 不能回滚、延迟或改变本地审计事务。事务 rollback 不产生通知。

启用 `security_event_endpoint` 后，每条通知先以 0600 临时文件原子替换进入 `state-root/security-events/outbox`，再按 event ID 顺序发送。outbox 最多保留 10,000 条，远端失败采用有上限指数退避，服务重启后继续；发送成功才删除本地文件。接收端必须是无 URL 凭据的 HTTPS 地址、禁止重定向，并使用共享 OutboundPolicy 固定 DNS 校验结果；默认拒绝私网，显式 `security_event_allow_private` 仍不能放行云元数据地址。Bearer token 只从绝对路径文件读取，不进入 YAML、环境或命令行明文。

检测器在同一 post-commit 边界处理事件：五分钟内第五次认证失败、十次权限拒绝，以及一分钟内二十次 External Trigger 拒绝产生高危告警，此后按有界增量再次告警；签名验证与 Runner/Runtime 隔离边界失败立即告警。告警写入受限、10 MiB 单代轮转的 `logs/security-alerts.jsonl` 并随远端 envelope 转发。转发故障不阻断业务或审计链，但待发事件保持在磁盘上以供运维检查。

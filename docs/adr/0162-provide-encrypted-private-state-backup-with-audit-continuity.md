# 提供带审计连续性的加密私有状态备份

ScriptBoard 提供 `backup create`、`backup inspect` 与 `backup restore` 本机 CLI，作为 P1 State Root 恢复能力的带外基础。备份不是 State Root 的文件系统副本：创建时使用 SQLite `VACUUM INTO` 取得一致性数据库 snapshot，只打包 `app.db`、受管 Run 私有证据、Broker 密文和本地安全事件；固定排除 `database-backups/`、上传 inbox、诊断日志、runtime、SQLite WAL/SHM 与实例锁。归档清单记录格式、Backup ID、数据库 schema、逐文件大小与 SHA-256、排除项、外部依赖以及创建时已验证的签名审计 checkpoint。

归档使用 Argon2id 从独立口令派生密钥，并以 XChaCha20-Poly1305 分块认证加密；口令只接受权限受限的绝对路径普通文件，不进入参数或环境。输出使用排他创建且不覆盖；读取必须认证完整终止记录，归档只接受固定相对普通文件路径、唯一条目、文件数量与总展开大小上限。恢复要求服务停止、完整 Backup ID 确认、兼容 schema、完整认证和摘要校验、SQLite `quick_check`，并在同卷 staging 中完成全部验证后才替换。所有恢复出的 Web Session 在提交前撤销，恢复前私有状态保留为同级目录；最终化或审计重锚失败会把原状态放回。

外部可恢复秘密主密钥、审计 checkpoint 签名私钥、启动配置和 TLS 材料不进入状态备份，必须由管理员独立保护。单独取得状态备份和口令仍不足以在另一实例解封 Broker 密文。管理员可用 `backup export-recovery` 生成独立的认证加密 host-recovery 材料；它以单独口令保护 credential master 和经其密封的审计签名密钥，Windows 新主机恢复时把 master 重新封装到本机 DPAPI，而不是复制旧机器 blob 或降级为明文。`backup recover-host` 只接受相同 canonical State Root 路径上的空、未初始化目录，要求三个组件停止和完整 Backup ID，拒绝覆盖任何已有外部信任材料；它把状态备份内的签名 checkpoint 与恢复出的链重新验证，撤销所有 Session，记录 `state_backup.recover_host` 后才推进新主机 checkpoint。状态包、recovery 材料和两份口令应分开保管。

恢复旧数据库通常会落后于当前外部审计 checkpoint，因此常规 checkpoint 刷新必须继续拒绝它。备份携带创建时已经由外部 Ed25519 密钥签名、且属于 snapshot 审计链的 checkpoint。恢复提交前分别验证当前 checkpoint 与备份 checkpoint；恢复后追加 `state_backup.restore` 连续性事件，事件绑定恢复前 checkpoint 文档摘要并记录备份 checkpoint 身份，然后才使用同一受保护私钥生成新 checkpoint。恢复前 checkpoint 文档随原状态保留。只有这个固定恢复动作允许受控的向后 Event ID 转换，普通启动和刷新路径仍然 fail closed。

CLI 是服务不可用时的恢复入口，不替代 Web 权限模型。受管 Web 页面已经可以在近期 AAL2 下创建、检查、暂存和丢弃恢复；Broker 独立复核会话与参数、验证归档和签名 checkpoint、撤销恢复出的 Session，并用暂存后逐文件摘要绑定 24 小时内待提交的 payload。Web 只接触服务器绝对路径和去敏公开清单，不得直接替换自己的活动数据库或解封 checkpoint 私钥。最终提交恢复仍必须停止四个组件、再次核对 Backup ID 并走离线恢复入口。

本决策取代 ADR-0100 的“没有用户备份命令”结论，也取代 ADR-0074 对密钥打包和无审计连续性恢复的旧细节；继续沿用 ADR-0143 的外部主密钥、ADR-0145/ADR-0155 的外部签名锚点和 Broker 私钥隔离边界。

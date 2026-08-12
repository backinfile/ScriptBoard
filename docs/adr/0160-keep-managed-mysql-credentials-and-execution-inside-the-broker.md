# 将受管 MySQL 凭据与执行能力限制在 Broker 内

受管 Web 不再初始化、迁移、解封或读取 MySQL 密码，也不再创建 MySQL 网络连接、客户端 option file，或启动 `mysql` / `mysqldump`。Web 只保留不含秘密的实例、备份、操作和计划元数据以及 HTTP 编排；Broker 持有用途绑定的密文，并把实例 ID、Host、Port、Username、TLS mode 与 CA path 和密码整体绑定。每次数据库或客户端操作都重新核对 IPC 中的实例与共享数据库内已经提交的记录，字段不一致即 fail closed。

Broker 协议只接受固定的 Store、Delete、Test、Databases、Status、Exists、Create、Replace、Drop、Dump、Import、Tools 和 Cancel 操作。凭据变更、工具变更和破坏性动作要求近期认证，其他交互操作要求当前 Administrator/Maintainer 会话；错误响应不回传数据库或客户端 stderr。备份和导入只允许配置的绝对备份根内、解析父级符号链接后仍受限的路径；Dump 文件由 Broker 以排他创建方式生成。MySQL/MariaDB 客户端只允许固定 basename，执行前复用 Runner 的规范路径、普通文件、Owner 与写 ACL 信任校验，工具设置不能退化为通用命令执行。

定时备份和中断恢复由 Broker 自己调度，不给 Web 可重放的后台系统令牌。Web 与 Broker 内的两个 Manager 通过 SQLite partial unique index 对每个实例实施跨进程单活动操作；Web 的取消请求经固定 Broker 操作传到真正持有子进程 context 的 Broker Manager，客户端断开也会取消执行。长 MySQL 操作单独允许两小时 IPC 截止时间，其他 Broker 调用继续保持 35 秒上限。

升级时 Broker 读取旧 `mysql-credentials.key` / `mysql-credentials.enc` 或现有 `mysql-credentials.v2.enc`，为每条可恢复密码补齐实例字段绑定后才删除旧原始密钥材料；缺少实例元数据时拒绝启动。独立 Web/Broker 凭据根的集成测试确认受管 Web 不产生 MySQL 密文，不持有本地数据库连接器或命令执行器，Broker 密文不包含明文；协议测试覆盖无会话、字段混用、实例替换、路径逃逸、取消传播和客户端 allowlist。

这项决定完成 P0-02 的 MySQL 凭据和执行切片，但不代表 P0-02 整体完成。Host Files 的列目录、下载、上传、移动、删除、备份根保护等宿主文件权限仍在 Web 进程或 Web 身份内，必须继续迁入固定边界并完成发布平台权限矩阵。

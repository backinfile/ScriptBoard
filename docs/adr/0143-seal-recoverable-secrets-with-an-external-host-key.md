# 可恢复凭据使用 State Root 外部、操作系统保护的主密钥

Provider API Key、MySQL 密码和远程 ScriptBoard 网站状态 Key 必须能够在服务运行时恢复，
但密文与解密主密钥不能继续同存于 State Root。统一 `internal/secretstore` 为每个规范 State
Root 派生独立 key 文件名，把主密钥保存在 State Root 同级的受保护 `secrets` 目录；Host
Files 将整个外部目录加入 Protected Paths。业务数据以 AES-256-GCM 密封，附加认证数据绑定
具体用途，不能把一个领域的密文换到另一个领域解密。

Windows key 文件只保存机器级 DPAPI blob，并继承只允许服务身份、SYSTEM 与管理员访问的
受保护 ACL；Unix key 文件保存原始随机 key，但目录为 0700、文件为 0600，预期由 root 或
专用服务身份拥有。单独复制 State Root 因不包含该文件而不能离线解密；Windows 即使同时
复制 blob 到另一主机也不能解封。doctor 只读检查外部 key 文件存在且是普通文件，不创建或
输出密钥。

启动迁移覆盖三类旧格式：明文 `assistant-provider.json`、State Root 内的 MySQL AES key，
以及 External Interface/远程网站秘密的旧 AES key。新密文完整提交后先删除旧原始 key，再
删除旧密文；旧 Provider 明文删除失败时回滚新文件并拒绝启动。启动不能完成迁移时 fail
closed，不能带着可独立解密的旧 State Root 继续服务。

这项决定取代 ADR-0127、ADR-0128（MySQL）和 ADR-0128（跨实例网站监控）中“主密钥位于
State Root”的保存方式，不改变一次显示 External Trigger Key 只存不可逆 verifier 的新语义。
Unix 备份必须把外部 key 作为独立秘密处理；Windows 跨主机恢复需重新录入可恢复凭据。

后续 ADR-0156、ADR-0157、ADR-0158 与 ADR-0159 已分别把受管部署的 MFA、Passkey、远程网站连接状态及 Assistant Provider 凭据迁入 Broker-owned 领域存储；本决策中的通用外部主密钥在受管 Web 内只继续服务 MySQL，并保留非受管部署的兼容路径。

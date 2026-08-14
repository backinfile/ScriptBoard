# 将 Registry 监控连接与请求限制在 Broker 内

Custom Dashboard 的 Registry Endpoint、用户名与密码共同构成一个连接绑定。受管部署中，Web 不再保存或读取 Registry 密码，也不直接执行带凭据的 Registry 请求；连接记录由 Broker 使用 State Root 外部主密钥加密，实际探测也在 Broker 内完成，Web 只接收镜像版本和脱敏错误。

连接变更采用 `prepare / commit / abort`。Web 先让 Broker 持久化不可见的待提交版本，再在同一个 SQLite 事务中写卡片配置与 Registry 操作日志，事务提交后激活 Broker 版本。若进程在两次提交之间中断，启动 reconciliation 根据操作日志幂等完成；SQLite 未提交时待提交版本不会替换现用连接。删除和认证方式切换使用同一协议，因此不会出现“新 Endpoint 使用旧密码”或删除卡片后遗留活动凭据。

升级时 Broker 读取旧 `custom-dashboard-registry.json` 和同目录 AES 主密钥，按数据库中的 Registry 卡片绑定完整连接。所有可恢复记录成功激活后才删除旧密文与主密钥；任一记录损坏时启动失败关闭。没有密码的导入 Basic 卡片保持“需要重新配置”，不会获得其他卡片或旧连接的密码。

非受管测试兼容路径使用相同深模块和外部 `secretstore`，不再使用 Dashboard 自有主密钥。正式发布仍以四服务信任边界和 Broker IPC 门禁为准。

HTTP Registry 卡片可由系统管理员通过独立的近期认证操作，把该 Registry 的 `host[:port]` 幂等加入 Docker Engine `daemon.json` 的 `insecure-registries`。Broker 只写入 Registry 主机，不把用户名、密码或 Token 写入 Docker 配置；更新会拒绝符号链接、超限文件、无效 JSON 和非数组的既有字段，并以同目录临时文件原子替换，同时保留其他 Docker 配置键。操作不会自动重启 Docker Engine，页面明确提示管理员在合适的维护窗口重启后生效。

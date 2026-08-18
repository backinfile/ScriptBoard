# 将受管远程网站连接凭据与 Endpoint 一并限制在 Broker 内

> 已被 [ADR-0174](./0174-retire-cross-instance-website-monitoring.md) 取代。

受管 Web 不再打开、解封或保存远程 ScriptBoard 网站的完整 Key。Broker 持有加密的连接记录，并把随机连接 ID、精确 HTTPS Endpoint 与完整 Key 原子绑定；Web 数据库只保存不含秘密的连接元数据。创建和删除必须携带当前 Web 请求的原始会话令牌与 Request ID，Broker 重新查询权威数据库，要求 Administrator/Maintainer、有效会话以及十分钟内的近期认证，并把意图与结果写入 Broker 审计链。

读取远端监控状态同样必须携带有效的当前会话，但不要求观察者重复 step-up，也不把 Key 返回给 Web。Broker 在内部注入 `Authorization`，通过统一出站策略固定 DNS 与目标地址，拒绝重定向，并限制超时和响应大小。Endpoint 必须使用 HTTPS，不得含 userinfo 或 fragment，并必须包含接口名称；Key 必须符合固定 `sbk_` 结构。Broker 先验证 JSON envelope、动作和 schema，Web 再验证网站监控领域数据与数量上限。

IPC 只接受固定的 `Store`、`Fetch` 与 `Delete` 字段，拒绝通用 payload、通用密文、MFA、Passkey 或宿主动作字段。每个连接记录、整个加密存储以及 Broker 响应均有独立大小上限，连接数量也有界。删除是幂等的：先删除 Broker 中的秘密，再删除 Web 数据库元数据；数据库删除失败只会留下不可用的可见元数据，后续重试仍可完成。创建若遇到不确定结果，会尝试按随机连接 ID 清理 Broker 记录并 fail closed。

升级时 Broker 使用数据库中已保存的 Endpoint 绑定旧版可恢复 Key，确认新记录持久化后才删除旧秘密。迁移失败会拒绝继续启动，不允许 Web 回退到本地 secret store。不同 Web/Broker 凭据根的集成测试确认密文只在 Broker 侧产生；外部浏览器故障测试确认 Broker 下线时删除受控失败且元数据保留，Broker 恢复后同一会话可以清理，页面不会显示完整 Key。

这项决定进一步收窄 ADR-0143 的受管部署适用范围：远程网站 Key 不再由通用外部主密钥服务给 Web 解封。External Interface 入站 Trigger Key 继续只显示一次并仅保存不可逆 verifier。P0-02 仍未完成；Assistant Provider、MySQL 与 Host Files 特权仍需迁入独立代理或执行边界。

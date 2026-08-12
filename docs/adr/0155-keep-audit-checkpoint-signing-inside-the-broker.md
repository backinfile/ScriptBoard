# 将受管审计 checkpoint 签名限制在 Broker 内

受管 Web 不再打开或解封外部 Ed25519 审计 checkpoint 私钥。Web 通过既有、受 OS peer identity
保护的 Broker IPC 使用两个无参数领域操作：`checkpoint_verify` 在启动时校验锚点，
`checkpoint_write` 在保留清理、周期刷新和正常关闭时推进锚点。协议拒绝 session token、特权动作、
资源、参数摘要、任意 payload 和 capability，因此这个接口不能退化为通用 Seal/Unseal 或签名服务。

Broker 对每个请求重新使用自己打开的权威 SQLite 数据库验证完整哈希链及此前信任的外部 checkpoint，
只为当前已验证链尾生成签名；它只向 Web 返回非秘密的 checkpoint 事件 ID。向后移动、替换同一事件
身份或提交一个不属于当前链的 checkpoint 仍然 fail closed。Web 缓存的事件 ID 只用于保留边界，
不是 Broker 的信任输入。

systemd 的停止顺序必须让 Web 在 Broker 之前退出，使正常关闭可以完成最后一次远程刷新。Ubuntu
26.04 实际安装验证了该顺序、checkpoint 文件随 Web 正常关闭推进，以及随后离线
`audit verify --json` 仍报告有效链和有效签名。独立 CLI、安装初始化与应急取证属于本机高权限或
离线恢复路径，仍可直接打开签名材料。

这只移出了一个明确的秘密消费点，不代表 P0-02 已完成。Web 仍因 MFA、Provider、MySQL、外部连接
等兼容消费者而能读取通用凭据主密钥；这些领域必须改为“验证、代理或执行”式接口，不能新增把
明文秘密返还给 Web 的通用 RPC。

# 主机写操作通过独立、单次授权的特权 Broker

主机安全页面的 UFW、Fail2ban、Linux 组件安装和 Windows Defender Firewall 写操作不再由
Web 进程直接调用系统工具。正式发布增加独立 `scriptboard-broker` 程序和受管服务；Web 只保留
只读探测，并通过本机 Unix Socket 或 Windows Named Pipe 请求固定动作。Linux socket 同时使用
0600 owner 和 `SO_PEERCRED` 校验，Windows Named Pipe 使用仅允许配置 Web 服务 SID 与 SYSTEM
的保护 DACL。协议拒绝未知字段、重复 JSON key、无界记录和未注册动作，不接受命令、可执行路径
或 shell 字符串。

每个动作使用两条独立 IPC 请求。Broker 首先用原始随机会话 token 在 SQLite 中重新验证用户启用
状态、认证版本、Administrator/Maintainer 角色、会话期限、12 小时空闲期限和十分钟内 step-up，
然后签发 30 秒、单次使用的 256-bit capability。capability 绑定服务端 Request ID、动作、资源、
资源 revision 和精确参数 SHA-256；执行请求先消费 capability，再由强类型执行器证明资源/revision
与解码参数一致。Windows 规则修改还重新读取当前规则并比较完整基线，UFW apply 继续在执行点
比较活动规则与默认策略。替换参数、替换资源、过期或重放都不会到达系统调用。

Broker 在执行前独立写入 `attempted` 审计；意图审计或外部签名 checkpoint 刷新失败时不执行。
执行后再写 `succeeded`/`failed`，两次记录均带用户、角色、Request ID 和认证保证，并立即刷新
ADR-0145 的外部 checkpoint。checkpoint 写入使用跨进程锁，只采纳同一受保护签名 key 签出的、
仍属于当前完整链且事件 ID 单调前移的其他进程 checkpoint。

这是 P0-02 的协议、进程和首批调用迁移切片，不是身份拆分完成声明。ADR-0147 已进一步把受管
Web 改为 Linux `scriptboard-web` 和 Windows `LocalService` + 独立服务 SID，Broker 继续使用
root/LocalSystem；获得 Web 进程代码执行已不再直接获得 Broker 的主机权限。后续仍必须把凭据
解封、Host Files 和 Run 各自移出 Web，并为 Runner 与 AI Runtime 建立独立身份和目录 ACL；
完成这些验收前 P0-02 保持“部分完成”。

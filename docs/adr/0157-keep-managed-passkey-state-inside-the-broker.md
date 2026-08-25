# 将受管 Passkey 公钥凭据状态限制在 Broker 内

受管 Web 不再直接打开、解封或改写 WebAuthn Passkey 状态。Broker 持有加密的公钥凭据、认证器计数器、克隆告警和备份状态；Web 通过受 OS peer identity 保护的本地 IPC 调用 `User`、`List`、`Add`、`Update`、`Delete` 与 `Reset` 领域操作。认证器私钥始终留在 Windows Hello、安全密钥或用户设备中，从不进入 ScriptBoard。

变更操作必须携带当前 Web 请求的原始会话令牌和 Request ID。Broker 重新查询权威数据库，要求 Administrator/Maintainer、有效会话及十分钟内的近期认证，并要求会话用户与被修改的凭据用户完全一致；意图和结果继续进入 Broker 审计链。无授权、过期 step-up 或跨用户调用均在触达凭据存储前拒绝。读取公钥凭据和登录后的认证器计数器更新不要求会话，因为登录尚未建立会话，但协议仍只接受固定结构。

登录验证返回的更新只能改变 WebAuthn 规范要求持久化的 `SignCount`、`CloneWarning` 和 `BackupState`。Store 会拒绝替换公钥、凭据 ID、AAGUID、传输、认证附件、attestation、备份资格或其他注册身份字段，因此被利用的 Web 不能把一次普通计数器更新退化为凭据替换。单个凭据 JSON 限制为 64 KiB、每用户最多 10 个，Broker 响应限制为 1 MiB。

协议拒绝任意参数 payload、通用密文、MFA 字段和宿主特权动作。集成测试用不同的 Web State Root 和 Broker 凭据根确认账户页面可读取 Broker 侧 Passkey，而 `account-passkeys.enc` 不会出现在 Web 侧。Broker 不可用时相关页面和登录路径 fail closed，不回退到本地 Store。

这一迁移仍不代表 P0-02 完成。MySQL、External Interface/远程连接凭据和 Host Files 特权仍需独立的代理或执行边界；Passkey 断言验证目前仍在 Web 中完成，Broker 仅以不可变身份校验约束其计数器更新，因此未来可进一步把完整 assertion ceremony 下沉为 Broker 领域服务。

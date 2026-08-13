# 将受管 MFA 状态限制在 Broker 内

受管 Web 不再直接打开、解封或改写 TOTP 与恢复码状态。MFA 的密文文件和通用主密钥由特权 Broker 持有，Web 只通过受 OS peer identity 保护的本地 IPC 调用五个领域操作：查询状态、开始注册、确认注册、验证一次性凭据和重置账户 MFA。

协议为每个操作定义独立的字段约束，拒绝 capability、宿主特权动作、任意参数载荷和通用密文。Broker 不提供 `Seal`、`Unseal`、任意 purpose 或任意明文输入接口，因此该边界不能被复用为其他凭据域的解密 oracle。注册 secret 和恢复码只会作为对应领域操作的预期结果返回；普通状态与验证调用不会返回持久秘密。

本地非受管模式继续使用进程内 MFA store，保持开发和便携运行方式。正式受管布局以及启用远端 Host fixture 的测试布局必须显式注入 Broker-backed MFA；Broker 不可用时登录、step-up 和 MFA 设置 fail closed，不回退到 Web 本地解封。

集成测试使用不同的 Web State Root 和 Broker 凭据根完成 MFA 注册，确认 `account-mfa.enc` 只在 Broker 侧产生。协议测试覆盖全部五种操作、领域错误映射，以及对通用密文、宿主动作和无关字段的拒绝。

该决定只迁移 MFA 领域，不代表 P0-02 已完成。Passkey 随后由 ADR-0157 迁入 Broker；Assistant Provider、MySQL、External Interface/远程连接凭据以及 Host Files 特权仍需分别改造成验证、代理或执行型接口；不得以返回任意明文的通用 Broker RPC 代替这些迁移。

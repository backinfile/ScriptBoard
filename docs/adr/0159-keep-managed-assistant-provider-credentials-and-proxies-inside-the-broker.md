# 将受管 Assistant Provider 凭据与代理会话限制在 Broker 内

受管 Web 不再打开、解封或读取 Assistant Provider API Key，也不再在自身进程中运行持有真实上游凭据的 Provider 代理。Broker 持有加密的 Provider 记录，把模型 ID、Owner、共享策略、Provider 类型、精确模型、Endpoint 与完整凭据原子绑定。Web 数据库继续保存不含秘密的展示和会话元数据；创建、更新与删除必须携带当前请求的原始会话令牌和 Request ID，由 Broker 重新验证 Administrator/Maintainer、有效会话与十分钟内的近期认证，并记录独立的意图和结果审计。

启动 Provider 会话只接受模型 ID 和有效当前会话。Broker 根据 Owner 或共享策略授权调用者，从内部记录启动仅监听 IPv4 环回的短期代理，并只向 Web 返回环回地址、随机模型 capability 与随机撤销句柄。代理只接受当前 Provider 对应的 POST 推理路径，要求请求模型与绑定模型完全一致，限制请求/响应和头部大小，使用统一 OutboundPolicy 固定 DNS 与目标地址，拒绝环境代理和重定向，并在内部注入真实认证头。撤销只接受 256-bit 随机句柄；未知句柄幂等成功，Broker 关闭或十五分钟上限到达时会回收所有会话。

IPC 只接受固定 `Store`、`Delete`、`Start` 和 `Stop` 字段，拒绝通用 payload、MFA、Passkey、远程网站或宿主动作字段。每条凭据、整个存储、记录数量及同时活动的代理会话都有独立上限。创建采用随机模型 ID；Broker 写入成功但数据库提交失败时尝试清理不可达记录。更新若出现跨进程提交不确定性，数据库与 Broker 的模型绑定不一致会让代理模型校验 fail closed，管理员可重新保存恢复一致状态。删除先校验数据库引用与默认状态，再删除 Broker 凭据并提交元数据删除；数据库失败只留下不可用元数据，重试仍可完成。

升级时 Broker 同时读取旧加密 `assistant-provider.enc` 与更早的明文 JSON，按数据库中每个 `credential_configured` 模型绑定 Owner、共享策略、Provider、模型和 Endpoint。任何已配置模型缺少可恢复凭据都会拒绝启动；新记录完整持久化后才删除旧文件。受管 Web 构造 Assistant 服务时不再初始化或迁移本地 Provider store，运行时也不能调用 `ModelCredential`。

这项决定收窄 ADR-0135：Pi 仍只看到会话能力和环回代理，但持有真实凭据的代理从 Web 进程迁入 Broker。不同 Web/Broker 凭据根的集成测试确认密文只在 Broker 侧产生，代理注入真实认证且响应不暴露凭据；外部 Chrome 验证列表与编辑表单不回显 Key，Broker 下线时保存受控失败且元数据保留。P0-02 仍未完成；MySQL 可恢复凭据与 Host Files 特权仍需迁入独立边界。

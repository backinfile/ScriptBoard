# 使用 State Root 外 Ed25519 checkpoint 锚定审计链

SQLite 内的串行 SHA-256 链可以发现中间记录修改、删除和普通截断，但单独依赖同一数据库中的
`audit_chain_state.tail_hash` 不能发现攻击者同时删除链尾并把链尾状态回退到较早有效摘要。为给
这类“看起来仍然有效”的回退增加独立证据，ScriptBoard 为每个 State Root 维护一个外部签名
checkpoint。

checkpoint 记录实例路径身份、已验证链尾的事件 ID、SHA-256、签名时间和 Ed25519 公钥，并由
专用 Ed25519 私钥签名。私钥不进入 State Root，而是由 ADR-0143 的外部主密钥以独立用途绑定
AEAD 密封；密钥密文和 checkpoint 都保存在 State Root 同级受保护的 `secrets` 目录。仅复制或
篡改 State Root 的攻击者因此不能生成一个与回退数据库匹配的新签名 checkpoint。

应用在任何初始化写入和保留清理前验证完整本地链、签名、实例身份、公钥绑定以及 checkpoint
事件仍属于本地链；任一失败都拒绝启动。受管部署中的校验与刷新按 ADR-0155 交给 Broker，Web
不再解封 Ed25519 私钥；保留清理后、每五分钟以及正常关闭时仍刷新 checkpoint。只读
`audit verify`、应急取证导出和安装初始化等本机高权限/离线流程继续直接验证同一外部锚点，缺失
时失败且不得创建新的信任材料；`doctor` 分别报告外部主密钥、签名密钥密文和 checkpoint 是否
存在且为普通文件。

这是主机本地的独立存储边界，不是远端不可回滚日志。最新一次 checkpoint 后、异常退出前的短暂
审计尾部仍可能没有外部锚定；拥有外部 secrets 目录和主密钥访问权的本机高权限攻击者仍能破坏
该边界。远端签名 checkpoint、集中转发、告警和不可变存储继续作为 P0-11 后续工作，完成前不得
宣称具备远端防篡改审计。

# 在 ScriptBoard 实例之间只读共享网站监控

> 已被 [ADR-0174](./0174-retire-cross-instance-website-monitoring.md) 取代。

> Endpoint 的 HTTP 兼容已被 [ADR-0141](./0141-expire-monitor-tls-exceptions-and-require-https-aggregation.md) 取代；远程 Key 的 State Root 内主密钥保存方式已被 [ADR-0143](./0143-seal-recoverable-secrets-with-an-external-host-key.md) 取代。

ScriptBoard 在 External Interface 中增加 `website_monitor` 条目。该条目只接受带 `Authorization: Bearer` 的 `GET /trigger/{group}/{name}`，返回当前实例全部未删除网站监控的列表快照、状态计数、最近检查证据与有界的 24 小时可用性，不开放创建、检查、暂停、恢复、编辑、排序或删除能力。响应继续受 External Interface 的启停、到期、速率限制、请求记录与审计约束，并禁止缓存。

另一个 ScriptBoard 实例可在网站监控页保存来源名称、完整调用 URL 与 Key。来源元数据保存在 SQLite；完整 Key 使用现有 External Interface 主密钥经 AES-GCM 加密后保存在 State Root 的私有密钥文件中，不进入 SQLite、HTML、审计或诊断日志。出站请求只允许明确的 `http` 或 `https` URL，不携带 URL 用户信息，不跟随重定向，固定 10 秒超时、4 MiB 响应上限和 20 个远端来源上限。非回环部署应使用 HTTPS。

远端数据在独立、明确标记为“只读”的列表中展示，远端 ID 不映射为本地监控 ID，也不生成本地详情或修改路由。远端不可达、Key 失效、TLS 校验失败或响应不符合版本化结构时，只把该来源标记为不可用，不影响本地监控调度和页面其余内容。本决策扩展 [ADR-0127](./0127-expose-only-bounded-external-trigger-actions.md) 的有界入口，但不形成通用 REST API、实例联邦、远端控制、推送通知或长期聚合存储。

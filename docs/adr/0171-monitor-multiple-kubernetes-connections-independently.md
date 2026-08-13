# 独立监控多个 Kubernetes 连接

ScriptBoard 允许管理员或维护员保存多个 Kubernetes 集群连接。Kubernetes 页面拆分为“集群连接”和“集群监控”两个页签：存在连接时默认显示监控页签，并通过下拉框切换具体集群；没有连接时默认显示连接页签。连接管理与监控内容保持分离，避免把连接配置混入日常观测路径。

每个连接拥有稳定且不可由显示名称推导的 ID。运行时客户端、当前快照、刷新互斥、版本历史和分钟指标均以该 ID 隔离；页面上的筛选、详情、日志与有限操作路由也必须携带连接 ID。修改某个连接并检测到 API Server/CA 指纹变化时，只清空该连接的历史，不影响其他连接。

schema 48 把 `kubernetes_connection` 从单例改为以 `id` 为主键的多行表，并为 `kubernetes_versions` 与 `kubernetes_metric_minutes` 增加 `connection_id` 复合主键和外键。升级 schema 38–47 时，原单例连接获得固定旧连接 ID，已有版本与指标历史归入该连接，以保留连续性。

本决策只扩展连接数量和监控切换，不扩大 [ADR-0166](./0166-monitor-one-kubernetes-cluster-with-bounded-operations.md) 的操作面：连接仍默认只读，“有限操作”仍只包含滚动重部署、Deployment/StatefulSet 副本数单步 `±1` 和从 CronJob 创建一次 Job；不提供跨集群编排、资源生命周期管理或通用 Kubernetes 控制面。

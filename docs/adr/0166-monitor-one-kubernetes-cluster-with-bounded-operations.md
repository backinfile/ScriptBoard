# 连接一个 Kubernetes 集群并限制操作面

> 传输协议限制已由 [ADR-0167](./0167-support-explicit-secure-and-plaintext-transports.md) 调整：kubeconfig `server` 现在同时接受 HTTP 与 HTTPS；本 ADR 的单集群和操作面限制保持不变。

ScriptBoard 在独立的“Kubernetes 监控”页面连接一个集群。连接由显示名称、主机上的 kubeconfig 路径、可选 context 和操作模式组成；SQLite 不保存 token、证书或私钥。连接适配器接受 HTTP 与 HTTPS，拒绝 `insecure-skip-tls-verify`、`exec` 与 `auth-provider` 凭据插件；HTTPS 使用 kubeconfig 中的 CA、静态 token、基本认证或客户端证书，HTTP 可使用静态 token 或基本认证并明确承担明文传输风险。

监控以 `namespace/kind/name` 作为工作负载的稳定身份，覆盖 Deployment、StatefulSet、DaemonSet 与 CronJob。Pin、分钟指标和镜像/revision 版本历史共享这一个身份，因此滚动发布或容器实例变化不会拆散历史。保存指向不同 API Server/CA 指纹的连接时，必须清空 Pin、指标和版本历史，避免不同集群共用时间线。产品不提供多集群选择器。

默认模式只读采集工作负载、Pod、Node、metrics.k8s.io、Event 和按需 Pod 日志。管理员或维护员可显式启用“有限操作”，且界面只暴露滚动重部署、Deployment/StatefulSet 副本数单步 `±1` 和从 CronJob 创建一次 Job；每次操作重新校验连接模式、探测到的 RBAC 能力、资源类型和目标，并写审计记录。不提供 YAML 编辑、删除资源、终端、Port Forward、Secret 浏览、任意扩缩容、Helm 或通用 Kubernetes 编排。

该能力扩展 [ADR-0118 应用与本机 Docker 容器只读观测](./0118-observe-host-applications-and-local-docker-containers.md)，但不把应用页变成容器控制面，也不改变 ScriptBoard 单主机、单实例的部署边界。

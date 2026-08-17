# 受管容器与 Kubernetes 访问经由特权 Broker

受管部署不再让低权限 Web 进程直接打开 Docker Unix Socket、Windows Named Pipe 或建立 Kubernetes API 连接。Privileged Broker 以现有 root/LocalSystem 身份持有这些运行时访问能力，并通过版本化本机 IPC 暴露固定领域接口；便携模式没有 Broker，继续使用启动用户可访问的本机运行时。Web 仍可管理 State Root 中由管理员明确导入的 kubeconfig 文件，但只有 Broker 使用其中凭据访问集群。

应用接口只包含快照、运行时详情、Docker 日志和已有的启动、停止、重启操作。日志实时读取拆成一秒有界批次，单次响应同时限制事件数和正文大小。Kubernetes 接口只包含连接能力检测、快照、工作负载详情、Pod 日志，以及 [ADR-0166](./0166-monitor-one-kubernetes-cluster-with-bounded-operations.md) 已允许的滚动重部署、单步副本调整和 CronJob 单次运行。接口不接受任意 Docker 或 Kubernetes 请求、远程 Docker endpoint、kubectl 命令、YAML、Secret 或终端操作。

后台快照和已登记集群的只读查询只依赖受保护 IPC 的 Web peer 身份；Broker 按连接 ID 从共享数据库解析并精确匹配路径、Context 和模式。新增、测试或更新连接时允许携带当前 Web Session 打开尚未保存的候选配置，但 Broker 重新验证 Administrator/Maintainer 角色，并拒绝引用外部 CA、token、客户端证书或私钥文件的候选 kubeconfig，防止把特权文件读取变成凭据转发通道。Docker 与 Kubernetes 状态修改复用 ADR-0146 的近期 step-up、30 秒单次 capability 和执行前意图审计；Broker 还重新验证已保存连接、运行时能力、工作负载类型和单步副本差值。Kubernetes 凭据只在 Broker 从管理员明确配置的绝对 kubeconfig 路径打开连接时使用，不通过 IPC 返回；默认部署获得访问链路不等于自动发现或导入登录用户凭据。

Windows Setup 和 Linux `.run` 不增加组件、端口或必填配置。Broker 已是同版本发布单元中的常驻服务，因此升级只提升内部 IPC 协议并整体替换 Web 与 Broker。Docker 或 Kubernetes 不存在、身份无权访问或目标不可达时，只降低对应数据源，不阻止 ScriptBoard 启动。安装和发布门禁必须保留 Web 低权限身份，并验证 Broker 可以建立本机运行时连接；不得通过提升 Web、挂载通用代理或开放未认证 TCP Docker API 来修复访问问题。

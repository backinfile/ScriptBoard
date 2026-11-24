# Kubernetes 监控界面设计

[打开交互原型](../../previews/kubernetes-monitoring.html) · [列表视图](../../previews/kubernetes-monitoring.html#list) · [节点分布](../../previews/kubernetes-monitoring.html#placement)

## 设计方向

日常巡检打开列表，定位异常后切到拓扑追踪关系。两种视图共享集群、命名空间、物理机、搜索、异常筛选和资源详情。沿用 ScriptBoard 深色导航与浅色资源区，拓扑画布用深色底与对齐的资源列，让关联线和异常节点保持清晰。

## 调研取舍

2026-11-24 查阅官方文档和代码仓库。GitHub 星标用于观察开源项目的社区关注度。

| 参考 | 关注度 | 采用的交互 |
| --- | --- | --- |
| Headlamp | GitHub 约 7,700 星 | 按资源范围查看关系图，点节点进入资源详情。采用异常筛选与按需展开，缩小排查范围。［[项目](https://github.com/kubernetes-sigs/headlamp) · [Map View](https://headlamp.dev/blog/2025/01/17/making-sense-of-kubernetes-with-headlamps-map-view/)］ |
| Argo CD | GitHub 约 24,700 星 | 用固定层级表达控制关系，资源节点显示健康状态。采用沿控制器查到异常 Pod 的交互。［[项目](https://github.com/argoproj/argo-cd) · [资源树设计](https://github.com/argoproj/argo-cd/wiki/Application-details-page-design)］ |
| Lens | 官方 2024 年产品资料披露超过 100 万用户 | 紧凑资源列表配侧栏详情，让用户在同一位置查看 Pod、指标和事件。［[产品资料](https://lenshq.io/assets/lens-datasheet.pdf) · [界面示例](https://lenshq.io/blog/lens-kubernetes/)］ |
| Grafana | GitHub 主项目约 78,100 星 | 利用集群、命名空间、节点和工作负载连续下钻，维持筛选上下文。［[项目](https://github.com/grafana/grafana) · [Kubernetes Monitoring 导航](https://grafana.com/docs/grafana-cloud/observe-and-act/monitor-infrastructure/kubernetes-monitoring/navigate-k8s-monitoring/)］ |

## 列表视图

- 集群状态、版本、最近采集时间、手动与自动刷新合并到一处。下方显示工作负载、Pod 就绪数、节点和待关注工作负载。点击待关注数量开关异常筛选。计数按所选集群、命名空间和物理机计算，搜索和异常筛选只改变结果区。
- 默认打开工作负载表。工作负载、网络、节点分别用资源页签切换。表格默认预览名称、命名空间、状态、就绪副本、所在节点、CPU、内存、重启。正式实现沿用现有排序、显示列、镜像和最近启动时间、行操作、日志入口。
- 可按物理机筛选工作负载、关联网络资源和节点。节点表显示可排序的「物理机」列，节点详情显示归属。筛选后 Pod 范围随物理机收窄，工作负载级状态、总副本和指标仍按整个对象显示。
- 点击行打开详情。宽屏显示并排详情，小屏以抽屉显示。工作负载详情沿用 Pod、事件、版本历史和指标。Pod 日志走现有能力校验。保留“集群连接”和“本地管理”入口。

## 拓扑视图

- **资源关系**按 Ingress、Service、工作负载、Pod 四列布局。选中资源后高亮相邻关系，可点任一相邻资源跳转。控制器链默认归并到工作负载，Pod 分组按控制器归属聚合，在同一位置先显示异常实例，再展开其余实例。
- **节点分布**按工作负载、Pod、物理机内的 Node 布局。使用 Pod 实际调度的 Node 连线，同一物理机的多个 Node 放在一组。点击物理机分组可筛选列表与拓扑。待调度 Pod 在全部物理机视图中保留在 Pod 列，可在详情查看原因。选中物理机后仍可查看该机上当前命名空间没有运行 Pod 的 Node。
- 列表和图使用同一个所选资源。异常筛选保留与异常对象相连的路径和必要的分组，搜索命中为中心，保留用来定位的上下文；列表只显示匹配项。图支持拖拽、缩放、适应视口和键盘选择。刷新按稳定身份更新资源，保留位置、选择及缩放。
- 原型示例路径为 `checkout-api → checkout-… → BackOff → Pod 日志`。节点分布显示 `checkout-api` 的两个副本分别在 `worker-a` 和 `worker-b`，两台 Node 同属 `edge-host-01`。展开 `catalog-api` 可看到跨物理机的部署。移动端从所选资源附近打开拓扑，可拖动、缩放或适应全图，点击资源打开抽屉。

## 关系数据

| 在图上显示 | 取值方式 |
| --- | --- |
| Ingress → Service | 使用同命名空间的 Ingress 规则及默认后端，详情展示 Host、Path 和端口。现有快照已解析这些字段。［[Ingress](https://kubernetes.io/docs/concepts/services-networking/ingress/)］ |
| Service → Pod | 使用同命名空间关联 Service 的 EndpointSlice，以 Pod 类型的 `targetRef` 建边，聚合所有切片并去重。切片读取失败、无端点、非 Pod 后端分别呈现；端点条件在详情展示。外部地址型 Service 仍作为独立资源。［[EndpointSlice](https://kubernetes.io/docs/reference/kubernetes-api/discovery/endpoint-slice-v1/)］ |
| 控制器 → Pod | 复用采集中已有的控制器 UID 归属解析，保留 Deployment → ReplicaSet → Pod、CronJob → Job → Pod 的中间链。StatefulSet 和 DaemonSet 使用直接归属。［[Owners and Dependents](https://kubernetes.io/docs/concepts/overview/working-with-objects/owners-dependents/)］ |
| Pod → Node | 使用 `spec.nodeName`。字段为空时显示待调度；字段有值但 Node 采集不可用时，在详情标记该 Node 信息不可用。［[Pod 节点分配](https://kubernetes.io/docs/concepts/scheduling-eviction/assign-pod-node/)］ |
| Node → 物理机 | 每个集群连接可指定一个由运维维护的 Node 标签键，标签值使用稳定的物理机标识。缺少映射的 Node 显示在「物理机未标记」区。Kubernetes Node 可以是虚拟机或物理机，`kubernetes.io/hostname` 表示 Node 主机名，物理机归属使用显式映射。［[Nodes](https://kubernetes.io/docs/concepts/architecture/nodes/) · [Node 标签](https://kubernetes.io/docs/reference/node/node-labels/)］ |

当前快照输出工作负载汇总、外部 Service、Ingress 和 Node。Pod 级归属在采集中存在，部分 Pod 信息目前通过工作负载详情取得。正式实现需从同一次采集中输出轻量关系索引，并给拓扑读取内部 Service 和 EndpointSlice 的能力。内部 Service 仍可在网络页签用类型筛选。新增采集遵循现有连接隔离、刷新上限和 RBAC 探测，EndpointSlice 无读取权限时保留其他可确认的边。

正式实现用连接 ID、命名空间、类型和名称维持页面选择，用 UID 验证对象归属，避免同名资源重建时短暂串线。快照部分失败时在对应资源区标明采集缺失，指标不可用显示 `—`，上一次可用快照显示采集时间。全命名空间先按命名空间及工作负载聚合，再按选择展开所在分组。大规模布局、采集权限和各连接模式接入后用真实集群验证。

## 交互验收

1. 打开监控即可看到工作负载状态，选中待关注后列表与图能显示同一问题。
2. 从某命名空间内的负载切换到拓扑及节点分布，原选择、搜索、命名空间和物理机筛选保留。
3. 图上能区分服务路由、控制器归属、Pod 调度。沿异常分支可以到达事件及示例日志。
4. 切换集群时清理旧集群选择，网络与节点分类支持打开详情，空结果可恢复筛选。桌面、小屏都可操作。
5. 同一物理机下可显示多个 Node。点 `edge-host-02` 后，列表和图只显示该机的节点及关联资源。切换到 staging 可查看「物理机未标记」分组。

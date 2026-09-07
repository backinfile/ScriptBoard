# Kubernetes 受控组件管理实施计划

## 1. 目标

在 ScriptBoard 中增加一个面向已有 Kubernetes 连接的“组件管理”能力。管理员或维护员先从现有连接列表中选择一个 k3s、标准 Kubernetes 或由本机 k3d 创建的集群，再对该连接执行组件预检、安装、升级、卸载、状态查看和故障恢复。

k3d 只作为可选的本地环境创建器：创建集群后将 kubeconfig 导入现有连接模型，此后的监控与组件管理不再区分 k3d、k3s 或其他 Kubernetes 发行版。

首批受控组件：

- 集群内 Docker Registry
- MySQL
- Redis
- Prometheus，以及按 Kubernetes Label Selector 创建监控目标
- VictoriaLogs，以及日志采集器

## 2. 产品边界

### 2.1 本期包含

- 在所有 Kubernetes 相关页面复用同一个连接选择器。
- 对每个连接单独启用“受控组件管理”，不改变已有 `observe` / `limited` 操作语义。
- 内置、版本锁定、经过验证的组件目录。
- 安装前能力检查、变更计划、近期认证确认和审计。
- 持久化安装任务；页面断线或 Web 重启不应使任务状态丢失。
- 通过 Kubernetes API 对账实际状态，显示 Ready、Degraded、Failed 等状态。
- 显式保留 kubeconfig 中的 HTTPS、HTTP 和 `insecure-skip-tls-verify` 选择，并展示相应风险。

### 2.2 本期不包含

- 任意 Helm 仓库、任意 Chart、任意 values 或 Helm 参数。
- 通用 YAML 编辑器、终端、Port Forward、Secret 浏览器或任意 Kubernetes 请求代理。
- 跨集群批量编排。
- 自动接管已有 Helm Release。
- 生产级数据库高可用、跨区容灾或数据库迁移服务。
- 对远端集群做节点、控制面或 Kubernetes 版本生命周期管理。
- 把镜像 tag 当作监控选择条件。监控选择条件统一使用 Kubernetes label。

## 3. 必须先完成的架构决策

现有 [ADR-0166](./adr/0166-monitor-one-kubernetes-cluster-with-bounded-operations.md)、[ADR-0171](./adr/0171-monitor-multiple-kubernetes-connections-independently.md) 和 [ADR-0173](./adr/0173-route-managed-container-and-kubernetes-access-through-the-broker.md) 明确排除了 Helm 与资源生命周期管理。因此实现前必须新增 ADR，记录以下决定：

1. 组件管理是新的显式授权能力，不并入也不暗中扩大 `limited` 模式。
2. Web 只提交受控领域请求，Privileged Broker 解析连接并执行安装；不得增加通用 `helm`、`kubectl` 或 shell IPC。
3. 只允许使用随 ScriptBoard 版本发布的组件目录；Chart 来源、版本、参数模式和资源范围均由目录约束。
4. 集群实时资源与 Helm Release 是运行状态事实来源；SQLite 只保存期望状态、任务状态、所有权和对账结果。
5. Secret 明文不写入 SQLite、日志或审计记录。

连接授权建议新增独立的 `addon_mode`：

| 值 | 含义 |
|---|---|
| `disabled` | 默认值，只保留连接现有能力 |
| `curated` | 允许安装和管理内置目录中的组件 |

不要新增一个含义混杂的 `managed` 操作模式。`operation_mode` 继续控制现有工作负载有限操作，`addon_mode` 单独控制组件生命周期，用户可分别授权。

## 4. 核心模型与模块划分

### 4.1 `clusteraddon` 深模块

新增 `internal/clusteraddon`，由它隐藏 Chart 选择、参数校验、预检、计划生成、任务状态机、对账、健康判断和恢复逻辑。Web 与 Broker 只学习一个小接口，而不理解每个 Chart 的实现细节。

建议外部接口：

```go
type Manager interface {
	Catalog(ctx context.Context, connectionID string) ([]CatalogEntry, error)
	Plan(ctx context.Context, request ChangeRequest) (ChangePlan, error)
	Start(ctx context.Context, approved ApprovedPlan) (Operation, error)
	GetOperation(ctx context.Context, operationID string) (Operation, error)
	ListInstallations(ctx context.Context, connectionID string) ([]Installation, error)
}
```

接口中的 `ChangeRequest` 只接受组件目录 ID、动作、命名空间、Release 名称和通过类型化模式验证的配置，不接受原始命令、Chart URL 或任意 YAML。

模块内部可包含目录解析、Helm 执行、Kubernetes 发现、任务存储和健康探测等内部 seam；这些细节不暴露给 Web。

### 4.2 现有模块职责

- `clusterstatus`：继续负责集群连接、快照、日志和已有有限操作，不扩展为通用 Helm 管理器。
- `kubeconfigmanager`：继续负责 kubeconfig 导入、检查和连接登记。
- `privilegebroker`：增加版本化、类型化的组件计划与执行消息，并重新校验用户、连接、授权能力和计划摘要。
- `processlaunch`：仅在 Broker 内部用于启动受信任且版本固定的 Helm 二进制。
- Web：负责连接选择、表单、计划展示、近期认证和任务状态展示，不直接访问 kubeconfig、Helm 或 Kubernetes API。

### 4.3 执行适配器

MVP 使用随发布包锁定版本的 Helm CLI 作为生产适配器，Broker 根据受控目录生成固定 argv 和临时 values 文件。这样可以复用 Helm 成熟的 Release、升级和回滚语义，同时避免 K3s 专用的 `HelmChart` CRD，使 k3s 与标准 Kubernetes 走同一条路径。

测试使用内存适配器验证 `clusteraddon.Manager` 的可观察行为。未来如需改用 Helm Go SDK，只替换内部适配器，不改变 Web 或领域接口。

不得把 Helm CLI 暴露成通用命令执行接口。

### 4.4 可选 `localcluster` 模块

本地 k3d 创建放在独立的 `internal/localcluster` 模块中，职责仅包括：

1. 检测 Docker 与 k3d。
2. 根据受控模板创建、删除或查看本地 k3d 集群。
3. 获取 kubeconfig 并调用现有连接登记流程。

成功登记后，`localcluster` 不参与组件安装。组件页只收到普通 `connection_id`。

## 5. 组件目录与具体配置

首版直接以现有五组自维护 Helm Chart 为实现基线，不引入第三方大而全的 Chart。目录以版本化静态文件或 Go 嵌入资源随 ScriptBoard 发布，不存成用户可修改的数据库记录。前端使用下面定义的领域配置项，不直接显示或保存 Chart 原始 values。

临时 CLI/Chart 的本地开发默认值进入 ScriptBoard 后做以下收敛：

| 临时实现 | ScriptBoard 目录决定 |
|---|---|
| Service 默认 NodePort | 默认 ClusterIP；用户显式选择 NodePort 后才使用原预设端口 |
| 使用 `local-*-password` 固定密码 | 默认生成随机密码并仅显示一次，也支持手工密码或既有 Secret |
| mysqld-exporter 使用 root | 安装时创建最小监控权限账户 |
| 连接文件包含明文凭据 | 连接摘要不包含密码，敏感信息不落 SQLite、日志或审计 |
| CLI 接受 Chart 路径和任意 values | 只接受目录 ID 和本节列出的类型化配置 |
| Prometheus 主要依赖注解发现 | 保留注解兼容规则，新增受控 Kubernetes label 规则 |

这不是重新设计五套工作负载；Chart 的资源结构、锁定镜像版本、端口、存储基线和探针继续沿用临时实现，差异只集中在多集群、安全默认值和面板配置接口。

每个目录条目至少包含：

```text
id
display_name
category
chart_asset
chart_version
workload_profile
kubernetes_version_range
supported_architectures
required_namespaced_permissions
required_cluster_permissions
storage_requirements
config_schema
sensitive_fields
health_checks
install_upgrade_uninstall_policy
```

Chart、默认镜像和可选镜像源均由目录锁定版本。升级 ScriptBoard 不自动升级集群组件，只提示有经过验证的新版本。

### 5.1 首批组件与重量分级

| 目录 ID | 实现 | 默认 namespace / release | 默认持久化 | 重量 | 公认度与适用范围 |
|---|---|---|---:|---|---|
| `distribution-single` | Docker Distribution 单副本 | `registry` / `distribution` | `20Gi` | 轻量 | 公认度高；适合开发、缓存和小团队私有仓库 |
| `redis-single` | Redis + redis-exporter | `data` / `redis` | `5Gi` | 轻量 | 使用广泛；适合开发和非高可用缓存 |
| `mysql-single` | MySQL + mysqld-exporter | `data` / `mysql` | `20Gi` | 中等 | 使用广泛；适合开发、小型业务和测试数据服务 |
| `victoria-logs-single` | VictoriaLogs + vlagent | `victoria-logs` / `k3s-logging` | `100Gi` | 中等 | 云原生日志领域认可度较高；资源占用低于大型日志栈 |
| `prometheus-single` | Prometheus + kube-state-metrics + node-exporter | `monitoring` / `prometheus` | `20Gi` | 偏重 | Kubernetes 监控事实标准，生态和用户量最大 |

上述五项均为单实例或单机型档位，不在界面中标记为“生产高可用”。后续的 Percona Operator、Redis Sentinel/Cluster、Prometheus Operator 和 `VLCluster` 应作为新的目录项，而不是给单机档位不断追加开关。

### 5.2 通用配置项

以下字段由所有 Kubernetes 组件共享：

| 配置键 | UI 名称 | 默认值 | 校验与行为 |
|---|---|---|---|
| `namespace` | 命名空间 | 各目录默认值 | DNS-1123 label；1–63 字符；已存在时复用 |
| `releaseName` | Release 名称 | 各目录默认值 | DNS-1123 label；同一连接和 namespace 内唯一 |
| `imageSource` | 镜像源 | `daocloud` | 枚举 `daocloud`、`upstream`；解析为目录内已锁定的完整镜像，首版不接受任意 repository/tag |
| `service.type` | 暴露方式 | `ClusterIP` | 枚举 `ClusterIP`、`NodePort`；远程集群不默认暴露 NodePort |
| `service.nodePort` | NodePort | 组件预设值 | 仅 `NodePort` 时出现；范围 `30000–32767`；安装前检查冲突 |
| `storage.className` | StorageClass | 空，使用默认类 | 必须来自目标集群发现结果；不接受自由输入不存在的名称 |
| `storage.size` | 存储容量 | 各目录默认值 | Kubernetes quantity；最小值按组件定义；升级只允许增大 |
| `storage.retainOnDelete` | 卸载时保留数据 | `true` | PVC 带 `helm.sh/resource-policy: keep`；改为删除需单独确认 |
| `resources.preset` | 资源档位 | `default` | 枚举 `small`、`default`、`custom` |
| `resources.*` | 自定义资源 | 仅 custom 显示 | CPU/memory quantity；request 不得大于 limit |

以下行为固定在目录实现内，不作为普通表单项：

- Helm 使用 `upgrade --install --create-namespace --wait --timeout 10m --history-max 10`。
- Chart 路径、Chart 版本、kubeconfig 和 context 不由表单传入；Broker 根据目录和 `connection_id` 解析。
- 所有凭据默认安全随机生成并仅显示一次；也可选择手工输入或引用目标 namespace 中的既有 Secret。
- 不生成包含明文凭据的 `artifacts/*-connection.yaml`。连接摘要可下载，但密码字段必须省略。
- 自定义原始 values、affinity、tolerations、priorityClass 和任意额外参数不进入 MVP。

### 5.3 VictoriaLogs + vlagent

目录 ID 为 `victoria-logs-single`，默认创建 1 个 VictoriaLogs StatefulSet、1 个 Service、1 个 PVC，以及每个可调度节点 1 个 vlagent DaemonSet Pod。vlagent 所需 ClusterRole 仅允许：Pod/Namespace 的 get/list/watch 和 Node 的 get。

#### 用户配置项

| 配置键 | UI 名称 | 默认值 | 校验与行为 |
|---|---|---|---|
| `server.imageSource` | VictoriaLogs 镜像源 | `daocloud` | `daocloud` 映射 `m.daocloud.io/docker.io/victoriametrics/victoria-logs:v1.52.0`；`upstream` 映射官方同版本镜像 |
| `collector.imageSource` | vlagent 镜像源 | `daocloud` | 映射 `victoriametrics/vlagent:v1.52.0` 的锁定镜像 |
| `retention` | 日志保留时间 | `30d` | 1–3650 天，最终写入 VictoriaLogs retention 参数 |
| `storage.size` | 日志存储 | `100Gi` | 最小 `5Gi`，ReadWriteOnce，数据目录 `/victoria-logs-data` |
| `service.type` | 访问方式 | `ClusterIP` | 可改 NodePort；预设 NodePort `30928` |
| `collector.enabled` | 部署日志采集器 | `true` | 关闭时只部署日志存储 |
| `collector.excludedNamespaces` | 排除命名空间 | `kube-system,kube-public,kube-node-lease` | namespace 多选；始终支持 Pod 注解 `logging.vlagent.io/exclude: "true"` |
| `collector.includedNamespaces` | 仅采集这些命名空间 | 空 | 与排除列表不能同时命中；空表示除排除项外全部 |
| `collector.podLabelSelector` | Pod 标签过滤 | 空 | 标准 Kubernetes Label Selector；空表示不过滤 |
| `collector.tolerateAllTaints` | 覆盖所有节点 | `true` | 关闭时不写入全量 Exists toleration |
| `server.resources.*` | VictoriaLogs 资源 | request `100m/256Mi`，limit `2/2Gi` | 支持资源档位覆盖 |
| `collector.resources.*` | vlagent 资源 | request `50m/64Mi`，limit `1/512Mi` | 支持资源档位覆盖 |

固定实现：日志格式 JSON；写入地址为集群内 Service 的 `/insert/native`；vlagent 使用 `emptyDir:/var/lib/vlagent` 缓冲并读取宿主 `/var/log`。两个工作负载均使用 UID/GID/fsGroup 65534、RuntimeDefault seccomp、禁止提权和丢弃全部 capabilities。VictoriaLogs readiness/liveness 使用 `GET /health`。

健康验收：StatefulSet Ready、PVC Bound、每个符合调度条件的 Node 均有 Ready vlagent Pod、`/health` 成功，且探针写入的一条测试日志可在限定时间内查询到。

### 5.4 Prometheus 监控栈

目录 ID 为 `prometheus-single`，采用现有轻量 Chart：Prometheus 单副本 StatefulSet、kube-state-metrics 单副本 Deployment 和 node-exporter DaemonSet。MVP 不安装 Prometheus Operator，也不依赖 ServiceMonitor/PodMonitor CRD。

#### 用户配置项

| 配置键 | UI 名称 | 默认值 | 校验与行为 |
|---|---|---|---|
| `server.imageSource` | Prometheus 镜像源 | `daocloud` | 映射 `prom/prometheus:v3.14.0` 的锁定镜像 |
| `server.retention` | 指标保留时间 | `15d` | 1–365 天 |
| `server.scrapeInterval` | 默认抓取周期 | `30s` | 15 秒–5 分钟 |
| `server.evaluationInterval` | 规则评估周期 | `30s` | 15 秒–5 分钟 |
| `server.webAuth.username` | Web 用户名 | `admin` | 1–64 字符；不写入审计正文 |
| `server.webAuth.passwordMode` | Web 密码 | `generate` | `generate`、`manual`、`existingSecret`；bcrypt 哈希进入 Web 配置 Secret |
| `storage.size` | TSDB 存储 | `20Gi` | 最小 `5Gi`，ReadWriteOnce，目录 `/prometheus` |
| `service.type` | 访问方式 | `ClusterIP` | 可改 NodePort；预设 NodePort `30090` |
| `kubeStateMetrics.enabled` | 集群对象指标 | `true` | 采集 Pod、Node、Namespace、Service、PV/PVC、Deployment、StatefulSet、DaemonSet、ReplicaSet、Job、CronJob |
| `nodeExporter.enabled` | 节点指标 | `true` | 需要 hostPID、hostPath 与 mount propagation，计划页必须突出显示 |
| `victoriaLogsTarget.enabled` | 抓取 VictoriaLogs/vlagent | `auto` | 枚举 `auto`、`true`、`false`；auto 仅在同连接发现受管日志栈时生成任务 |
| `discovery.excludedNamespaces` | 排除命名空间 | `kube-system,kube-public,kube-node-lease,monitoring,local-path-storage` | namespace 多选 |
| `server.resources.*` | Prometheus 资源 | request `100m/256Mi`，limit `2/2Gi` | 支持资源档位覆盖 |
| `kubeStateMetrics.resources.*` | KSM 资源 | request `50m/64Mi`，limit `1/512Mi` | 支持资源档位覆盖 |
| `nodeExporter.resources.*` | node-exporter 资源 | request `25m/32Mi`，limit `500m/256Mi` | 支持资源档位覆盖 |

锁定附属镜像为 kube-state-metrics `v2.19.1`、node-exporter `v1.12.1`。Prometheus Web lifecycle API 保持启用，但不直接暴露给用户；配置变更由 Broker 更新 Secret 后调用受控 reload。node-exporter 继续只读挂载 `/proc`、`/sys`、`/`，并排除 dev、proc、sys、Docker 与 kubelet 等挂载点。

#### 标签监控规则配置

每条规则都属于一个 `connection_id + prometheus installation_id`，由 ScriptBoard 生成原生 Prometheus `kubernetes_sd_configs` 和 relabel 配置：

| 配置键 | UI 名称 | 默认值 | 校验与行为 |
|---|---|---|---|
| `name` | 规则名称 | 必填 | 同一 Prometheus 安装内唯一，DNS-1123 label |
| `targetKind` | 目标类型 | `Pod` | 枚举 `Pod`、`Service`；Service 规则解析 EndpointSlice/Endpoints 后抓取后端 |
| `namespaces` | 命名空间范围 | 当前 namespace | 必须显式选择；“所有命名空间”需要额外确认 |
| `labelSelector` | 标签选择器 | 必填 | 标准 Kubernetes Label Selector；保存前预览匹配对象及数量 |
| `port` | 指标端口 | 必填 | 端口名或 1–65535 数字端口 |
| `path` | 指标路径 | `/metrics` | 必须以 `/` 开头，不允许 URL、查询参数或片段 |
| `scheme` | 协议 | `http` | 枚举 `http`、`https` |
| `interval` | 抓取周期 | 继承全局 `30s` | 15 秒–5 分钟 |
| `tls.mode` | TLS 校验 | `verify` | HTTPS 时可选 `verify`、`insecureSkipVerify`；后者必须明确提示中间人攻击风险 |
| `auth.mode` | 认证 | `none` | `none`、`basicSecretRef`、`bearerSecretRef`；只保存目标 namespace 中的 Secret 引用 |
| `enabled` | 启用规则 | `true` | 关闭后保留配置但不写入 Prometheus 抓取配置 |

为支持 Pod 规则，Prometheus RBAC 需要 Pod 的 get/list/watch；Service 规则还需要 Service、Endpoints 和 EndpointSlice 的 get/list/watch。权限按已启用规则计算，不预先授予未使用的范围。

兼容保留原有 `prometheus.io/scrape`、`prometheus.io/port`、`prometheus.io/path` 注解发现，作为一个可关闭的内置规则；新建规则统一使用 label，而不是镜像 tag 或自由 relabel YAML。

健康验收：Prometheus StatefulSet Ready、PVC Bound、启用的 KSM/每节点 exporter Ready、`/-/ready` 成功；每条标签规则显示匹配对象数、active targets 数和最近抓取错误。

### 5.5 MySQL + mysqld-exporter

目录 ID 为 `mysql-single`，创建 1 个 StatefulSet；MySQL 与 exporter 为同 Pod 两个容器。

| 配置键 | UI 名称 | 默认值 | 校验与行为 |
|---|---|---|---|
| `server.imageSource` | MySQL 镜像源 | `daocloud` | 映射 `library/mysql:8.4.11` 的锁定镜像 |
| `database.name` | 初始数据库 | `app` | 1–64 字符，仅允许 MySQL 标识符安全字符集 |
| `database.charset` | 字符集 | `utf8mb4` | 首版固定 |
| `database.collation` | 排序规则 | `utf8mb4_unicode_ci` | 首版使用目录允许列表 |
| `credentials.appUsername` | 应用用户名 | `app` | 1–32 字符，禁止保留账户名 |
| `credentials.appPasswordMode` | 应用密码 | `generate` | `generate`、`manual`、`existingSecret` |
| `credentials.rootPasswordMode` | root 密码 | `generate` | `generate`、`manual`、`existingSecret` |
| `storage.size` | 数据存储 | `20Gi` | 最小 `5Gi`，ReadWriteOnce |
| `service.type` | 访问方式 | `ClusterIP` | 可改 NodePort；预设 NodePort `30306` |
| `exporter.enabled` | Prometheus exporter | `true` | 端口 `9104`，Pod 自动带受控抓取标签/注解 |
| `server.resources.*` | MySQL 资源 | request `100m/512Mi`，limit `2/2Gi` | 支持资源档位覆盖 |
| `exporter.resources.*` | exporter 资源 | request `20m/32Mi`，limit `250m/128Mi` | 支持资源档位覆盖 |

固定端口为 MySQL `3306`、exporter `9104`。readiness 使用认证后的 `mysqladmin ping`；实现时补充 startup/liveness probe。原清单中 exporter 使用 root，纳入 ScriptBoard 时改为自动创建最小监控权限账户，且不向表单暴露该内部账户。

安装后的受控动作可提供数据库 list、create、drop、reset。drop/reset 要求近期 AAL2、输入目标数据库名称复核，并明确不会删除 PVC。系统库永远不可作为目标。

健康验收：StatefulSet Ready、PVC Bound、认证查询成功、exporter `/metrics` 成功且 Prometheus 已安装时 target 为 up。

### 5.6 Redis + redis-exporter

目录 ID 为 `redis-single`，创建 ConfigMap、Service、PVC 和单副本 StatefulSet；Redis 与 exporter 为同 Pod 两个容器。

| 配置键 | UI 名称 | 默认值 | 校验与行为 |
|---|---|---|---|
| `server.imageSource` | Redis 镜像源 | `daocloud` | 映射 `library/redis:7.2.4-alpine` 的锁定镜像 |
| `credentials.username` | ACL 用户 | `default` | 非 default 时关闭默认用户；用户名不得含空白和 ACL 分隔字符 |
| `credentials.passwordMode` | ACL 密码 | `generate` | `generate`、`manual`、`existingSecret` |
| `persistence.aofEnabled` | AOF 持久化 | `true` | 首版支持开关 |
| `persistence.appendFsync` | AOF 刷盘策略 | `everysec` | 枚举 `always`、`everysec`、`no`，界面说明性能/持久性取舍 |
| `databaseCount` | 逻辑数据库数量 | `16` | 1–128；升级减少数量前检查被移除编号是否非空 |
| `storage.size` | 数据存储 | `5Gi` | 最小 `1Gi`，ReadWriteOnce |
| `service.type` | 访问方式 | `ClusterIP` | 可改 NodePort；预设 NodePort `30379` |
| `exporter.enabled` | Prometheus exporter | `true` | 端口 `9121`，复用 Redis Secret，Pod 自动带抓取标签/注解 |
| `server.resources.*` | Redis 资源 | request `50m/128Mi`，limit `1/1Gi` | 支持资源档位覆盖 |
| `exporter.resources.*` | exporter 资源 | request `20m/32Mi`，limit `250m/128Mi` | 支持资源档位覆盖 |

固定服务端口为 `6379`，数据目录 `/data`。readiness 使用带 ACL 认证的 `redis-cli ping`，实现时补充 startup/liveness probe。

安装后的受控动作可提供逻辑库 list/size/clear/clear-all。clear 与 clear-all 要求近期 AAL2，并分别确认数据库编号或安装名称；操作审计不记录密码或 key 内容。

健康验收：StatefulSet Ready、PVC Bound、带认证 PING 成功、exporter `/metrics` 成功且 Prometheus 已安装时 target 为 up。

### 5.7 Docker Distribution（集群内）

目录 ID 为 `distribution-single`，创建 htpasswd Secret、Service、PVC 和单副本 Deployment，更新策略为 `Recreate`。

| 配置键 | UI 名称 | 默认值 | 校验与行为 |
|---|---|---|---|
| `server.imageSource` | Registry 镜像源 | `daocloud` | 映射 `library/registry:2` 的锁定镜像 |
| `credentials.username` | 用户名 | `developer` | 1–64 字符，不允许冒号、换行和控制字符 |
| `credentials.passwordMode` | 密码 | `generate` | `generate`、`manual`、`existingSecret`；手工密码禁止换行且 UTF-8 不超过 bcrypt 72 bytes |
| `storage.size` | 镜像存储 | `20Gi` | 最小 `5Gi`，ReadWriteOnce，目录 `/var/lib/registry` |
| `service.type` | 访问方式 | `ClusterIP` | 可改 NodePort；预设 NodePort `30500` |
| `deleteEnabled` | 允许删除镜像 | `true` | 控制 `REGISTRY_STORAGE_DELETE_ENABLED` |
| `tls.mode` | Registry TLS | `disabled` | 枚举 `disabled`、`existingSecret`；远程环境选择 HTTP 时显示明文与凭据泄露风险 |
| `tls.secretName` | TLS Secret | 空 | TLS 开启时必填；必须位于目标 namespace 且包含 `tls.crt`、`tls.key` |
| `resources.*` | Registry 资源 | request `50m/64Mi`，limit `1/512Mi` | 支持资源档位覆盖 |

TLS Secret 的 `resourceVersion` 写入 Pod template annotation，证书更新后触发滚动替换。readiness 使用 TCP `5000`，实现时增加对 `/v2/` 的认证健康检查。

健康验收：Deployment Ready、PVC Bound、带认证访问 `/v2/` 返回成功；如启用 TLS，还要验证证书链或明确记录用户选择的跳过校验策略。

### 5.8 k3d Registry（集群外）

k3d Registry 不属于 Kubernetes 组件目录，不显示在任意已连接 k3s/k8s 的安装列表中。它只出现在“创建本地 k3d 环境”的高级选项中：

| 配置键 | 默认值 | 行为 |
|---|---|---|
| `registry.enabled` | `true` | 创建由 k3d 管理的 Docker Registry 容器 |
| `registry.name` | `<cluster>-registry.localhost` | 受 cluster 名称派生，不接受任意容器名 |
| `registry.hostPort` | `15000` | 范围 1–65535，创建前检查占用 |
| `registry.imageSource` | `daocloud` | 映射锁定的 `registry:2` 镜像 |

创建完成后界面分别显示宿主推送地址和集群内拉取地址，但不把它登记成 `distribution-single` 安装实例。

### 5.9 本地 k3d 环境配置

本节只定义可选环境创建器，不影响对已有集群的组件管理：

| 配置键 | 默认值 | 校验与行为 |
|---|---|---|
| `clusterName` | `dev` | DNS-1123 label，本机 k3d 集群名唯一 |
| `serverCount` | `1` | MVP 固定 1 |
| `agentCount` | `0` | 0–8；超过 0 时明确增加本机资源占用 |
| `k3sVersion` | `v1.34.10-k3s1` | 仅能选择目录验证过的版本 |
| `imageSource` | `daocloud` | 映射 K3s、k3d proxy 和 tools 的锁定镜像 |
| `registry.*` | 见 5.8 | 可选宿主 Registry |
| `portMappings` | 空 | 可从组件预设选择宿主端口到 NodePort 的映射；宿主端口范围 1–65535 |

内置端口映射预设如下，默认不启用，由用户在创建集群前勾选：

| 用途 | 默认宿主地址 | 目标 NodePort |
|---|---|---:|
| VictoriaLogs | `127.0.0.1:19428` | `30928` |
| Prometheus | `127.0.0.1:19090` | `30090` |
| MySQL | `127.0.0.1:13306` | `30306` |
| Redis | `127.0.0.1:16379` | `30379` |
| 集群内 Distribution | `127.0.0.1:15001` | `30500` |

组件 NodePort 映射只能在创建 k3d 集群时声明。已存在集群缺少映射时，组件安装仍可完成，但界面不得声称宿主机端口可访问；增加映射需要明确提示重建本地集群及其数据影响。

生成 kubeconfig 后检查 context 和 `/readyz`；Windows 下只对确认属于本次 k3d 集群的 server 地址执行 `host.docker.internal` 到 `127.0.0.1` 的转换，然后通过现有 kubeconfig 导入流程登记普通连接。

### 5.10 明确暂缓的配置

以下能力不通过“高级配置”混入首版，而应作为后续独立档位或专项能力：

- 多副本、高可用、HPA、拓扑分散和 PodDisruptionBudget。
- Ingress、Gateway API、LoadBalancer 和自动 DNS/证书签发。
- NetworkPolicy、ResourceQuota、LimitRange、自定义 affinity、nodeSelector 和 priorityClass。
- 备份、恢复、定时快照和跨集群迁移。
- MySQL、Redis、Prometheus 的传输层 TLS。
- Grafana、Alertmanager、PrometheusRule 和日志多租户。
- 任意镜像、任意 Helm values 或用户自定义日志 pipeline。

## 6. 用户界面

Kubernetes 页面增加“组件”页签，并继续使用已有连接选择器：

```text
连接：[生产 k3s ▼]  Kubernetes v1.xx  addon: curated

组件目录 | 已安装 | 监控目标 | 操作记录
```

### 6.1 连接摘要

选择连接后显示：

- 连接名称、Context、Kubernetes 版本和架构。
- 识别到的发行版；无法识别时显示 Kubernetes，不影响使用。
- 当前连接是否允许受控组件管理。
- RBAC、默认 StorageClass、Ingress、metrics API 等能力。
- HTTP 明文或跳过证书校验的醒目风险提示。

### 6.2 安装流程

1. 用户选择组件与配置档位。
2. Web 请求预检和计划。
3. 页面列出即将创建的命名空间、CRD、ClusterRole、Service、PVC 等资源范围，以及版本和风险。
4. 涉及集群级资源、安装、升级或卸载时要求近期 AAL2。
5. 用户确认后提交计划摘要；Broker 再次计算和比对摘要后执行。
6. 页面通过持久化 operation ID 查看进度，刷新或断线后可恢复。

状态统一为：`NotInstalled`、`Planning`、`Installing`、`Ready`、`Degraded`、`Upgrading`、`Uninstalling`、`Failed`、`NeedsAttention`。

### 6.3 “按标签监控”

界面文案使用“按标签监控”。用户选择：

- 目标类型：Service 或 Pod。
- 命名空间范围。
- Kubernetes Label Selector，例如 `app=api,environment=prod`。
- 端口名、路径、scheme 和抓取间隔。

Prometheus MVP 直接生成受控的原生 `kubernetes_sd_configs` 与 relabel 配置，不要求目标集群预装 Prometheus Operator。镜像 tag 只表示镜像版本，不参与工作负载选择。创建前预览匹配资源数量，零匹配和选择范围过大时给出提示；保存后显示匹配数量、active targets 和最近抓取错误。

## 7. 数据模型

建议新增以下表；具体 schema 编号在实现时按当前最新版本顺延。

### 7.1 `kubernetes_addon_installation`

```text
id TEXT PRIMARY KEY
connection_id TEXT NOT NULL REFERENCES kubernetes_connection(id) ON DELETE CASCADE
catalog_id TEXT NOT NULL
release_name TEXT NOT NULL
namespace TEXT NOT NULL
desired_version TEXT NOT NULL
observed_version TEXT NOT NULL DEFAULT ''
config_json TEXT NOT NULL
config_digest TEXT NOT NULL
generation INTEGER NOT NULL
status TEXT NOT NULL
last_error_code TEXT NOT NULL DEFAULT ''
last_error_message TEXT NOT NULL DEFAULT ''
created_at INTEGER NOT NULL
updated_at INTEGER NOT NULL
UNIQUE(connection_id, namespace, release_name)
```

`config_json` 只保存非敏感、经过模式裁剪的期望配置。敏感值只写入集群 Secret；数据库最多保存 Secret 引用和不可逆摘要。

### 7.2 `kubernetes_addon_operation`

```text
id TEXT PRIMARY KEY
connection_id TEXT NOT NULL
installation_id TEXT
kind TEXT NOT NULL
requested_by TEXT NOT NULL
plan_digest TEXT NOT NULL
phase TEXT NOT NULL
attempt INTEGER NOT NULL
helm_revision INTEGER
error_code TEXT NOT NULL DEFAULT ''
recovery_hint TEXT NOT NULL DEFAULT ''
created_at INTEGER NOT NULL
started_at INTEGER
finished_at INTEGER
```

### 7.3 `kubernetes_monitor_target`

保存 5.4 定义的标签规则、Prometheus 安装归属、生成配置摘要和最近一次对账结果。实际抓取配置由 `clusteraddon` 根据这些类型化字段生成，用户不能提交原始 Prometheus YAML 或 relabel 表达式。认证信息只保存 Secret 引用。

### 7.4 连接表变更

为 `kubernetes_connection` 增加 `addon_mode TEXT NOT NULL DEFAULT 'disabled'`。迁移不得改变任何已有连接的权限。

## 8. 预检与安全控制

每次计划和执行都必须检查：

- 连接仍存在，指纹、路径、Context 和传输配置未被替换。
- `addon_mode=curated`，操作者仍为 Administrator 或 Maintainer。
- 计划未过期，计划摘要与即将执行的目录版本、参数完全一致。
- Kubernetes 版本、CPU 架构和 Chart 兼容。
- 使用 `SelfSubjectAccessReview` 检查该组件需要的 namespaced 与 cluster-scoped 权限。
- 命名空间、Release 和已存在资源没有所有权冲突。
- 默认或指定 StorageClass 存在；无持久卷时必须明确选择临时存储档位。
- Chart/镜像源可达；失败时返回可操作的错误码，不泄露凭据。
- 安装、升级和卸载以 `connection_id + namespace + release_name` 加锁。

Broker 接口只接受 `catalog_id`、受控配置和计划摘要。Broker 必须自己从数据库解析连接，不信任 Web 传入的 kubeconfig 路径、Context、Chart 地址或命令参数。

卸载默认保留 PVC；选择删除数据时需要独立确认，并在计划中列出准确 PVC。不得静默删除 CRD，因为 CRD 可能由其他 Release 共用。

## 9. 状态机与恢复

执行路径：

```text
Preflight -> Planned -> Authorized -> Running -> Verifying -> Succeeded
                                      \-> Failed -> Reconcile/Retry
```

- `Start` 先写 operation，再触发执行，保证每个动作都有持久记录。
- HTTP 请求结束不取消任务。
- Broker 或 Web 重启后，扫描非终态 operation，根据 Helm Release 与 Kubernetes 资源重新判断结果。
- 重试使用同一个幂等键，不并行创建第二个 Release。
- 仅在尚未进入 Helm mutation 或适配器明确支持的阶段允许取消。
- 健康状态由周期对账产生，不以 Helm 命令退出码作为唯一成功依据。
- 不自动接管同名既有 Release；MVP 要求用户更换名称或先在外部处理冲突。

## 10. 分阶段实施

### 阶段 0：ADR、威胁模型和界面契约

- 新增受控组件管理 ADR，并更新 PRD 的非目标描述。
- 定义目录格式、类型化请求、错误码、状态机和 Broker 版本升级策略。
- 明确 Chart 获取、缓存、校验和离线失败行为。

验收：评审能够证明 Web 无法借此发送任意 Helm、kubectl、YAML 或 shell 命令。

### 阶段 1：只读目录与连接预检

- 增加组件页和现有连接选择器复用。
- 展示目录、连接能力、RBAC、StorageClass 和潜在冲突。
- 读取现有 Helm Release 仅用于冲突检测，不提供接管。

验收：在两个已保存连接之间切换时，目录可用性、权限和状态严格隔离；无写操作。

### 阶段 2：纵向打通一个测试组件

- 建立 `clusteraddon`、数据表、类型化 Broker IPC、任务状态机和 Helm 适配器。
- 用一个内部测试 Chart 打通 Plan、AAL2、Install、Verify、Uninstall 和重启恢复。
- 完成审计、脱敏、并发锁和幂等。

验收：页面断线、Web 重启和重复提交不会产生重复 Release；Broker 拒绝篡改后的计划。

### 阶段 3：Registry、MySQL、Redis

- 发布本计划 5.5–5.7 定义的单实例、可预测资源占用档位。
- 提供 PVC、ClusterIP/NodePort 和自动生成、手工输入或既有 Secret 三种凭据模式。
- 卸载时明确保留或删除数据。
- 增加数据库 list/create/drop/reset 和 Redis list/size/clear 等类型化受控动作。

验收：三种组件均可在选定连接中独立安装、升级到一个测试版本和卸载，不影响其他连接。

### 阶段 4：Prometheus 与标签监控

- 发布 5.4 定义的 Prometheus、kube-state-metrics、node-exporter 轻量目录项。
- 增加 Pod/Service 标签规则表单、匹配预览、原生抓取配置生成、受控 reload 和删除。
- 对 RBAC 不足、选择器零匹配、端点不可抓取提供明确诊断。

验收：为带指定 label 的测试 Service/Pod 建立监控后，可看到 target 为 up；更换到另一连接不会显示或修改原目标。

### 阶段 5：VictoriaLogs

- 安装 5.3 定义的单节点 VictoriaLogs 与 vlagent。
- 配置命名空间/Pod label 过滤、排除注解、保留期、PVC 和资源限制。
- 验证每节点采集覆盖率以及探针日志的写入和查询闭环。

验收：测试 Pod 日志可查询；过滤标签外的日志不会进入目标存储；重启后状态可对账。

### 阶段 6：可选本地 k3d 环境创建器

- 单独实现 Docker/k3d 能力检测和受控集群模板。
- 创建后导入 kubeconfig，生成普通 Kubernetes 连接。
- 组件安装复用阶段 2–5 的链路，不出现 k3d 专用安装分支。

验收：同一组件可在手工导入的 k3s、标准 Kubernetes 和新建 k3d 连接上通过相同入口安装。

### 阶段 7：生产增强

- Chart 升级兼容矩阵、手动回滚、离线缓存和供应链校验。
- Prometheus Operator、数据库 Operator、Redis 高可用、VictoriaLogs Cluster 等重型档位。
- 资源配额评估、备份/恢复集成和维护窗口。

## 11. 测试计划

### 11.1 模块测试

- 通过 `clusteraddon.Manager` 接口测试目录解析、配置校验、计划摘要和状态迁移。
- 使用内存 Helm/Kubernetes 适配器，不对内部实现逐层重复测试。
- 覆盖 Secret 脱敏、连接隔离、过期计划、篡改计划、重放和并发冲突。

### 11.2 Broker 集成测试

- Broker 重新解析数据库连接，不接受 Web 提供的路径和 Chart 来源。
- `addon_mode=disabled`、角色不足、AAL2 过期、指纹变化和 RBAC 不足时 fail closed。
- 覆盖 HTTPS、HTTP，以及 kubeconfig 明确启用跳过证书校验的模式和风险回显。

### 11.3 Kubernetes 端到端测试

- 使用 k3d 建立至少两个隔离测试集群，但它只是测试基础设施，不是产品执行前提。
- 逐项安装、升级、卸载首批组件并验证 Pod、Service、PVC、ConfigMap/Secret、RBAC、Helm Release 和健康状态。
- 覆盖无默认 StorageClass、命名冲突、Chart 超时、网络中断、Broker/Web 重启、PVC 保留/删除和部分失败恢复。
- 验证日志与审计中不出现数据库密码、Registry 凭据、Bearer Token、证书私钥或完整 kubeconfig。

### 11.4 UI 与基础访问测试

- 多连接切换、空状态、权限状态、长任务刷新恢复和错误恢复提示。
- 键盘操作、焦点、颜色对比、窄屏布局及无表情符号图标规范。
- 按仓库本地部署流程重新部署并保留测试数据、部署和测试报告。

## 12. 发布与回退

- 功能默认关闭，所有旧连接迁移后保持 `addon_mode=disabled`。
- 首版可增加全局 feature flag；关闭后停止接受新操作，但仍允许查看历史和对账状态。
- 目录随应用版本发布，回退应用时不得自动回退已安装 Chart。
- 每个组件上线前固定 Chart 版本并在支持的 Kubernetes 版本矩阵上测试。
- 首批发布顺序建议为 Registry -> Redis -> MySQL -> Prometheus -> VictoriaLogs，以先验证通用链路，再进入集群级 RBAC 和宿主挂载较多的组件。

## 13. 完成定义

本功能只有同时满足以下条件才算完成：

1. 用户能切换任一已保存 Kubernetes 连接，并且所有目录、安装、监控目标、任务和状态均按连接隔离。
2. k3s、标准 Kubernetes 与 k3d 使用同一个组件管理入口和执行链路。
3. Web 无 kubeconfig 凭据和通用命令能力；所有写操作经类型化 Broker 接口和近期认证。
4. 5.1 列出的五个目录项均完成版本锁定、配置模式校验和端到端验证。
5. 任务可恢复、可审计、可对账，失败时不会误报成功或产生重复 Release。
6. Secret 不进入 SQLite、日志、审计或前端持久状态。
7. 安装前能准确展示集群级权限、CRD、持久卷和数据删除影响。

## 14. 参考资料

- [Helm SDK 与 Helm 作为 Kubernetes 包管理器](https://helm.sh/docs/sdk/)
- [Prometheus Operator 设计](https://prometheus-operator.dev/docs/getting-started/design/)
- [Prometheus Operator 入门：ServiceMonitor 与 PodMonitor](https://prometheus-operator.dev/docs/developer/getting-started/)
- [Prometheus Kubernetes 服务发现配置](https://prometheus.io/docs/prometheus/latest/configuration/configuration/#kubernetes_sd_config)
- [VictoriaLogs 快速开始与部署方式](https://docs.victoriametrics.com/victorialogs/quickstart/)
- [VictoriaMetrics Operator VMServiceScrape](https://docs.victoriametrics.com/operator/resources/vmservicescrape/)
- [k3d 配置文件](https://k3d.io/stable/usage/configfile/)
- [k3d Registry 使用方式](https://k3d.io/stable/usage/registries/)
- [Rancher 多集群管理](https://ranchermanager.docs.rancher.com/how-to-guides/new-user-guides/manage-clusters/)
- [Portainer Kubernetes 应用部署](https://docs.portainer.io/2.33-lts/user/kubernetes/applications/manifest)
- [Lens Helm Charts](https://docs.lenshq.io/k8slens/using-lens/helm/charts/)
- [Headlamp App Catalog 插件](https://github.com/headlamp-k8s/plugins/blob/main/README.md)

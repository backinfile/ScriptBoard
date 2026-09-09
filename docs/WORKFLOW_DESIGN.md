# 可视化工作流：Windmill 参考与实施建议

日期：2026-09-09  
状态：调研提案，尚未实现。基于 ScriptBoard dev 的 49cce511。

## 推荐方向

新增“自动化 → 工作流”，采用 ComfyUI/Dify 式拖拽连线画布，借鉴 Windmill 的输入输出、版本与运行观察。保留 Go 服务、SQLite 和原生安装，本机完成源码拉取、脚本/Maven/Go 构建、制作与推送镜像，再向已有 k3s/k3d 集群发布应用。

第一版以本机执行为主。远程 Kubernetes API 目标不等于远程构建 Worker。“k3s/k3d 部署”暂按向已有集群部署应用设计；创建 k3d 集群、安装/升级 k3s、SSH 引导主机作为后续独立节点。

## Windmill 的参考价值

Windmill 用 OpenFlow JSON 描述步骤与嵌套控制结构，步骤间映射输入和结果，共享目录承载不适合 JSON 的数据。这里借鉴定义与执行分离、显式输入输出和文件引用，不必声明兼容 OpenFlow。[架构与数据交换](https://www.windmill.dev/docs/flows/architecture)

编辑器把配置、分支、测试和运行观察放在流程上下文中。应重点实现选中节点即可配置与看日志、选择上游输出、定位失败节点。[Flow editor](https://www.windmill.dev/docs/flows/flow_editor)

Windmill 区分草稿和已部署版本。这里称“草稿 / 已发布”：运行固定版本快照，编辑不影响当前执行；“发布工作流”与“部署应用”分开命名。[Draft and deploy](https://www.windmill.dev/docs/core_concepts/draft_and_deploy)

Windmill 用 Worker 标签按执行环境分配工作，社区版与商业版管理能力不同。首期只借鉴能力检测，后续多机再加入分配、租约和产物搬运。[Workers and worker groups](https://www.windmill.dev/docs/core_concepts/worker_groups)

Windmill 支持步骤重试。这里进一步区分副作用：网络读取可以有限重试，脚本、推送、部署在结果不确定时不能默认重复执行。[Retries](https://www.windmill.dev/docs/flows/retries)

| 路线 | 收益 | 代价 | 建议 |
| --- | --- | --- | --- |
| 嵌入/跳转独立 Windmill | 现成编排调试 | 额外服务、账号、执行环境，运行记录分离 | 接受独立平台时可验证业务 |
| ScriptBoard UI + Windmill 引擎 | 成熟引擎 | 两套身份、凭据、状态与日志集成 | 当前不优先 |
| 原生工作流 | 主机、登录、连接、运行体验统一 | 自行维护状态机、恢复和节点 | 推荐，先限定范围 |

Windmill 仓库描述的后端是 Rust API/Workers 与 PostgreSQL 队列，引入完整平台会改变当前部署结构。上述建议是架构取舍，并非性能评测。[Windmill 项目](https://github.com/windmill-labs/windmill)

## 页面设计

列表展示名称、已发布版本、最近结果和更新时间，支持创建、复制、导入导出、运行、历史；提供 Java 发布、Go 发布、已有镜像部署模板。

编辑页：
- 顶部：名称、草稿状态、保存、校验、测试运行、发布、历史。
- 左侧：可搜索节点库，按源码、脚本、构建、镜像、部署、控制分类。
- 中间：缩放平移画布，支持拖拽连线、框选、复制、删除、撤销重做、自动整理和缩略图。
- 右侧：参数、输入输出、超时、资源额度、失败策略。
- 底部：执行面板，展示节点尝试、日志、产物和错误。

节点只展示 Lucide 图标、名称、关键目标、状态与耗时；大表单留在右侧。默认水平排列，数据端口按需展开。键盘可选择与编辑，窄屏优先步骤列表和运行查看。

编辑和运行观察复用画布组件，但数据分离：历史运行显示当时版本，旧日志不覆盖当前草稿。断线恢复补取事件，并标记日志过期状态。

执行线表示先后依赖，数据绑定表示字段来自哪个上游输出。绑定时提出依赖连线，服务端校验一致性，不按画布坐标执行。首期无环图、成功依赖；未实现的条件/并行节点禁止发布。条件上线后明确 selected/skipped，汇合只等待已选择路径。

~~~mermaid
flowchart LR
 A[开始：分支、环境、版本] --> B[Git 拉取]
 B --> C[Maven 或 Go 构建]
 C --> D[制作镜像]
 D --> E[推送镜像]
 E --> F[部署到 k3s]
 F --> G[等待就绪]
 G --> H[完成：摘要和访问入口]
~~~

本地模板：Git → 构建 → 制作镜像 → 导入 k3d → 部署 → 等待就绪。推送和拉取是可选步骤。

## 节点目录

全部节点共享输入绑定、超时、额度、失败策略、执行目标、日志和结果；节点类型带版本，由服务端 schema 校验。

| 节点 | 主要配置 | 输出 | 首期边界 |
| --- | --- | --- | --- |
| Git 拉取 | 仓库、branch/tag/commit、凭据引用 | checkout、commit SHA | 每次独立目录，不修改日常工作仓库 |
| 本地脚本 | 脚本/Quick Run、参数、目录模式 | exitCode、可选 JSON、产物 | 普通脚本保留目录语义，checkout 模式新增契约 |
| Maven 构建 | checkout、pom、goals、profiles、JDK、测试选项 | JAR/WAR、摘要、工具版本 | 优先 mvnw，否则本机 Maven；默认保留测试 |
| Go 构建 | checkout、包、GOOS/GOARCH、CGO、测试选项 | 二进制、摘要、平台 | 检测本机工具，报告交叉编译能力不足 |
| 制作镜像 | context、Dockerfile、tag、平台、build args | 本地 image ID、镜像引用 | 单平台、已有 Dockerfile |
| 推送镜像 | 镜像、仓库连接、tag | 仓库引用、manifest digest | 写权限独立授权，监控权限不自动扩展 |
| 拉取镜像 | tag/digest、连接、平台 | 镜像、digest、Engine 标识 | 拉到本地 Engine，不代表集群可用 |
| k3d 镜像导入 | 本地镜像/归档、集群 | 导入结果、集群标识 | 与部署分开，也可走仓库路线 |
| k3d 部署 | 连接、namespace、清单产物、镜像、等待策略 | 资源、就绪结果、入口 | 已有集群，复用 Kubernetes 部署执行器 |
| k3s 部署 | 同上、imagePullSecret 引用 | 同上 | 集群节点负责拉取镜像 |
| 等待就绪 | 资源、超时 | rollout/readiness | 部署默认内置，也可独立复用 |

k3d 支持把本地镜像或归档导入集群，“本机已有”和“集群可用”要分开记录。[k3d 命令](https://github.com/k3d-io/k3d/blob/main/docs/usage/commands.md)

k3s 私有仓库涉及节点 containerd 配置。推送成功不代表集群能拉取，预检区分认证、CA/HTTP 和 Pod 凭据；首期不自动改写节点配置。[K3s 私有仓库](https://docs.k3s.io/installation/private-registry)

制作镜像首期指 Dockerfile 构建；自动生成 Dockerfile、Buildpacks、多架构、签名、SBOM 后续独立添加。

## 数据和执行模型

新增概念为提议，尚未纳入正式领域词汇：
- Workflow：名称、草稿、已发布版本指针、归档状态。
- WorkflowVersion：不可变图、输入 schema、节点类型版本、资源引用、创建者、摘要。
- WorkflowRun：版本和输入快照、启动用户、触发来源、状态、目录引用、时间。
- StepRun：节点、attempt、状态、底层 Run 或领域操作 ID、输出与日志引用。
- Artifact：运行归属、相对路径、类型、大小、SHA-256、保留期；镜像增加 Engine、平台与 image ID/registry digest。

nodes/edges 与 editorLayout 分开。参数引用按类型保存，例如：

~~~json
{"kind":"stepOutput","nodeId":"checkout","path":["commitSha"]}
~~~

常量、流程输入、连接引用分别编码；首期不执行任意 JavaScript 表达式。源码、JAR、镜像归档存文件，JSON/SQLite 只保存引用。每次运行独立目录，并行写入分离，缓存单独加锁；引用校验归属、路径、链接和过期状态。

目录不能随意放入普通 Run 禁止执行的 State Root。实施时指定受管工作目录和 ACL，或新增有界目录句柄，不能放宽所有受保护路径。发布固定脚本/Quick Run 版本与摘要；分支在每次运行开始解析到 commit，精确复跑使用原 commit。凭据只记录连接 ID/版本，不进入快照。

建议 internal/workflow 拥有图校验、版本、状态推进、产物规则，internal/workflownodes 拥有节点 schema 与适配；bootstrap 组合，store 统一 SQLite 迁移，web 负责 HTTP/UI。

| 现有模块 | 复用范围 | 新增工作 |
| --- | --- | --- |
| runmanager / runnerhost | 日志、停止、超时、额度 | 启动描述当前面向脚本，不能假定支持任意 CLI |
| scheduler | 时间规则、触发留痕 | 当前直接依赖 Run，需工作流触发入口 |
| privilegebroker / clusterstatus / kubeconfigmanager | 连接、访问边界、观测 | 固定部署动作与授权契约 |
| registrymonitor | 配置语义、探测经验 | Inspect 不是 build/push/pull |
| secretstore / secretredaction | 秘密机制、脱敏 | 遵循所有权；密码型 Variable 不等于凭据库 |
| web/ui | 导航、语言、主题、SSE | 画布及执行事件投影 |

脚本/Maven/Go 用受信执行描述；镜像和集群用强类型适配器。Broker 不提供任意命令或 YAML 透传。部署清单需绑定版本、目标和资源范围，允许的 kind、namespace、字段及 Secret 处理是部署切片前置契约。

WorkflowRun 使用 running/succeeded/failed/cancelling/cancelled/disconnected；StepRun 的 pending 表示依赖未满足，不改变现有 Run 无 queued 的模型。首期不建全局 Run 队列。

启动意图先持久化，WorkflowRun + 节点 + attempt 作为稳定调用标识，输出和推进决定在事务中提交。执行端须能按标识查到已接受操作，否则崩溃后不能自动重发副作用动作。失去可靠监督采用 disconnected，可查询操作进行对账，不承诺任意步骤恰好执行一次。

取消先阻止后继，再停止活动步骤，不代表撤销已推送/部署内容。首期整流重跑；从失败节点继续需先具备输入一致性、产物有效性、副作用重放策略。回滚独立设计。

## 权限和连接

维护员/管理员编辑、发布、测试草稿；执行员只运行已发布且允许该角色的流程，不能用任意 namespace/仓库输入绕过权限；观察员看允许的状态和脱敏结果。停止沿用启动用户和维护员/管理员边界。

Broker 的 30 秒一次性 capability 不能复用于长流程。需绑定流程版本、操作范围、调用标识的执行授权，每个敏感动作校验当前用户状态；定时与人工批准待此契约完成后实施。

Git/Registry/Kubernetes 按协议支持安全、明文、显式跳过证书验证，保留选择并提示中间人风险。Engine/k3s 额外配置缺失时明确报告，不静默改写安全策略。秘密不进入 URL、日志或普通环境快照。

Runner 当前默认身份以 ADR-0172 为准，不能沿用 ADR-0149 的旧默认隔离描述；额度以 ADR-0183 为准。能力检测必须在实际运行身份下执行。

## 前端选型及架构冲突

| 选择 | 适用性 | 建议 |
| --- | --- | --- |
| 原生 JS + SVG + HTML | 不改构建链，需自维护连线、历史、选择、布局 | 保持原约定时的备选 |
| 局部 React Flow 页面 | 适合长期节点编辑器 | 产品方向优先，先调整 ADR |
| 完整移植 Windmill 前端 | 与现有模板及模型差异大 | 不建议 |

[React Flow 官方文档](https://reactflow.dev/learn) 提供节点、端口、视窗等能力说明。它只处理图交互，不是执行引擎；历史与自动布局需单独评估。建议局部 React/TypeScript 构建，产物嵌入 Go，其余页面保持现状，用户无需安装 Node。

需要登记的新决策，而非静默改变现有约定：
- [ADR-0060](./adr/0060-use-a-server-rendered-pure-go-stack.md) 明确不引入 Node 构建链、SPA 框架，局部 React 同样需要调整。
- [CONTEXT.md](../CONTEXT.md) 当前不是通用运维编排平台，本方案限定为可信主机的构建发布自动化。
- [ADR-0171](./adr/0171-monitor-multiple-kubernetes-connections-independently.md) 和 [ADR-0173](./adr/0173-route-managed-container-and-kubernetes-access-through-the-broker.md) 不提供任意资源部署，需新增受约束领域。
- [ADR-0027](./adr/0027-allow-unbounded-concurrent-runs-without-a-queue.md)、[ADR-0050](./adr/0050-use-an-explicit-run-state-machine-without-queueing.md)：依赖等待放 StepRun，未来队列单独修订。
- [ADR-0008](./adr/0008-use-script-directory-as-working-directory.md)：普通脚本保留原工作目录，checkout 是新增模式。
- [ADR-0146](./adr/0146-route-host-mutations-through-a-privileged-broker.md)：长流程须设计执行授权。

本次只提出建议，不修改正式 ADR 或声称已接受上述扩展。

## 实施顺序

| 阶段 | 交付 | 完成标准 |
| --- | --- | --- |
| 0 | 技术、授权、目录、schema；两条交互样机 | 可连线配置保存恢复，模拟执行标注清楚 |
| 1 | 草稿/发布、手动触发、Git、脚本、历史、日志、取消 | 真实 checkout → 脚本产物，关浏览器继续执行 |
| 2 | Maven、Go、build/push/pull、凭据、产物保留 | Java/Go 各完成真实构建推送，digest 可追溯 |
| 3 | k3d 导入、共享 Kubernetes 部署、k3s/k3d 模板、就绪检查 | 目标集群真实运行镜像，错误定位到节点 |
| 4 | 条件、并行汇合、定时、人工批准、恢复、子流程 | 每项独立语义与失败验收 |
| 5 | 多机 Worker、产物传输、k3d 创建、k3s 安装 | 后续独立需求 |

用户列出的节点在阶段 3 全部覆盖；阶段 1 不是全部需求完成。工期在阶段 0 验证契约改动后估算。首先实现 Git → 本地脚本 → 产物，最早检验目录、凭据、状态、日志和取消，再扩充节点。

## 后续实施验收

以下是后续测试计划，本次文档调研未执行这些测试，未重新部署服务。

- 基础访问：登录、权限、导航、中英文、刷新、未认证接口、CSRF、跨用户访问。
- 画布：增删复制、撤销、缩放、保存恢复、环/坏引用、冲突保存、旧版本执行。
- 执行：成功、非零退出、超时、停止进程树、内存限制、磁盘不足、SSE 重连。
- 恢复：启动丢回复、输出提交前崩溃、重启、disconnected、不重复推送部署。
- 构建：固定 commit、并发目录隔离、缺少工具、空格路径、mvnw、CGO/平台兼容。
- 镜像：build/push/pull、认证失败、digest 一致、Engine 区分、平台不匹配。
- 部署：k3d 导入/仓库路径、k3s 成功、ImagePullBackOff、rollout 超时、namespace 越界。
- 连接：按协议覆盖 TLS、明文、显式跳过验证，保存后保持选择。
- 凭据：日志/结果/导出脱敏，撤权后敏感节点停止，监控连接不自动可写。
- 产物：路径穿越、链接、过期引用，清理不破坏活动运行。
- 重新本地部署后用外部浏览器或 HTTP 逐项测试，记录实际环境、账号和结果，保留部署及测试数据。

研究范围：核对官方资料和仓库代码/ADR；未安装 Windmill、操作其演示实例或比较性能。


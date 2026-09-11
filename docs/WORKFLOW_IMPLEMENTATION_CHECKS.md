# 工作流实现验收报告

日期：2026-09-10。工作分支：`codex/workflow-design-20260909`。

## 测试条目

- 基础访问：登录、工作流入口、静态资源、四个内部页签、重启后读取已保存数据。
- 页面：卡片、抽屉、输入焦点、按类型创建节点、端口连线、连接编辑/删除、取消操作、撤销重做、拖动/缩放、无文档刷新。
- 领域：字段类型/枚举、默认值、必填、仅连线、循环/引用/重复来源、上游缺失不得兜底。
- 存储：不可变版本、并发修订冲突、Entry 软锁/发布绑定、运行参数及变量快照。
- 运行：队列顺序、原子资源锁、取消、失败阻断、条件跳过、监督丢失、重启恢复和显式释放。
- 执行器：Git、Go、Maven、镜像制作/推送/拉取、本地脚本、托管 Python/JavaScript/Shell/PowerShell、k3d/k3s。
- 连接：协议/证书验证选择保留；真实 Kubernetes TLS、显式跳过验证和 HTTP；Docker context/config 引用契约。
- 集成保护：权限矩阵、主机文件访问边界、工作流子进程禁止绕过 Entry 直接重跑。

## 本地部署

应用地址：<http://127.0.0.1:18910/workflow>。测试账号：`admin`，密码文件为 `.scratch/workflow-deploy/admin-password`。服务以隐藏窗口运行，PID 记录在同目录的 `pid`。

部署、配置、运行日志和 SQLite 测试数据保留在 `.scratch/workflow-deploy/`；源码、产物、镜像及集群测试文件保留在 `.scratch/workflow-jobs/`。测试期间从 schema 70 升级至 71，沿用原 State Root 并保留历史记录。

使用独立资源：

- Git 示例仓库与持久 clone 工作区。
- Registry 容器 `scriptboard-workflow-qa-registry`，回环端口 18911。
- k3d 集群 `scriptboard-workflow-qa`，未切换主机默认 kubeconfig/context。
- 该集群的回环 HTTP 测试代理端口 18912。
- Maven 使用官方 `maven:3.9.9-eclipse-temurin-17` 容器工具链，经临时测试命令入口调用；没有安装全局 JDK。

未操作需求文档中的业务发布仓库、正式 Registry 或业务集群。

## 实际执行结果

| 场景 | 结果与证据 |
| --- | --- |
| Git 拉取 | 成功，返回实际 sourceCommit `2416f25c60c761f5bcf21f1c4ee59e3141ef253c` |
| Go 构建 | 成功，生成 clone/dist/qa |
| Maven 构建 | 成功，生成 maven/target/workflow-1.0.jar |
| 镜像制作、推送、拉取 | 成功，镜像 `127.0.0.1:18911/workflow/qa:verified`，返回实际 ID/digest |
| k3d | ConfigMap 应用、镜像导入、Deployment 应用、rollout 等待成功 |
| k3s | 使用显式 kubeconfig 应用清单与 Deployment，并等待 rollout 成功 |
| Kubernetes 连接模式 | TLS、显式 skipTLSVerify、回环 HTTP proxy 三种方式均成功 |
| 本地脚本 | 传入参数并读取结果，answer=42 |
| 四种托管语言 | Python、JavaScript、PowerShell、Git Shell 均返回 answer=42 |
| 主动失败 | 非零退出标记 failed，后续出口 skipped |
| 取消 | 正在运行的脚本停止并标记 cancelled |
| 持久化 | 多次重新部署后保留自定义节点、Entry 和运行快照 |
| Entry 执行约束 | 普通历史重跑接口对工作流子进程返回 409 |

原始结果：

- `.scratch/workflow-deploy/integration-results.json`
- `.scratch/workflow-deploy/maven-local-results.json`
- `.scratch/workflow-deploy/plaintext-results.json`
- `.scratch/workflow-deploy/deployment-results.json`
- `.scratch/workflow-deploy/final-smoke-results.json`
- `.scratch/workflow-deploy/integration-progress.jsonl`

## 外部浏览器验收

使用外部 Chrome 和 Playwright 操作真实应用。基础及高级画布测试通过：

- 创建节点、拖动节点、双向参数连线。
- 选中连线后 Delete 删除及撤销恢复。
- 更换已有连线端点。
- Escape 取消换线保留原连接。
- 从端口拖至空白创建兼容节点并自动接线。
- 一次撤销同时撤回新节点和自动连接。
- 右键端口断开、空白平移、参数/说明切换。
- 保存草稿、切换页签、自定义节点抽屉输入。
- 统计主文档导航次数为 **0**，页面异常为 **0**。

结果文件：`.scratch/workflow-deploy/ui-results.json`、`ui-advanced-results.json`。截图：`editor-interactions.png`、`editor-advanced.png`、`custom-nodes.png`，均在部署目录下。

## 自动化检查

- 工作流领域测试通过，包括失败/监督不确定时的资源持有、变量快照与密码变量拒绝、条件跳过、枚举约束。
- 工作流 HTTP 及现有权限矩阵测试通过。
- Python CLI 适配器契约测试 `scripts/test-workflow-runner.py`：3 组通过，覆盖 Git URL 保留、Kubernetes 三种连接策略、Docker context/config 引用。
- `go vet ./internal/workflow ./internal/web ./internal/runmanager ./internal/store/migrations` 通过。
- JavaScript 语法检查、差异空白检查通过。
- 最终全量测试 `GOMAXPROCS=2 go test -p 1 ./...` 通过；Web 包 272.868 秒，所有包成功。

测试期间先后处理了 schema 升级兼容入口、画布焦点更新、端点手柄命中、选中操作误加入撤销栈及排队取消竞争。主机内存压力导致的一轮测试进程分配失败，改为 `GOMAXPROCS=2 go test -p 1` 后重跑。现有 Windows 临时数据库清理占用测试定向复测通过；最终全量日志为 `.scratch/workflow-deploy/go-test-verified.log`。

## 边界与保留状态

本版是本机执行的工作流平台。远程执行机、循环、并发节点、定时工作流、审批与通知未纳入本版。实际业务 run.py 的发布语义仍由业务脚本负责；本次未对不存在于当前环境的业务目录创建猜测性模板。

Registry 的 TLS/明文配置由 Docker daemon 管理。实际镜像链路测试使用回环 HTTP Registry；Docker context/config 选择另有命令契约测试，不将其描述为实际远程 TLS Registry 验收。

冻结预览 SHA-256 保持 `16b97ec0ccfede1daeb8d27415eff13202ca9a6228fdf25557a9449c68a7cbd7`，通过 Git 属性保留字节。工作分支、worktree、应用部署与测试数据保留，未合并 dev。

## 分层整理与 UI 回归（2026-09-10）

更新原有本地测试部署后，以外部 Chrome 执行 `scripts/test-workflow-layout.cjs` 和既有 `ui-advanced.cjs`，均通过。测试覆盖基础登录访问、ELK 资源加载、混合数据/控制依赖、独立节点、不同高度节点、分层无重叠、重复整理稳定、一次撤销/重做、保存保留脚本及连线、计算期间编辑、计算失败保留原图，以及四页签无刷新、桌面填满右侧、390px 窄屏无横向溢出。既有连线创建/改接/取消/删除、拖线创建与抽屉编辑回归通过。

定向 Go 测试通过：`GOMAXPROCS=2 go test -p 1 ./internal/web -run "TestWebTemplatesDeclareEveryButtonType|TestWorkflow|Test.*Route" -count=1`，6.774 秒。构建并重新部署成功；本次没有改动执行引擎，未重复运行上一轮完整构建/集群执行链路。

可复跑浏览器测试：配置 `WORKFLOW_TEST_PASSWORD`、可选 `WORKFLOW_TEST_URL` / `WORKFLOW_TEST_USER`、`CHROME_EXECUTABLE` 和 `PLAYWRIGHT_MODULE`，运行 `node scripts/test-workflow-layout.cjs`。结果及四页签、移动端截图保存在 `.scratch/workflow-deploy/layout-*`；测试草稿保留。本地地址仍为 http://127.0.0.1:18910/workflow 。

## 2026-09-10 流程控制回归

- 领域测试：算术全部操作、除零/非有限结果、严格类型、整数范围、分支类型/路径/范围/默认/首条与全部命中、失败结构化输出、汇合任一/全部、显式恢复、重试历史、监督不确定不重试、等待取消与控制出口校验通过。
- 本地重新部署后，外部 Chrome 的 test-workflow-controls.cjs 通过：7 × 2 输出 14，命中大于十，另一侧 skipped，规则更名/排序保持出口 ID，无整体刷新或页面异常。
- test-workflow-control-runtime.cjs 通过：真实 Python 托管节点首次失败、第二次成功返回 42，保留两次进程及非空日志；等待节点取消成功。
- 分层整理、抽屉与日志、运行历史/菜单回归通过。日志测试按具有进程日志的运行选择，兼容新增纯计算运行。
- 测试部署 http://127.0.0.1:18910/workflow 与 QA 数据继续保留；截图位于 .scratch/workflow-deploy/control-editor.png、control-history.png。冻结 HTML 不变，未合并 dev。
- 最终全量 Go 测试通过（Web 包 266.126 秒）；原有数据库升级、权限、资源锁与执行器测试全部通过。

## 2026-09-10 节点选择关闭与自定义节点操作

重新部署后使用外部 Chrome 验证：test-workflow-node-picker.cjs 原先在点击外部后断言失败，修复后关闭按钮、Esc、外部点击均通过；取消改接保留原线，再次创建不会误接旧线，Enter 创建保持有效，未触发整体导航。选择窗口缺失关闭入口和外部点击处理，现统一清理选择上下文并隔离画布快捷键。

test-workflow-custom-actions.cjs 通过：卡片仅保留右下角更多按钮，已有节点的配置抽屉底部提供删除区，新建不显示；取消删除保留编辑，确认后关闭抽屉并移除卡片；390px 无横向溢出，无页面异常或整体刷新。本地部署、原有测试数据和截图保留；删除验证仅移除了该测试新建的专用节点。

## 2026-09-10 编排精简与等待节点验证

重新部署本地服务后验证通过：
- Go 领域测试覆盖原子保存回滚、冲突保护、历史版本不变、Entry 确认开关（含显式 false）、定时等待先后、等待对象与环路拒绝。
- Web 定向测试覆盖工作流 API、固定角色权限矩阵和路由认证/变更声明。
- 外部 Chrome：test-workflow-editor-actions.cjs 覆盖单次保存即可被 Entry 选择、确认开关持久化及取消执行、Entry 页内跳转、参数/说明键盘切换、390px 布局。
- test-workflow-wait.cjs 覆盖实际定时等待后执行、等待对象勾选与连线更新、环路错误提示、保存与无整页刷新。
- 分层布局、节点选择关闭/取消改接、统一抽屉及日志回归通过。节点选择测试将登录等待改为完成跳转，避免页面尚未登录时提前导航。
- 新的 QA 工作流、Entry、运行历史和本地部署保留。截图：editor-simplified.png、entry-confirm.png、editor-simplified-mobile.png、wait-targets.png（均位于 .scratch/workflow-deploy）。冻结预览未修改，未合并 dev。

## 2026-09-10 入口与出口参数入口统一

编排顶部并列提供入口参数、出口参数，节点面板沿用相同名称和字段抽屉。重新部署后，外部 Chrome 的 test-workflow-drawers.cjs 通过：入口新增参数保留、出口新增返回参数并重新打开仍可见、两侧互不覆盖；日志及窄屏抽屉回归通过。

## 2026-09-10 节点输入显示统一

输入参数端口、名称、类型和本地值合并为一个参数行，连线后同位置显示上游来源；入口默认值与输出端口合并显示。重新部署后，外部 Chrome 的 test-workflow-node-inputs.cjs 验证 Go 节点 8 个输入无重复、来源切换、撤销保留本地值、连线锚点误差小于 2px、无整体刷新。节点选择/取消改接及分层整理回归通过，截图保留在 .scratch/workflow-deploy/node-input-unified.png。

## 2026-09-10 导入导出与 AI 指南验证

- Go 领域测试通过：跨数据库迁移、引用旧脚本版本保持准确、重复导入使用新 ID、原配置不变、非法环路回滚所有新记录、缺失脚本和未知格式版本拒绝。
- 重新部署后，外部 Chrome 的 test-workflow-transfer.cjs 通过：说明页指南/示例下载、JSON 文件上传预览、导入后导出保持图一致、示例实际执行返回 14、无效文件不留下流程记录、390px 页面无溢出、所有操作无整体刷新。
- Web 工作流 API、权限矩阵及路由认证声明定向测试通过。新增导入/导出仅允许管理执行权限，指南允许查看权限。
- 截图保留：.scratch/workflow-deploy/workflow-guide.png、workflow-import.png。QA 流程、Entry 与运行历史保留，本地部署继续运行，冻结 HTML 未修改，未合并 dev。

## 2026-09-11 页签 URL 与导入导出入口

- 导入、导出统一位于编排页更多菜单；说明页提供指南和示例下载。
- 内部五个页签使用 tab 查询参数同步地址，刷新恢复选中页签；无效值回到执行页，保留其他查询参数。
- 浏览器前进、后退原地恢复页签，保留编排实例；程序内跳转编排和运行历史也同步 URL。
- 重新构建并保留本地部署 http://127.0.0.1:18910/workflow。C 盘空间不足时将 Go 与浏览器临时目录设为工作树内 D 盘目录，构建成功。
- 外部 Chrome 自动化通过：五页签 URL/刷新、前进后退实例保留、无效参数、说明页入口检查、指南与示例下载、导入导出一致性、导入示例执行、非法图不写入、移动端布局及无整页导航。
- 测试脚本：scripts/test-workflow-transfer.cjs；测试工作流 e47c74696ed302629fb9b31e48bd7937 保留。
- Entry 操作回归通过（test-workflow-editor-actions.cjs）；旧浏览器测试统一按文档导航请求统计整页刷新，兼容 History API 地址更新。

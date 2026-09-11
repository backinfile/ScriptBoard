# Kubernetes 局部操作验证

日期：2026-09-09

## 验证范围

1. 全部、就绪、变更中、需查看四个状态筛选。
2. 名称、命名空间、状态、副本、节点、CPU、内存、重启数排序。
3. 搜索、命名空间、工作负载类型筛选。
4. 手动刷新、自动刷新、全部展开/收起与键盘。
5. 集群切换、详情抽屉、浏览器前进/后退。
6. 工作负载操作的确认、CSRF 参数、局部更新和失败保留数据。
7. 登录、静态资源、404 和手机布局。

## 复现与结果

最初执行 scoped-actions-repro.cjs：
- 状态筛选保持 document，但替换了集群概况、外部访问和节点。
- 搜索触发新 document，工作负载恢复折叠。

最终构建复跑：
- document 保持相同。
- 集群概况、外部访问和节点 DOM 保持挂载。
- 工作负载保持展开。
- 四个状态、八个排序、搜索条件、历史导航、详情抽屉和集群提交在实际 Edge 部署中全部通过。
- 工作负载操作由浏览器契约测试验证，保留确认流程；模拟请求包含 CSRF 和 return_to，操作后只更新工作负载。
- 503 与结构不完整响应保留已有内容并允许重试。
- 工具栏回归、手机布局和基础访问通过。

新增回归：integration/browser/kubernetes-scoped-actions-contract.cjs，已加入浏览器测试入口。现有 section-refresh、local、connection-delete 测试通过。

Kubernetes 与兼容性定向 Go 测试通过（5.736 秒）。完整 Web 测试两次运行未全绿：首次为 Windows 临时数据库清理占用及已修复的 MySQL 校验契约；第二次仅 TestScheduleTriggersRunAtNextCronTime 失败（150.166 秒）。数据库清理相关测试及 MySQL/Kubernetes 定向复跑通过，计划任务测试单独复跑通过（1.019 秒）。未修改无关的计划任务逻辑。

完整检查时发现原 Kubernetes 提交处理器包含 MySQL 的批量备份校验；校验已归属到 MySQL 工作区，相关契约通过。首次完整测试另遇到临时 app.db 清理时的 Windows 文件占用，单独复跑通过。

JavaScript 语法检查、git diff --check 通过。应用 stderr 为空。

## 保留部署

- 地址：http://127.0.0.1:18990/monitor/kubernetes?cluster=k8s_c60c9360d95b940022d7b96d
- 用户：admin；密码文件：.scratch/k8s-collapse-20260909/admin-password.txt
- 应用 PID：27428；模拟 Kubernetes API PID：22780。
- State Root、kubeconfig、连接、日志与测试数据保留在 .scratch/k8s-collapse-20260909/。
- 同目录保留 scoped-actions-repro.cjs、scoped-actions-local.cjs、scoped-actions-desktop.png。
- 使用本机 Microsoft Edge 访问重新构建的 HTTP 部署，未使用内部浏览器。

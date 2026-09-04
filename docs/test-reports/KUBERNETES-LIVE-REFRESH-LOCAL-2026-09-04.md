# Kubernetes 工作负载实时刷新本地部署测试报告

测试时间：2026-09-04（Asia/Shanghai）  
测试分支：`codex/kubernetes-refresh`  
worktree：`D:\Github\worktrees\ScriptBoard\kubernetes-refresh`

## 结论

- Kubernetes 实时更新请求现在会在渲染前主动采集集群快照，不再重复返回已有的内存快照。
- 模拟集群从一个旧工作负载切换为两个新工作负载后，单次刷新请求立即移除旧项、显示新项，并将总数从 1 更新为 2。
- 普通页面访问和筛选不强制采集；只有实时更新请求携带 `refresh=1`，避免无关导航额外访问集群。
- 全仓 Go 测试、静态检查、补丁格式检查和真实本地部署 HTTP 验收均通过。

## 自动化验证

| 项目 | 命令 | 结果 |
| --- | --- | --- |
| 红灯复现 | `go test ./internal/web -run TestKubernetesLiveRefreshUpdatesWorkloadMembership -count=1` | 修复前失败：响应仍含旧工作负载 |
| 专项回归 | `go test ./internal/web -run 'TestKubernetesLiveRefreshUpdatesWorkloadMembership\|TestMonitorTabsReplaceOnlyTheSnapshotAndRestoreScroll' -count=1` | 通过 |
| 相关模块 | `go test ./internal/web ./internal/clusterstatus` | 通过 |
| 全仓测试 | `go test ./...` | 通过 |
| 静态检查 | `go vet ./...` | 通过 |
| 补丁格式 | `git diff --check` | 通过，仅有仓库既有的 Windows 换行提示 |

## 保留的本地部署

| 项目 | 值 |
| --- | --- |
| ScriptBoard URL | `http://127.0.0.1:19040` |
| ScriptBoard PID | `60284` |
| 模拟 Kubernetes API | `http://127.0.0.1:19041` |
| 模拟 API PID | `68160` |
| State Root | `.scratch/kubernetes-refresh-local-20260904/state` |
| kubeconfig | `.scratch/kubernetes-refresh-local-20260904/kubeconfig.yaml` |
| 登录用户 | `admin` |
| 初始密码 | 保留在 State Root 的 `secrets/initial-admin-password` |
| 测试连接 | `Live refresh test` |
| 二进制 SHA-256 | `86B6346E72D44D72B6D1ECB387F2C40ABA69124881A55658C9B8A82A242AF406` |
| ScriptBoard / 模拟 API stderr | 均为 0 字节 |

## HTTP 验收

| 编号 | 测试 | 结果 |
| --- | --- | --- |
| B01 | 登录页返回 200 并包含登录表单 | 通过 |
| B02 | 匿名访问 Kubernetes 监控返回 303 并跳转 `/login` | 通过 |
| B03 | 未知路由返回 404 | 通过 |
| B04 | 使用新部署生成的管理员密码登录并进入 `/monitor` | 通过 |
| B05 | 创建指向显式明文 HTTP 模拟 API 的观察模式 Kubernetes 连接 | 通过 |
| B06 | 初始快照显示 `old-api`，工作负载总数为 1 | 通过 |
| B07 | 浏览器脚本为实时更新请求附加 `refresh=1` | 通过 |
| B08 | 强制刷新请求返回 200 | 通过 |
| B09 | 模拟集群删除 `old-api` 后，刷新响应不再包含旧项 | 通过 |
| B10 | 模拟集群新增 `new-api` 与 `worker` 后，刷新响应立即包含两项 | 通过 |
| B11 | 刷新后的工作负载总数从 1 更新为 2 | 通过 |
| B12 | 两个服务只监听指定 IPv4 回环地址 | 通过 |
| B13 | ScriptBoard 与模拟 API stderr 均为空 | 通过 |

统计：**13 项通过，0 项失败**。

测试连接、模拟集群、State Root、验证脚本、日志和最终部署均保留在 `.scratch/kubernetes-refresh-local-20260904/`，未停止进程或清理测试数据。

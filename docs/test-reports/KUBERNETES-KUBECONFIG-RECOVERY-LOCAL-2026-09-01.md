# Kubernetes kubeconfig 恢复后监控续采本地部署测试报告

测试时间：2026-09-01（Asia/Shanghai）  
测试分支：`codex/k8s-config-monitor-recovery`  
worktree：`D:\Github\worktrees\ScriptBoard\k8s-config-monitor-recovery`

## 结论

- Kubernetes 快照采集失败后不再永久复用失效客户端；下一轮会重新打开已保存连接并重新读取 kubeconfig。
- kubeconfig 在外部删除后恢复、同时凭据轮换时，监控可从旧快照自动恢复到新快照。
- 连接首次打开失败时会保存连接错误，避免页面继续把暂时缺失的 kubeconfig 表示为正常连接。
- 全仓 Go 测试、静态检查、Broker/Web 边界测试和真实本地部署 HTTP 验收均通过。

## 自动化验证

| 项目 | 命令 | 结果 |
| --- | --- | --- |
| 红灯复现 | `go test ./internal/clusterstatus -run TestRefreshReopensConnectionAfterKubeconfigCredentialsRecover -count=1` | 修复前稳定失败：恢复后仍返回旧凭据错误 |
| 回归测试 | 同上 | 修复后通过 |
| Kubernetes 状态模块 | `go test ./internal/clusterstatus -count=1` | 通过 |
| 受影响边界 | `go test ./internal/web ./internal/privilegebroker ./internal/bootstrap -count=1` | 通过 |
| 全仓测试 | `go test ./... -count=1` | 通过 |
| 静态检查 | `go vet ./...` | 通过 |
| 补丁格式 | `git diff --check` | 通过，仅有仓库既有的 Windows 换行提示 |

当前 Windows Go 环境未启用 CGO，`go test -race ./internal/clusterstatus` 因工具链要求未运行；管理器的客户端替换继续受既有互斥锁保护，相关生命周期由回归测试覆盖。

## 本地部署

| 项目 | 值 |
| --- | --- |
| ScriptBoard URL | `http://127.0.0.1:18920` |
| ScriptBoard PID | `46112` |
| 模拟 Kubernetes API | `http://127.0.0.1:18921` |
| 模拟 API PID | `57516` |
| State Root | `.scratch/k8s-config-recovery-local-20260901/state` |
| kubeconfig | `.scratch/k8s-config-recovery-local-20260901/kubeconfig.yaml` |
| 登录用户 | `admin` |
| 初始密码 | 仅保留在 State Root 的 `secrets/initial-admin-password` |
| 测试连接 | `Kubeconfig recovery test` |
| 二进制 SHA-256 | `927CBDAD48CA57A382448B2E0CA6012C1B07D2CB0F4F36E6FE7D9C301CCC1D11` |
| ScriptBoard / 模拟 API stderr | 均为 0 字节 |

## HTTP 与恢复场景验收

| 编号 | 测试 | 结果 |
| --- | --- | --- |
| B01 | 登录页返回 200 并包含登录表单 | 通过 |
| B02 | 匿名访问 Kubernetes 监控返回 303 并跳转 `/login` | 通过 |
| B03 | 未知路由返回 404 | 通过 |
| B04 | 使用新部署生成的管理员密码登录并进入监控页 | 通过 |
| B05 | 创建指向显式明文 HTTP 测试 API 的观察模式 Kubernetes 连接 | 通过 |
| B06 | 初次采集显示工作负载镜像 `api:v1` | 通过 |
| B07 | 外部删除 kubeconfig，并让 API 将凭据从 `token-v1` 轮换为 `token-v2`、镜像更新为 `api:v2` | 通过 |
| B08 | 恢复含新凭据的 kubeconfig 后，后台自动重连并显示 `api:v2`；旧镜像消失、错误区域为空 | 通过 |
| B09 | ScriptBoard 只监听指定 IPv4 回环地址 | 通过 |
| B10 | ScriptBoard 与模拟 API stderr 均为空 | 通过 |

统计：**10 项通过，0 项失败**。

测试连接、模拟集群、恢复后的 kubeconfig、State Root、日志和最终部署均保留在 `.scratch/k8s-config-recovery-local-20260901/`；报告生成后未停止进程或清理测试数据。

# Kubernetes 外部端口查询本地部署测试报告

测试时间：2026-09-03（Asia/Shanghai）  
测试分支：`codex/k8s-external-ports`  
worktree：`D:\Github\worktrees\ScriptBoard\k8s-external-ports`

## 结论

- Kubernetes 监控页新增“外部访问”，可查询 NodePort、LoadBalancer、`externalIPs`、ExternalName 与 Ingress 路由。
- Service 结果展示命名空间、类型、流量策略、外部地址、服务端口、NodePort、协议和目标端口；纯 ClusterIP Service 不混入外部入口。
- Ingress 结果展示入口地址、Class、HTTP/HTTPS 主机与路径、后端 Service 和端口。
- Service 或 Ingress 权限不足时保留其他快照结果，并显示局部不可用提示。
- 页面明确区分 Kubernetes 声明与网络可达性；k3d `-p` 宿主机映射由 Docker 管理，远程 kubeconfig 无法直接读取。
- 全仓 Go 测试、静态检查、JavaScript 语法检查和真实本地部署 HTTP 黑盒验收均通过。

## 测试条目

1. 聚合 NodePort Service，并保留服务端口、数字或命名目标端口、NodePort、协议及 `externalTrafficPolicy`。
2. 聚合 LoadBalancer IP/主机名、`externalIPs` 与 ExternalName。
3. 排除没有外部声明的普通 ClusterIP Service。
4. 聚合 Ingress Class、状态地址、TLS 主机、路径与 Service 后端，包括默认后端。
5. Service/Ingress API 返回 Forbidden 时，工作负载快照继续可用并记录局部错误。
6. 页面渲染外部访问区域，实时刷新同步替换该区域。
7. 登录页、未知路由、静态资源、管理员登录和 Kubernetes JSON 数据接口基础访问。
8. 最终部署只监听指定 IPv4 回环地址，应用与模拟 API stderr 均为空。

## 自动化验证

| 项目 | 命令 | 结果 |
| --- | --- | --- |
| 定向聚合测试 | `go test ./internal/clusterstatus -run TestHTTPSnapshot -count=1` | 通过 |
| Web 契约测试 | `go test ./internal/web -run TestKubernetesPageSeparatesConnectionsFromSelectedClusterMonitoring -count=1` | 通过 |
| 全仓测试 | `go test ./... -count=1` | 通过 |
| 静态检查 | `go vet ./...` | 通过 |
| JavaScript 语法 | `node --check internal/web/ui/assets/app.js` | 通过 |
| 补丁格式 | `git diff --check` | 通过，仅有仓库既有的 Windows 换行提示 |

## 本地部署

| 项目 | 值 |
| --- | --- |
| ScriptBoard URL | `http://127.0.0.1:18980` |
| ScriptBoard PID | `59084` |
| 模拟 Kubernetes API | `http://127.0.0.1:18981` |
| 模拟 API PID | `41460` |
| State Root | `.scratch/k8s-external-ports-20260903/state` |
| kubeconfig | `.scratch/k8s-external-ports-20260903/kubeconfig.yaml` |
| 登录用户 | `admin` |
| 初始密码 | 保留在 State Root 的 `secrets/initial-admin-password` |
| 测试连接 | `k3d external access test` |
| ScriptBoard / 模拟 API stderr | 均为 0 字节 |

## HTTP 黑盒验收

| 编号 | 测试 | 结果 |
| --- | --- | --- |
| B01 | 登录页返回 200 并包含登录表单 | 通过 |
| B02 | 使用新部署生成的管理员密码登录 | 通过 |
| B03 | 未知路由返回 404 | 通过 |
| B04 | CSS 与 JavaScript 静态资源返回 200 | 通过 |
| B05 | 创建指向显式明文 HTTP 模拟 API 的观察模式 Kubernetes 连接 | 通过 |
| B06 | 外部访问区域显示 NodePort `30080` | 通过 |
| B07 | LoadBalancer 显示 `192.0.2.10` 与命名目标端口 | 通过 |
| B08 | Ingress 显示 `https://api.example.test/v1` 及后端 | 通过 |
| B09 | 普通 ClusterIP Service `internal-only` 未出现在页面 | 通过 |
| B10 | Kubernetes JSON 数据接口包含 Service 与 Ingress 结构 | 通过 |
| B11 | 页面显示 k3d/Docker 映射边界说明 | 通过 |
| B12 | ScriptBoard 仅监听 `127.0.0.1:18980` | 通过 |
| B13 | ScriptBoard 与模拟 API stderr 均为空 | 通过 |

统计：**13 项通过，0 项失败**。

测试连接、模拟集群、kubeconfig、State Root、日志和最终部署均保留在 `.scratch/k8s-external-ports-20260903/`，未停止进程或清理测试数据。

# Redis 实例编辑与超时报错本地测试报告

测试时间：2026-09-02（Asia/Shanghai）
测试分支：`codex/redis-instance-edit-errors`

## 结论

- Redis 详情页新增编辑实例抽屉，可修改名称、环境、主机、端口、ACL 用户名、数据库索引、密码、TLS 模式与 CA 路径。
- 连接与 TLS 设置不变时，编辑可留空密码并保留现有凭据；改变凭据绑定字段时仍要求提交新密码。
- Redis 请求超过页面期限时保留 `context deadline exceeded`，页面显示主机、端口、网络访问与 TLS 模式检查建议，不再显示 Broker JSONL framing 错误。
- 安全 TLS、明文与显式跳过证书验证三种模式均保留，界面继续提示明文和跳过验证的中间人攻击风险。
- HTTP 基础访问、真实管理员登录、专项回归、静态检查与本地部署验收通过。

## 保留部署

| 项目 | 值 |
| --- | --- |
| URL | `http://127.0.0.1:18941` |
| PID | `7908` |
| 登录用户 | `admin` |
| 初始密码 | State Root 私有文件 `secrets/initial-admin-password` |
| 部署目录 | `.scratch/redis-instance-edit-errors-20260902` |
| State Root | `.scratch/redis-instance-edit-errors-20260902/state` |

## 保留测试数据

- Redis 实例：`Redis edit TLS verified`
- 地址：`127.0.0.1:6399 / db3`
- 环境：`development`
- TLS 模式：`insecure_skip_verify`
- 该端口刻意没有 Redis 服务，用于保留连接失败测试证据；`stderr.log` 中的 connection refused 记录符合预期。

## 验收结果

| 编号 | 测试项 | 结果 |
| --- | --- | --- |
| B01 | 登录页返回 200 | 通过 |
| B02 | 匿名访问数据库页返回 303 并跳转 `/login` | 通过 |
| B03 | 未知路径返回 404 | 通过 |
| B04 | 使用新部署生成的管理员密码登录 | 通过 |
| R01 | 创建明文 Redis 实例 | 通过，返回 303 并进入实例详情 |
| R02 | 编辑抽屉预填实例 ID、主机和当前 TLS 模式 | 通过 |
| R03 | 仅修改名称和环境、密码留空 | 通过，现有凭据保留 |
| R04 | 主机变化但密码留空 | 通过，返回 400 并提示需要新密码 |
| R05 | 提供新密码并切换为跳过证书验证 TLS | 通过 |
| R06 | 三种 Redis 连接安全模式和风险提示仍存在 | 通过 |
| E01 | Broker 响应被调用期限中断 | 通过，返回 `context deadline exceeded` |
| E02 | Redis 页面超时提示 | 通过，显示可操作建议且不包含 JSONL framing 错误 |
| T01 | `go test ./internal/redismanager ./internal/privilegebroker ./internal/web -count=1` | 通过 |
| T02 | `go vet ./...` | 通过 |
| T03 | `go build ./cmd/...` | 通过 |
| T04 | `git diff --check` | 通过 |
| T05 | `go test ./... -count=1` | 通过，全部包无失败 |

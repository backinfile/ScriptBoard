# Redis 按操作选择数据库本地测试报告

测试时间：2026-09-02（Asia/Shanghai）  
测试分支：`codex/redis-database-selection`

## 结论

- Redis 新增与编辑连接不再配置或展示数据库索引，连接摘要也只显示服务器地址。
- 总览和键空间在连接后选择逻辑数据库；数据库编号会在页签、SCAN 续页、键选择和值预览之间保持。
- 连接测试固定验证服务器端点的 DB 0；总览、SCAN 和键值读取使用各自请求选择的数据库。
- schema 64 移除 `redis_instances.database_index`，旧连接元数据和凭据仍保留；数据库不再参与加密凭据绑定。
- 明文、验证身份与显式跳过证书验证三种连接模式保持不变。

## 测试清单

1. 登录页、匿名重定向和未知路径等基础访问。
2. 使用真实管理员密码登录新部署。
3. 不提交数据库索引创建、编辑 Redis 连接。
4. 检查新增/编辑表单和连接摘要不再出现数据库配置。
5. 使用真实 Redis 8.2 分别读取 DB 0 和 DB 5 的总览与键空间。
6. 验证 SCAN、键链接和值预览保持所选数据库。
7. 验证非法数据库编号被拒绝。
8. 验证连接测试、schema 迁移、静态检查、构建和全仓测试。

## 保留部署

| 项目 | 值 |
| --- | --- |
| ScriptBoard URL | `http://127.0.0.1:18964` |
| ScriptBoard PID | `51036` |
| 登录用户 | `admin` |
| 初始密码 | State Root 私有文件 `secrets/initial-admin-password` |
| 部署目录 | `.scratch/redis-database-selection-20260902` |
| State Root | `.scratch/redis-database-selection-20260902/state` |
| Redis 容器 | `sb-redis-db-selection-20260902` |
| Redis 地址 | `127.0.0.1:26464`，明文、无认证 |

## 保留测试数据

- ScriptBoard 连接：`Redis database selection QA retained`
- Redis DB 0：String 键 `qa::db0`
- Redis DB 5：String 键 `qa::db5`、Hash 键 `order::64`
- Hash 字段：`status=selected-database`

## 验收结果

| 编号 | 测试项 | 结果 |
| --- | --- | --- |
| B01 | 登录页 | 通过，HTTP 200 |
| B02 | 匿名访问数据库页 | 通过，HTTP 303 跳转 `/login` |
| B03 | 未知路径 | 通过，HTTP 404 |
| B04 | 管理员登录 | 通过 |
| R01 | 不带数据库字段创建 Redis 连接 | 通过 |
| R02 | 新增与编辑连接表单无数据库索引 | 通过 |
| R03 | 连接摘要无 `/ dbN` | 通过 |
| R04 | 不带数据库字段且密码留空编辑连接 | 通过，凭据保留 |
| R05 | DB 0 总览与键空间 | 通过，仅看到 `qa::db0`，键数 1 |
| R06 | DB 5 总览与键空间 | 通过，仅看到 DB 5 测试键，键数 2 |
| R07 | DB 5 Hash 值预览 | 通过，显示 `selected-database` |
| R08 | 数据库选择器和导航参数保持 | 通过，保持 `database=5` |
| R09 | 非法 `database=-1` | 通过，HTTP 400 |
| R10 | 真实连接测试 | 通过，Redis 8.2.9 |
| M01 | schema 63 → 64 迁移 | 通过，移除索引列并保留连接 |
| T01 | 目标包测试 | 通过 |
| T02 | `go vet ./...` | 通过 |
| T03 | `go build ./cmd/...` | 通过 |
| T04 | `git diff --check` | 通过 |
| T05 | `go test ./... -count=1` | 通过，全部包无失败 |

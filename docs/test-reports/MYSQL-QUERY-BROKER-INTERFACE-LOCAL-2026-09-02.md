# MySQL 查询 Broker 接口修复本地测试报告

测试时间：2026-09-02（Asia/Shanghai）

测试分支：`codex/mysql-query-broker-interface`

## 结论

- 受管部署的 `brokerMySQLService` 现在显式转发完整 `QueryBackend`，对象列表、对象详情、系统数据库浏览和 SQL 控制台均可进入 Broker 持有的本地 MySQL 后端。
- 编译期接口断言防止后续改动再次静默丢失查询能力。
- 修复不改变 MySQL 凭据、授权、TLS、安全 SQL 分类或文件权限边界。

## 验证

| 项目 | 结果 |
| --- | --- |
| 修复前编译期复现 | 失败，`brokerMySQLService does not implement mysqlmanager.QueryBackend` |
| `TestBrokerMySQLServiceForwardsQueryBackend` | 通过，四种查询调用全部转发 |
| `go test ./internal/privilegebroker -count=1` | 通过 |
| `go test -p 1 ./... -count=1` | 通过 |
| `go vet -p 1 ./...` | 通过 |
| `git diff --check` | 通过 |
| 登录页 | HTTP 200 |
| 管理员登录 | HTTP 200 |
| 已认证数据库页 | HTTP 200 |
| 静态资源 | HTTP 200 |
| 匿名数据库页 | HTTP 303，跳转 `/login` |
| 未知路径 | HTTP 404 |
| stderr | 0 字节 |

## 保留部署

| 项目 | 值 |
| --- | --- |
| URL | `http://127.0.0.1:18943` |
| PID | `29200` |
| 用户名 | `admin` |
| 初始密码 | State Root 私有文件 `secrets/initial-admin-password` |
| 部署目录 | `.scratch/mysql-query-broker-interface-20260902` |

Windows 本地部署为便携单进程模式，不经过独立 Broker；实际 Broker 查询边界由编译期接口断言和定向转发测试覆盖。部署进程、State Root 和日志均保留。

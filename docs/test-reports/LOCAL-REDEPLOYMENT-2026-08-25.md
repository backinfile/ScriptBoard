# ScriptBoard 本地重新部署测试报告

## 结论

当前 `dev` 提交 `72e3be3` 已完成重新构建并切换到保留的 State Root。全仓测试、基础 HTTP 访问、真实管理员登录、受保护页面与静态资源访问均通过；部署进程在报告生成时继续运行。

## 部署信息

| 项目 | 结果 |
| --- | --- |
| 地址 | `http://127.0.0.1:18788` |
| PID | `27992` |
| 二进制 | `.scratch/database-unified-dev-deployment/scriptboard-dev-redeploy-72e3be3-20260825-172600.exe` |
| State Root | `D:\Github\worktrees\ScriptBoard\database-unified\.scratch\database-unified-deployment\state` |
| 数据库 | 复用原 `app.db`，现有测试数据保留 |
| 管理员 | `admin` |
| 密码 | State Root 私有文件 `secrets/initial-admin-password` |
| 启动时间 | `2026-08-25 17:26:42 +08:00` |

旧部署 PID `14668` 在新二进制构建及测试通过后停止。新进程使用相同 State Root 启动，没有删除或重建数据库与测试数据。

## 测试清单与结果

| 序号 | 测试项 | 结果 | 证据 |
| --- | --- | --- | --- |
| 1 | 全仓 Go 测试 | 通过 | `go test ./... -count=1` 退出码 0；含 `internal/web` 集成测试 |
| 2 | 当前源码构建 | 通过 | 生成 47,066,624 字节的 Windows 二进制 |
| 3 | 端口与进程 | 通过 | `127.0.0.1:18788` 由 PID `27992` 监听 |
| 4 | 根路径 | 通过 | `/` 返回 303，跳转 `/login` |
| 5 | 登录页 | 通过 | `/login` 返回 200 |
| 6 | 未认证保护 | 通过 | `/monitor` 返回 303，跳转 `/login` |
| 7 | 真实管理员登录 | 通过 | CSRF 校验与用户名/密码登录成功，最终进入 `/monitor` |
| 8 | 监控页 | 通过 | 登录后 `/monitor` 返回 200，响应 14,109 字节 |
| 9 | 数据库工作台 | 通过 | 登录后 `/resources/databases` 返回 200，响应 18,890 字节 |
| 10 | CSS 静态资源 | 通过 | `/assets/app.css` 返回 200，响应 539,041 字节 |
| 11 | JavaScript 静态资源 | 通过 | `/assets/app-v2.js` 返回 200，响应 419,931 字节 |
| 12 | State Root 运行元数据 | 通过 | `runtime.json` 记录 PID `27992`、schema 57 与新的 boot ID |
| 13 | 只读 doctor 核心检查 | 通过 | State Root、主密钥、审计 checkpoint、磁盘、SQLite integrity/WAL/schema、Run 日志与执行器均为 OK |

## 已知环境项

`doctor` 的默认监听端口探测为 `127.0.0.1:8787`，该端口位于本机 Windows TCP 排除范围 `8712-8811`，因此单独报告 `listen-port` 失败。这不影响本次显式配置的 `127.0.0.1:18788`；实际监听和 HTTP 验收均已通过。

## 保留状态

- 部署进程 PID `27992` 保持运行。
- 原 State Root、SQLite 数据库、数据库连接与已有测试数据保持原位。
- 登录密码继续仅保存在 State Root 的权限受限文件中，未写入本报告。

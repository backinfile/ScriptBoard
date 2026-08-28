# 数据库备份并清库本地部署测试报告

测试日期：2026-08-28（Asia/Shanghai）

## 部署信息

| 项目 | 结果 |
| --- | --- |
| 分支 | `codex/database-backup-clear` |
| Worktree | `D:\Github\worktrees\ScriptBoard\database-backup-clear` |
| 构建 | `ScriptBoard development` |
| 地址 | `http://127.0.0.1:18821` |
| 进程 | PID 42828，保持运行 |
| 管理员 | `admin`；初始密码保留在 State Root 私有文件中，未写入报告 |
| State Root | `D:\Github\worktrees\ScriptBoard\database-backup-clear\.scratch\database-backup-clear-deployment\state` |

## 测试清单与结果

| 序号 | 测试项 | 方式 | 结果 |
| --- | --- | --- | --- |
| 1 | 登录页与静态资源基础访问 | 外部 HTTP 会话 | 通过；登录页与 `/assets/app-v2.js` 返回 200 |
| 2 | 管理员登录 | 外部 HTTP 会话 | 通过；使用 State Root 私有密码登录并进入 `/monitor` |
| 3 | 数据库工作台访问 | 外部 HTTP 会话 | 通过；MySQL 测试连接和业务数据库清单可读取 |
| 4 | 备份并清库入口 | 页面契约检查 | 通过；数据库操作菜单、确认抽屉、保留数据库说明和 Lucide 图标均存在 |
| 5 | 完整名称确认 | 外部 HTTP 表单 | 通过；错误确认返回 HTTP 400，不创建清库操作 |
| 6 | 备份失败保护 | 外部 HTTP 操作 | 通过；主机缺少 `mysqldump` 时 operation 标记为 failed，测试库对象保持不变 |
| 7 | 备份并清库成功路径 | 外部 HTTP 操作 + MySQL 查询 | 通过；配置 QA Docker 客户端代理后 operation 完成并生成 safety 备份记录 |
| 8 | 数据库保留 | MySQL `information_schema` 查询 | 通过；操作后 `sb_backup_clear_20260828` schema 数量仍为 1 |
| 9 | 库内对象清空 | MySQL `information_schema` 查询 | 通过；表/视图、例程、事件、触发器数量均为 0 |
| 10 | 审计事件 | 外部 HTTP 会话 | 通过；审计页包含 `start_backup_and_clear_mysql_database` 接受事件 |
| 11 | MySQL / MariaDB 兼容矩阵 | `mysqlintegration` 容器测试 | 通过；MySQL 8.4 与 MariaDB 11.8 均覆盖表、外键、视图、触发器、过程、函数、事件及恢复 |
| 12 | 基础 404 | 外部 HTTP 会话 | 通过；未知路径返回 404 |
| 13 | Web 串行回归 | `go test ./internal/web -parallel 1 -count=1` | 通过；192.596 秒 |
| 14 | 本地监听 | PowerShell TCP 检查 | 通过；`127.0.0.1:18821` 持续监听 |
| 15 | 全量串行回归 | `go test -p 1 ./... -parallel 1 -count=1` | 通过；全部包通过 |

## 保留的测试数据

- MySQL 连接：`Backup Clear QA`，连接本机保留的 `sb-qa-mysql-plain` 容器。
- 数据库：`sb_backup_clear_20260828`，数据库本身保留，库内对象已按验收操作清空。
- 备份记录：保留一次成功的 safety 备份，以及一次用于验证备份失败保护的 failed operation。
- 部署目录、State Root、日志、QA 客户端代理和当前进程均保留，便于继续复核。

测试过程中没有执行 `DROP DATABASE`；成功路径和服务重启恢复路径均使用库内对象清理接口。

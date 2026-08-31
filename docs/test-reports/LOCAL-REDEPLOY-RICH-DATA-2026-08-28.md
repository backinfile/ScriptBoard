# 当前 dev 本地重新部署与充足测试数据报告

测试时间：2026-08-28（Asia/Shanghai）  
测试分支：`dev`  
被测提交：`a1db4ea`

## 结论

- 当前 `dev` 已重新构建并部署到 `http://127.0.0.1:18869`。
- 外部 Playwright Chromium 与 HTTP 共完成 13 项检查，13 项通过、0 项失败。
- 部署保留 4 个共享分组、12 个变量、5 个普通用户、8 个网站监控、8 个快捷执行、6 个执行计划、12 条成功运行记录和 10 个上传文件。
- 最终服务 PID 为 `43244`，仅监听 `127.0.0.1:18869`；`stderr.log` 为 0 字节。
- 测试数据、运行记录、截图、结构化结果与最终部署均保留。

## 保留部署

| 项目 | 值 |
| --- | --- |
| URL | `http://127.0.0.1:18869` |
| 登录用户 | `admin` |
| 初始密码 | 仅保留在 State Root 的 `secrets/initial-admin-password`，未写入报告 |
| 部署目录 | `.scratch/local-deploy-richdata-final-20260828` |
| State Root | `.scratch/local-deploy-richdata-final-20260828/state` |
| 宿主测试目录 | `D:\ScriptBoard-QA-20260828` |
| 结构化结果 | `.scratch/local-deploy-richdata-final-20260828/seed-results.json` |
| 截图 | `.scratch/local-deploy-richdata-final-20260828/rich-data-dashboard.png` |
| 服务日志 | `stderr.log` 为 0 字节 |

## 测试数据

| 类型 | 数量 | 示例 |
| --- | ---: | --- |
| 共享分组 | 4 | Production、Staging、Maintenance、Observability |
| 变量 | 12 | `APP_ENV`、`CANARY_PERCENT`、`BACKUP_RETENTION_DAYS` |
| 普通用户 | 5 | Maintainer 1、Operator 2、Viewer 2 |
| 网站监控 | 8 | Primary Login、Canary Login、Expected Missing Endpoint |
| 快捷执行 | 8 | Production smoke check、Backup integrity check |
| 执行计划 | 6 | Weekday production check、Nightly maintenance |
| 运行记录 | 12 | 均由实际 PowerShell 脚本执行产生且状态为 succeeded |
| 上传文件 | 10 | `sample-data-01.txt` 至 `sample-data-10.txt` |

## 验收条目

1. 登录页基础访问返回 200。
2. 匿名访问受保护页面返回 303 并跳转登录。
3. 未知路径返回 404。
4. 使用新实例真实初始密码登录管理员页面。
5. 创建并回显 4 个共享分组。
6. 创建并回显 12 个分组变量。
7. 创建并回显 5 个不同角色的普通用户。
8. 创建并回显 8 个本机 HTTP 网站监控。
9. 创建并回显 8 个快捷执行。
10. 创建并回显 6 个执行计划。
11. 启动脚本并保留 12 条成功运行记录。
12. 通过文件上传页面保留 10 个宿主测试文件。
13. 对变量、用户、快捷执行、计划和网站监控执行最终数据回显检查。

全部通过。

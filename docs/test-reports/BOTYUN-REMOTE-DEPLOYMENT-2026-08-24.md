# Botyun 远程部署测试报告（2026-08-24）

## 部署信息

| 项目 | 结果 |
| --- | --- |
| 目标主机 | SSH `botyun` |
| 对外地址 | `https://server-test.karen.fan` |
| 分支 | `dev` |
| 提交 | `9adb486b4150fa6c03c457a3e03c1b468c69026a` |
| 构建版本 | `ScriptBoard dev`，数据库 schema 53 |
| 当前目录 | `/opt/scriptboard/versions/0.0.1` |
| 状态目录 | `/var/lib/scriptboard/state-v20` |
| 登录用户 | `admin`；密码继续保存在主机私有初始密码文件中，未写入报告 |
| 部署状态 | 保留运行 |

部署前完整复制状态目录到：

`/root/.scriptboard-backups/state-v20-predeploy-dev-9adb486-20260824T030849Z`

旧版本二进制保留于：

`/opt/scriptboard/versions/0.0.1-pre-9adb486`

## 测试清单

1. 完整 Go 测试与 Web 包回归测试。
2. Go Vet 与全部命令入口构建。
3. Web、Broker、AI Socket、Runner Socket systemd 状态。
4. 以 Web 服务身份运行 Doctor。
5. HTTP 登录页、未认证重定向及静态资源访问。
6. Playwright Chromium 管理员登录。
7. Playwright 访问监控概览、主机文件、快捷执行、运行历史和账户设置。
8. 浏览器控制台、页面异常和失败请求检查。
9. 1440 × 1000 桌面视口截图检查。

## 测试结果

| 测试项 | 结果 | 证据 |
| --- | --- | --- |
| `go test ./... -count=1` | 通过 | 非 Web 包全部通过；更新页面契约后 Web 包单独复跑通过 |
| `go test ./internal/web -count=1` | 通过 | `ok scriptboard/internal/web` |
| `go vet ./...` | 通过 | 退出码 0 |
| `go build ./cmd/...` | 通过 | 退出码 0 |
| systemd 单元 | 通过 | `scriptboard.service`、`scriptboard-broker.service`、`scriptboard-ai.socket`、`scriptboard-runner.socket` 均为 `active` |
| Doctor | 通过 | 以 `scriptboard-web` 身份执行，全部检查为 `[OK]` |
| 登录页 | 通过 | HTTP 200 |
| 未认证 `/monitor` | 通过 | HTTP 303，重定向登录 |
| `app.css` 与 `app-v2.js` | 通过 | HTTP 200 |
| 管理员登录 | 通过 | Playwright 登录后进入 `/monitor` |
| 监控概览 | 通过 | HTTP 200；标题“机器状态”；本机数据显示“数据正常” |
| 主机文件 | 通过 | HTTP 200；标题“文件”；主机根目录列表正常渲染 |
| 快捷执行 | 通过 | HTTP 200；标题“快捷执行” |
| 运行历史 | 通过 | HTTP 200；标题“运行记录” |
| 账户设置 | 通过 | HTTP 200；标题“设置” |
| 浏览器异常 | 通过 | 控制台错误 0、页面异常 0、失败请求 0 |

## 部署问题与处理

首次将开发构建放入 `versions/dev-9adb486` 后，安装元数据仍声明当前版本为 `0.0.1`。Web 因此把自身判断为便携模式并尝试直接读取 Broker 所有的外部审计 checkpoint，启动时被权限边界拒绝。

保持外部审计文件权限不变，将新构建放回安装元数据声明的 `versions/0.0.1` 槽位后，Web 正确使用 Broker 依赖，原始 curl 故障信号连续两次通过。未放宽 State Root 或外部秘密目录权限。

当前部署是提交级开发构建，不是带正式 Tag 和签名清单的 Release，因此 `service verify` 会按设计拒绝把它认定为正式 Installed Release。服务运行、Doctor 和浏览器验收均正常；正式发布仍应从 `dev` 创建 release 分支和 Tag，并由现有 GitHub Actions workflow 生成安装包。

## 保留证据

- `.scratch/deploy-botyun-9adb486/remote-smoke-results.json`
- `.scratch/deploy-botyun-9adb486/monitor-overview.png`
- `.scratch/deploy-botyun-9adb486/host-files.png`
- `.scratch/deploy-botyun-9adb486/remote-smoke.mjs`

测试未创建或删除 ScriptBoard 业务数据；服务器保持当前部署运行。

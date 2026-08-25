# 本地与 botyun Playwright 部署测试报告

测试时间：2026-08-24（Asia/Shanghai）

测试分支：`dev`

测试提交：`e3d47acfb395188d116475958c5dca027c9a2b73`

结论：**通过（本地 22/22，botyun 22/22）**

## 1. 测试清单

1. 登录页、未认证重定向、JavaScript 静态资源和安全响应头。
2. Playwright Chromium 管理员登录。
3. 监控概览、网站监控、主机文件、快捷执行、运行记录、数据库和账户设置。
4. 主机文件页面的原子批量上传入口。
5. Redis 管理页面及 `verify_identity`、`disabled`、`insecure_skip_verify` 三种连接策略。
6. Redis 明文连接和跳过证书验证的风险提示。
7. `1440 × 1000` 桌面布局与 `390 × 844` 移动布局的横向溢出检查。
8. 浏览器控制台错误、页面异常和失败请求。
9. 本地进程、标准错误和登录页存活检查。
10. botyun Web、Broker、Runner Socket、Doctor、SQLite schema 和 HTTPS 存活检查。

## 2. 本地部署

| 项目 | 结果 |
| --- | --- |
| 模式 | Windows 便携部署 |
| 地址 | `http://127.0.0.1:18954` |
| 版本 | `dev-e3d47ac`；schema 54 |
| 进程 | PID `37368`，报告生成时仍在运行 |
| 部署目录 | `.scratch/deploy-local-e3d47ac` |
| State Root | `.scratch/deploy-local-e3d47ac/state` |
| 登录用户 | `admin` |
| 密码位置 | State Root 私有文件 `secrets/initial-admin-password`，未写入报告或版本库 |
| 标准错误 | 0 字节 |
| 最终登录页 | HTTP 200 |
| 部署状态 | 保留运行 |

本地使用全新 State Root 重新部署，未复用旧测试实例。Playwright 结果为 **22/22 通过**；桌面与移动页面均无横向溢出，控制台错误、页面异常和失败请求均为 0。

## 3. botyun 部署

| 项目 | 结果 |
| --- | --- |
| 目标主机 | SSH `botyun` |
| 对外地址 | `https://server-test.karen.fan` |
| 版本 | `dev-e3d47ac`；schema 54 |
| 安装槽位 | `/opt/scriptboard/versions/0.0.1` |
| State Root | `/var/lib/scriptboard/state-v20` |
| 登录用户 | `admin` |
| 密码位置 | 服务器私有文件 `/var/lib/scriptboard/state-v20/secrets/initial-admin-password`，未写入报告或版本库 |
| systemd | Web、Broker、Runner Socket 均为 `active` |
| Doctor | 全项 `[OK]`，SQLite schema 54 |
| 最终登录页 | HTTPS 200 |
| 部署后错误日志 | Web 与 Broker 无 error 级别记录 |
| 部署状态 | 保留运行 |

部署前停止三个组件并保留了可恢复副本：

- State Root：`/root/.scriptboard-backups/state-v20-predeploy-dev-e3d47ac-20260824T084346Z`
- 旧二进制：`/opt/scriptboard/versions/0.0.1-pre-e3d47ac-20260824T084346Z`

上传前后四个 Linux 二进制的 SHA-256 一致。部署后 Playwright 结果为 **22/22 通过**；桌面与移动页面均无横向溢出，控制台错误、页面异常和失败请求均为 0。

## 4. Playwright 结果

| 测试项 | 本地 | botyun |
| --- | --- | --- |
| 登录页基础访问 | 通过 | 通过 |
| CSP、`nosniff`、Frame 防护 | 通过 | 通过 |
| 未认证 `/monitor` 重定向 | 通过 | 通过 |
| 应用 JavaScript 资源 | 通过 | 通过 |
| 管理员登录 | 通过 | 通过 |
| 监控概览 | 通过 | 通过 |
| 网站监控 | 通过 | 通过 |
| 主机文件 | 通过 | 通过 |
| 原子批量上传入口 | 通过 | 通过 |
| 快捷执行 | 通过 | 通过 |
| 运行记录 | 通过 | 通过 |
| 数据库总览 | 通过 | 通过 |
| Redis 管理 | 通过 | 通过 |
| Redis 三种连接安全策略 | 通过 | 通过 |
| Redis 风险提示 | 通过 | 通过 |
| 账户设置 | 通过 | 通过 |
| 移动端监控概览 | 通过 | 通过 |
| 移动端网站监控 | 通过 | 通过 |
| 移动端 Redis 管理 | 通过 | 通过 |
| 浏览器异常 | 0 | 0 |

测试使用仓库安装的 Playwright Chromium，不使用应用内浏览器。探针只读取页面和验证表单契约，没有创建或删除业务连接、监控规则或主机文件；登录审计、部署状态、JSON 结果和截图均按要求保留。

## 5. 保留证据

本地证据：

- `.scratch/deploy-local-e3d47ac/deployment-smoke-results.json`
- `.scratch/deploy-local-e3d47ac/monitor-desktop.png`
- `.scratch/deploy-local-e3d47ac/monitor-mobile.png`
- `.scratch/deploy-local-e3d47ac/websites-desktop.png`
- `.scratch/deploy-local-e3d47ac/websites-mobile.png`
- `.scratch/deploy-local-e3d47ac/files-desktop.png`
- `.scratch/deploy-local-e3d47ac/redis-desktop.png`
- `.scratch/deploy-local-e3d47ac/redis-mobile.png`

botyun 证据：

- `.scratch/deploy-botyun-e3d47ac/deployment-smoke-results.json`
- `.scratch/deploy-botyun-e3d47ac/monitor-desktop.png`
- `.scratch/deploy-botyun-e3d47ac/monitor-mobile.png`
- `.scratch/deploy-botyun-e3d47ac/websites-desktop.png`
- `.scratch/deploy-botyun-e3d47ac/websites-mobile.png`
- `.scratch/deploy-botyun-e3d47ac/files-desktop.png`
- `.scratch/deploy-botyun-e3d47ac/redis-desktop.png`
- `.scratch/deploy-botyun-e3d47ac/redis-mobile.png`

共享 Playwright 探针保留于 `.scratch/deployment-smoke-e3d47ac.mjs`。本地进程与 botyun 三个组件在报告生成后继续运行。

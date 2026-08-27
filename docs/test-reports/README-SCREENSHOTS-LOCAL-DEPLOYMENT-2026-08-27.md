# README 截图本地部署验收（2026-08-27）

结论：**通过**。当前 `dev` 源码已重新构建并部署，使用仓库安装的 Playwright Chromium 从外部浏览器完成中英文 README 页面复验与 6 张截图更新；所有目标路由返回 HTTP 200，浏览器控制台错误和页面异常均为 0。

## 测试条目

| 条目 | 结果 |
| --- | --- |
| 登录页基础访问 | 通过，HTTP 200 |
| 管理员登录 | 通过；凭据从隔离部署密码文件读取，未写入报告 |
| 英文机器概览 | 通过，HTTP 200 |
| 英文主机文件 | 通过，HTTP 200 |
| 英文快捷执行 | 通过，HTTP 200 |
| 中文机器概览 | 通过，HTTP 200 |
| 中文主机文件 | 通过，HTTP 200 |
| 中文 Redis 数据库工作台 | 通过，HTTP 200 |
| README 图片链接 | 通过，引用的图片均存在 |
| 截图尺寸 | 通过，6 张图片均为 1440 × 1000 PNG |
| 浏览器控制台与页面异常 | 通过，均为 0 |
| Markdown 差异检查 | 通过，`git diff --check` 无错误 |

## 部署与留存

- 访问地址：`http://127.0.0.1:18790`
- 运行进程：`scriptboard-readme-current.exe`，PID `29276`
- State Root：`.scratch/readme-screenshots-20260827/state`
- 初始密码文件：`.scratch/readme-screenshots-20260827/state/secrets/initial-admin-password`
- Playwright 验证结果：`.scratch/readme-screenshots-20260827/verification-result.json`
- 演示文件：`.scratch/readme-screenshot-data-20260827/`

部署、State Root、管理员凭据、快捷执行演示数据、浏览器结果和截图均保留；报告生成后未停止进程或清理测试数据。原有 `127.0.0.1:18789` 部署未被改写。

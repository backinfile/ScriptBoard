# 主机安全修复与 botyun 部署测试报告（2026-08-24）

## 部署信息

| 项目 | 结果 |
| --- | --- |
| 目标主机 | SSH `botyun` |
| 对外地址 | `https://server-test.karen.fan` |
| 分支 | `dev` |
| 提交 | `724b0ceb501f265f14550e89d8d5e8eb19383f3f` |
| 部署目录 | `/opt/scriptboard/versions/0.0.1` |
| State Root | `/var/lib/scriptboard/state-v20` |
| 部署状态 | 保留运行 |

部署前保留了以下可恢复副本：

- State Root：`/root/.scriptboard-backups/state-v20-predeploy-dev-724b0ce-20260824T150459+0800`
- 二进制：`/opt/scriptboard/versions/0.0.1-pre-724b0ce-20260824T150459+0800`

## 修复范围

1. UFW 新建和重建规则时将名称写入原生 `comment`。
2. UFW 状态采集将规则注释解析为名称，并从地址列中移除注释文本。
3. 修改 UFW 名称会被识别为需要替换的规则变更。
4. Fail2Ban 封禁详情枚举所有活跃 Jail，不再写死 `sshd`。
5. 即使当前封禁为零，也展示每个 Jail 的当前封禁与累计封禁。
6. 所有合法 Jail 名称均可从详情中执行单 IP 解封，命令继续使用结构化参数。
7. 自定义面板当前页签不再改变背景、文字、边框颜色或增加强调阴影。

## 自动化验证

| 测试项 | 结果 |
| --- | --- |
| `go test ./... -count=1` | 通过 |
| `go vet ./...` | 通过 |
| `go build ./cmd/...` | 通过 |
| Host Security 聚焦回归测试 | 通过 |
| 自定义面板 Playwright 样式契约 | 通过 |
| `git diff --check` | 通过 |
| Impeccable 界面检测 | 已执行；报告为现有全局 CSS/模板警告，本次新增字号已对齐设计系统 |

## 远端验证

| 测试项 | 结果 | 证据 |
| --- | --- | --- |
| systemd | 通过 | Web、Broker、Runner Socket 均为 `active` |
| Doctor | 通过 | State Root、密钥、审计 checkpoint、SQLite schema 53、执行器、监听端口和服务状态均为 `[OK]` |
| 登录页 | 通过 | HTTPS 200 |
| UFW comment 命令 | 通过 | `ufw --dry-run ... comment "ScriptBoard smoke"` 退出码 0，未修改防火墙 |
| Fail2Ban 主机事实 | 通过 | 活跃 Jail：`sshd`；当前封禁：0；累计封禁：8 |
| Playwright 管理员登录 | 通过 | 外部 Chromium 成功访问主机安全和自定义面板页面 |
| Fail2Ban Jail 汇总 | 通过 | 抽屉显示 `sshd / 当前封禁 0 / 累计封禁 8` |
| UFW 名称回读 | 通过 | `22/tcp` 显示名称 `SSH`，地址保持 `Anywhere` |
| 自定义面板页签 | 通过 | 当前页签透明背景、无阴影；页面 HTTP 200 |
| 移动端 | 通过 | 390 × 844 视口无横向溢出 |
| 浏览器异常 | 通过 | 控制台错误、页面异常、失败请求均为 0 |

## 保留证据

- `.scratch/deploy-botyun-724b0ce/security-smoke-results.json`
- `.scratch/deploy-botyun-724b0ce/security-desktop.png`
- `.scratch/deploy-botyun-724b0ce/security-mobile.png`
- `.scratch/deploy-botyun-724b0ce/security-smoke.mjs`

测试未新增或删除 UFW 规则，也未改变 Fail2Ban 当前封禁。服务器保留本次部署继续运行。

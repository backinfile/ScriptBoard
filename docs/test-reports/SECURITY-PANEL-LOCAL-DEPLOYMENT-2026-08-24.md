# 安全面板增量本地部署测试报告

测试时间：2026-08-24 09:26（Asia/Shanghai）

测试提交：`b6ae795 fix: harden panel security boundaries`

测试分支：`codex/security-panel-gap-plan`
结论：**通过（35/35）**

## 1. 部署信息

| 项目 | 结果 |
| --- | --- |
| 部署模式 | Windows 便携部署；用于功能与协议验证，不替代三服务 SCM 身份/ACL 门禁 |
| 地址 | `http://127.0.0.1:18787` |
| 进程 | PID `26368`，报告生成时仍在监听 |
| 部署目录 | `.scratch/local-deploy-security-panel-gap-b6ae795` |
| State Root | `.scratch/local-deploy-security-panel-gap-b6ae795/state` |
| 测试数据目录 | `.scratch/local-security-test-data-b6ae795` |
| 管理员 | `admin`；初始密码保留在 State Root 的 `secrets/initial-admin-password`，不写入版本库 |
| 标准错误 | 0 字节 |
| 审计校验 | 31 个事件；哈希链和签名 checkpoint 均有效 |

部署、State Root、测试数据库、审计记录、Key/Entry 和测试日志均按要求保留。报告生成后没有停止进程或清理数据。

## 2. 部署态 HTTP 测试（24/24）

| # | 测试条目 | 结果 | 证据 |
| --- | --- | --- | --- |
| 1 | 登录页基础访问 | 通过 | `GET /login` → 200 |
| 2 | 安全响应头 | 通过 | CSP、`nosniff`、`DENY`、`no-store` 均存在 |
| 3 | 静态应用资源 | 通过 | `app-v2.js` → 200，447442 字节 |
| 4 | 未登录路由保护 | 通过 | `GET /monitor` → 303 `/login` |
| 5 | 错误密码 | 通过 | 登录 → 401 |
| 6 | 正确管理员登录 | 通过 | 登录 → 303 `/monitor` |
| 7 | 已登录概览页 | 通过 | `/monitor` → 200 |
| 8 | 编码斜杠 | 通过 | `/monitor%2fsecurity` → 400 |
| 9 | 编码反斜杠 | 通过 | `/monitor%5csecurity` → 400 |
| 10 | 原始反斜杠 | 通过 | `/monitor\security` → 400 |
| 11 | 点段 | 通过 | `/monitor/../login` → 400，未发生 ServeMux 重定向 |
| 12 | 重复斜杠 | 通过 | `//login` → 400 |
| 13 | absolute-form target | 通过 | `http://attacker.invalid/login` → 400 |
| 14 | 查询串中的编码分隔符 | 通过 | `/login?return_to=%2Fmonitor` → 200，不误伤合法查询值 |
| 15 | 退役 GET Trigger | 通过 | `GET /trigger/legacy/probe` → 405 |
| 16 | 无效重复 Authorization | 通过 | 两个 Bearer Header → 401 |
| 17 | 管理员 password step-up | 通过 | 页面 200，认证后 303 |
| 18 | 测试部署 External Interface 开关 | 通过 | 显式确认后启用，303 |
| 19 | 创建并保留外部 Key | 通过 | 201；最终测试 Key ID `3wZ1Yrq74FiimUgl` |
| 20 | 创建并保留日志 Entry | 通过 | 201；`local-security-probe-3` |
| 21 | 正常外部日志动作 | 通过 | 200；写入 `VALID_DEPLOYMENT_PROBE` |
| 22 | 重复 Authorization 无副作用 | 通过 | 401；日志前后均为 52 字节，无拒绝载荷 |
| 23 | 创建并保留签名 Entry | 通过 | 201；`local-signed-probe-3` |
| 24 | 重复签名证明无副作用 | 通过 | 401；签名日志文件未创建 |

HTTP 探针源码保留为部署目录中的 `http_security_probe.go`，可使用同一 State Root 复核。前两轮探针因测试资源路径位于受保护的部署根、缺少全局开关确认字段而产生 422/400；这是探针配置错误，应用按设计拒绝。由这两轮创建的 Key 和相关审计记录也按“测试数据保留”要求未删除。

## 3. 进程内安全边界测试（11/11）

这些边界依赖临时 capability、请求体流控、SQLite 竞态或恶意归档对象，无法仅从部署外部稳定精确构造；使用与部署二进制相同的提交重新运行对应集成测试。

| # | 测试条目 | 结果 |
| --- | --- | --- |
| 2 | 签名 External 请求重复证明 Header 在动作前拒绝 | 通过 |
| 3 | 签名请求暂存期间禁用 Key，执行前撤销复核阻止动作 | 通过 |
| 4 | 下游错误秘密脱敏、控制字符清理、UTF-8 和 2048 rune 上限 | 通过 |
| 5 | `template.HTML/JS/URL/CSS/HTMLAttr` 可信类型例外清单 | 通过 |
| 6 | 第一方 DOM sink 数量门禁及 DOMPurify 不可用时 fail closed | 通过 |
| 7 | MFA QR SVG 不插值 enrollment URI | 通过 |
| 8 | CSV 公式注入前缀防护 | 通过 |
| 9 | ZIP 共享恶意路径语料拒绝且不发布目标 | 通过 |
| 10 | TAR 符号链接、硬链接、FIFO、字符/块设备拒绝 | 通过 |
| 11 | State Backup 使用同一恶意归档路径语料并拒绝 | 通过 |

执行命令覆盖：

```text
go test ./internal/web -run <本轮安全边界测试集合> -count=1
go test ./internal/update -run <归档安全测试集合> -count=1
go test ./internal/statebackup -run TestStateBackupRejectsSharedUnsafeArchivePathCorpus -count=1
```

## 4. 测试界面选择

按用户要求，本报告不采用内部浏览器证据。登录、认证后页面、静态资源、响应头和安全边界均由真实监听端口上的 HTTP Cookie 会话或原始 HTTP/1.1 target 验证；没有把本地管理员密码输入浏览器。

## 5. 保留状态与已知边界

- 部署保持运行，监听 `127.0.0.1:18787`；未合并到 `dev`，也未安装 Windows SCM 服务。
- External Interface 在此测试实例中保持启用，数据库保留三轮测试 Key、最终两个 Entry 和 31 条审计事件。最终 Key 在创建 Entry 时已轮换，明文 secret 未写入报告。
- `external-probe.log` 保留正常动作记录；`signed-probe.log` 不存在，证明重复证明 Header 没有到达动作。
- 本报告证明本地便携实例的功能、HTTP 与浏览器边界，不替代 Windows SCM 服务身份、Named Pipe DACL、服务恢复和正式签名发布矩阵。

# v2.4.7 之后提交的 HEAD 本地部署黑盒与白盒测试报告

测试时间：2026-08-28（Asia/Shanghai）  
测试分支：`dev`  
基线：`1b7272a`（`v2.4.7`）  
被测提交：`2b15adc`（合并后 `HEAD`，相对基线 11 个提交）

## 结论

- 真实本地部署的 HTTP + 外部 Playwright Chromium 黑盒测试为 **30 项通过、0 项失败**，浏览器 console error 和 page error 均为 0。
- 全仓白盒 `go test -p 1 ./... -count=1`、`go vet ./...`、`go build ./cmd/...`、`go mod verify`、前端语法检查和 `git diff --check` 均通过。
- 独立浏览器契约中的 failure dialog、action-menu Top Layer、单活动 Run 链接等均通过。
- 完整 `pnpm test` 浏览器 Gate **未全通过**：在 `integration/browser/run.cjs:1599` 等待旧地址 `/config/schedules/groups/new` 超时。合并后的真实页面已统一使用 `/config/groups/new?return_to=...`；本次部署黑盒已通过新地址完成共享分组创建与五页验证，因此这是自动化 Gate 的旧选择器未随统一分组路由更新，不是部署功能失败。报告保留该失败，不将完整 Gate 记为通过。
- 部署、State Root、外部宿主测试目录、OAuth 客户端、共享分组、变量、网站监控、文件、快捷执行和多条 Run 均保留，没有删除测试数据。

## 提交与功能范围

| 提交 | 功能 | 本轮覆盖 |
| --- | --- | --- |
| `21f14b4` | step-up 身份验证保持在页面内弹窗，不跳到独立验证页 | B29、W01 |
| `f96bb4c` | MCP/OAuth 审查修复：注册、限流、撤销/令牌状态、幂等执行账本、Windows 服务生命周期 | B06–B09、W02–W04 |
| `27fe282`、`671a575` | Windows ACL / Linux POSIX 跨平台文件权限 | B19、W05–W07 |
| `ffd739a` | 文件上传冲突处理、重命名抽屉和失败弹窗 | B20–B21、W08–W09 |
| `767a88a` | 单活动 Run 直达、长操作菜单滚动与 Top Layer | B24–B25、W10–W11 |
| `383de7d`、`20c9a86` | 快捷执行、计划、变量、文件快捷访问、网站监控共享同一分组目录 | B11–B18、B26、W12–W14 |
| `4934061` | 统一分组合并后保留任务表单客户端校验 | B27、W15 |
| `6821293`、`2b15adc` | 选定快捷执行的手动启动二次确认与 schema 62 合并路径 | B22–B23、W16–W17 |

合并提交只承载集成结果，没有另列重复功能。

## 保留部署与数据

| 项目 | 值 |
| --- | --- |
| URL | `http://127.0.0.1:18861` |
| 进程 | `scriptboard-head-2b15adc.exe`，PID `34640`，报告生成时仍在监听 |
| 监听边界 | 仅 `127.0.0.1:18861` |
| 部署目录 | `.scratch/local-deploy-post-v247-20260828` |
| State Root | `.scratch/local-deploy-post-v247-20260828/state` |
| 登录用户 | `admin` |
| 初始密码 | 仅保留在 State Root 的 `secrets/initial-admin-password`，未写入报告或测试结果 |
| 外部宿主测试目录 | `D:\ScriptBoard-QA-20260828` |
| 黑盒结构化结果 | `.scratch/local-deploy-post-v247-20260828/blackbox-results.json` |
| 外部浏览器脚本 | `.scratch/local-deploy-post-v247-20260828/deployment-blackbox.cjs` |
| 截图 | `.scratch/local-deploy-post-v247-20260828/retained-head-blackbox.png` |
| 审计 | 35 个事件；哈希链与签名 checkpoint 有效 |
| 运行日志 | `stderr.log` 为 0 字节 |

保留的宿主文件包括 `qa-script.ps1`、`retained-renamed-source.txt`、`retained-upload.txt` 和冲突自动重命名产生的 `retained-upload (2).txt`。应用状态中保留共享分组 `QA Shared Group 20260828 HEAD`、变量 `QA_GROUP_VALUE`、网站监控 `QA Local Monitor 20260828`、快捷执行 `QA Confirmed Quick Run 20260828`、OAuth DCR 客户端和多条实际 Run。

## 黑盒测试条目

| ID | 测试条目 | 方式 | 结果 |
| --- | --- | --- | --- |
| B01 | 登录页基础访问返回 200 | HTTP | 通过 |
| B02 | 匿名访问快捷执行页返回 303 并跳转登录 | HTTP | 通过 |
| B03 | 未知路径返回 404 | HTTP | 通过 |
| B04 | CSP、`nosniff`、Frame 防护响应头存在 | HTTP | 通过 |
| B05 | 不允许的 Host 返回 421 | HTTP | 通过 |
| B06 | 未认证 `POST /mcp` 返回 401 与 Bearer 发现 challenge | HTTP | 通过 |
| B07 | OAuth 授权服务器与受保护资源元数据可发现 | HTTP | 通过 |
| B08 | DCR 创建无客户端密钥的公开客户端并保留 | HTTP | 通过 |
| B09 | DCR 拒绝 `client_secret_basic` | HTTP | 通过 |
| B10 | 使用真实初始密码登录管理员会话 | 外部 Playwright Chromium | 通过 |
| B11 | 从合并后的统一分组路由创建共享分组 | 外部 Playwright Chromium | 通过 |
| B12 | 快捷执行页显示真实共享分组 | 外部 Playwright Chromium | 通过 |
| B13 | 计划页显示同一共享分组 | 外部 Playwright Chromium | 通过 |
| B14 | 变量页显示同一共享分组 | 外部 Playwright Chromium | 通过 |
| B15 | 文件快捷访问异步区域显示同一共享分组 | 外部 Playwright Chromium | 通过 |
| B16 | 网站监控页显示同一共享分组 | 外部 Playwright Chromium | 通过 |
| B17 | 创建归组变量并在列表回显 | 外部 Playwright Chromium | 通过 |
| B18 | 创建本机明文 HTTP 网站监控、选择 local scope 并归组 | 外部 Playwright Chromium | 通过 |
| B19 | Windows 文件权限抽屉读取平台、ACL，所有者编辑默认收起 | 外部 Playwright Chromium | 通过 |
| B20 | 重命名抽屉执行真实文件重命名并回到文件列表 | 外部 Playwright Chromium | 通过 |
| B21 | 同名上传出现冲突弹窗，选择 Rename 后生成 `(2)` 文件 | 外部 Playwright Chromium | 通过 |
| B22 | 创建开启 `require_confirmation` 的归组快捷执行 | 外部 Playwright Chromium | 通过 |
| B23 | 手动 Run 弹出自定义确认；取消不启动，确认生成实际 Run | 外部 Playwright Chromium | 通过 |
| B24 | 左下角 Run 链接保持列表/唯一 Run 详情契约 | 外部 Playwright Chromium | 通过 |
| B25 | 长操作菜单可滚动到底且面板位于 Top Layer | 外部 Playwright Chromium | 通过 |
| B26 | 分组折叠后刷新仍恢复本页偏好 | 外部 Playwright Chromium | 通过 |
| B27 | 非法小写变量名触发客户端约束，不提交、不离开工作区 | 外部 Playwright Chromium | 通过 |
| B28 | 真实 Run 完成后保留运行详情和审计数据 | 外部 Playwright Chromium | 通过 |
| B29 | 实例名称 step-up 使用内联 dialog transport，操作后回到原设置页 | 外部 Playwright Chromium | 通过 |
| B30 | 全流程浏览器 console error / page error 为 0 | 外部 Playwright Chromium | 通过 |

## 白盒测试条目

| ID | 测试条目 | 主要覆盖 | 结果 |
| --- | --- | --- | --- |
| W01 | step-up 表单传输契约与功能测试 | `button_contract_test.go`、`step_up_feature_test.go` | 通过 |
| W02 | MCP OAuth 存储失败、令牌撤销、scope 收窄和熵源故障 | `internal/mcpaccess` | 通过 |
| W03 | MCP 幂等 claim、续租、完成记录与禁止重复动作 | `internal/mcpcommand` | 通过 |
| W04 | MCP Web 后端 HTTP 错误与权限边界 | `internal/web/mcp_*` | 通过 |
| W05 | Windows ACL 读取、规则验证和写入模型 | `permissions_windows_test.go` | 通过 |
| W06 | Linux POSIX 模式、递归预检、符号链接与受保护路径 | `permissions_linux_test.go` | 通过 |
| W07 | Broker 权限协议拒绝混合/非法字段 | `internal/privilegebroker` | 通过 |
| W08 | 文件上传冲突、缺失 Broker 目标、重命名路由与权限 | `files_test.go`、authorization tests | 通过 |
| W09 | 异步/原生失败弹窗保护当前工作区 | `failure-dialog-contract.cjs` | 通过 |
| W10 | action menu Top Layer 与滚动期间零几何重写 | `action-menu-layer-contract.cjs` | 通过 |
| W11 | 单活动 Run 的首屏与实时链接更新 | `shell-active-run-link-contract.cjs`、shell cache tests | 通过 |
| W12 | schema 61 共享分组迁移与 ID/顺序保留 | migrations / database tests | 通过 |
| W13 | 五类记录的共享分组 CRUD、影响计数、分页 | record group feature / pagination tests | 通过 |
| W14 | 调度器、变量、文件快捷访问与网站监控分组回归 | scheduler / web / websitemonitor tests | 通过 |
| W15 | 合并后 task panel 校验注册不被提前返回短路 | 前端语法、frontend contract、全仓 Web tests | 通过 |
| W16 | schema 62 快捷执行确认字段升级，旧数据默认关闭 | `quick_run_confirmation_test.go`、policy tests | 通过 |
| W17 | 确认偏好保存/回显、不增加 Quick Run 版本、启动接口保持兼容 | quick run feature / execution tests | 通过 |

## 自动化命令与结果

```powershell
node --check internal/web/ui/assets/app.js
git diff --check
go test -p 1 ./... -count=1
go vet ./...
go build ./cmd/...
go mod verify
node .scratch/local-deploy-post-v247-20260828/deployment-blackbox.cjs
pnpm test  # integration/browser；失败于旧共享分组路由选择器
```

`pnpm test` 在失败前已通过 clipboard fallback、custom dashboard drawer、custom tabs、custom dialog、failure containment、Kubernetes 合同、action menu layer、shell active Run link 和 icon tooltip layer 合同。失败产生的临时 tracked snapshot 改写已恢复；未删除部署数据或外部宿主测试数据。

## 遗留问题

更新 `integration/browser/run.cjs` 中创建计划分组等旧地址，使其使用统一 `/config/groups/new?return_to=...` 路由后，需要重新运行完整 Chromium Gate。当前报告不包含该修复，因为本次任务授权范围是部署与测试，而非修改产品或测试代码。

# 五页共享内容分组本地部署验收 — 2026-08-28

## 结论

- 全仓 `go test ./... -count=1` 与 `go vet ./...` 通过，前端脚本 `node --check` 与 `git diff --check` 通过。
- 外部 Playwright Chromium 黑盒 19 项通过、0 项失败，浏览器控制台错误为 0。
- schema 61 迁移测试确认：快捷执行分组的 ID、名称和顺序保留为共享目录；旧计划、变量、文件快捷访问和网站监控进入“未分组”。
- 当前部署与验收数据均保留。

## 保留部署

| 项目 | 值 |
| --- | --- |
| URL | `http://127.0.0.1:18857` |
| 进程 | `scriptboard-unified-grouping.exe`，PID `46196` |
| 监听 | 仅 `127.0.0.1:18857` |
| 部署目录 | `.scratch/unified-grouping-20260828` |
| State Root | `.scratch/unified-grouping-20260828/state-18857` |
| 登录用户 | `admin` |
| 初始密码 | 仅保留在 State Root 的 `secrets/initial-admin-password`，未写入报告或测试结果 |
| 结构化结果 | `.scratch/unified-grouping-20260828/blackbox-result.json` |
| 浏览器证据 | `.scratch/unified-grouping-20260828/shared-group-delete-impact.png` |

保留数据包括共享分组 `QA Shared Group 20260828` 与变量 `QA_SHARED_GROUP_VALUE=retained`。删除确认页只被检查和截图，没有提交删除。

## 测试清单

| 范围 | 结果 |
| --- | --- |
| 基础访问 | 登录页 200；匿名访问受保护页面返回 303；未知路径 404；CSP 与 `nosniff` 响应头存在 |
| 共享目录 | 在快捷执行创建一个分组后，快捷执行、计划、变量、文件快捷访问和网站监控五页均显示同一真实空分组 |
| 空分组规则 | 真实空分组持续显示；无内容时不额外生成“未分组” |
| 折叠偏好 | 四个服务端分组页分别折叠并刷新，均在本页恢复各自状态；文件快捷访问使用独立本地键 |
| 变量归组 | 通过外部浏览器创建变量并选择共享分组，列表刷新后在目标分组显示 |
| 删除影响 | 删除任务页展示五类影响计数，变量影响数为 1；未提交删除，测试数据保留 |
| 文件快捷访问 | 即使没有固定项，异步目录也返回并渲染真实空分组 |
| 浏览器质量 | 1440×1000 Chromium 完成流程，控制台错误为 0 |
| 数据迁移 | schema 60→61、schema 28 文件快捷访问升级测试通过；迁移不按名称猜测分组 |
| 回归 | 全仓 Go 测试通过，网站监控独立模块测试通过 |

## 执行命令

```powershell
node --check internal/web/ui/assets/app.js
go test ./... -count=1
go vet ./...
git diff --check
node .scratch/unified-grouping-20260828/grouping-blackbox.cjs
```

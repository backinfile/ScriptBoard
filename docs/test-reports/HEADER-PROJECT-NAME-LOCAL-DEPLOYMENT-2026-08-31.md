# 左上角项目名称自适应显示本地部署测试报告

测试时间：2026-08-31（Asia/Shanghai）  
测试分支：`codex/header-project-name`  
worktree：`D:\Github\worktrees\ScriptBoard\header-project-name`

## 结论

通过。左上角项目名称改为名称优先的两行品牌区：版本号位于名称下方，不再横向挤占名称；五个汉字保持原字号完整显示，名称继续变长时按估算字形宽度逐级缩小，达到最小字号后再使用既有省略规则。外部 Chromium 实测五字和八字名称均完整显示，未与折叠按钮重叠，浏览器 console error 和 page error 均为 0。

## 测试条目

| 编号 | 测试项 | 结果 |
| --- | --- | --- |
| T01 | 五个汉字 `脚本管理台` 保持完整字号 | 通过；`17.85px`，可用宽度 `131px`，无截断 |
| T02 | 八个汉字 `脚本管理控制面板` 自然缩小 | 通过；`14.45px`，可用宽度 `131px`，无截断 |
| T03 | 名称与侧栏折叠按钮不重叠 | 通过；名称右边界 `191px`，按钮左边界 `199px` |
| T04 | 默认英文名 `ScriptBoard` 保持完整字号 | 通过；Go 契约覆盖 |
| T05 | 32 字极长名称进入最小字号档 | 通过；Go 契约覆盖 |
| T06 | 登录页与匿名受保护页面基础访问 | 通过；`/login` 200，匿名 `/monitor` 303 |
| T07 | 外部 Chromium console/page error | 通过；均为 0 |
| T08 | 完整 Chromium 浏览器门禁 | 通过 |
| T09 | 全仓 Go 测试 | 通过；`go test -p 1 ./... -count=1` |
| T10 | `go vet ./...` 与 `go build ./cmd/...` | 通过 |
| T11 | `git diff --check` | 通过，仅有仓库既有的 Windows 换行提示 |

首次并行执行全仓 Go 测试时，Windows 因分页文件不足无法启动一个编译器进程；随后按 `-p 1` 串行重跑，全仓通过。该次失败属于本机资源争用，不是测试断言或产品失败。

Impeccable 机械检测已执行。检测器报告了 `app.css` 中既有的全局设计告警，并指出新增字号中的非设计阶梯值；新增字号随后已收敛到设计文档已有的 `.85rem`、`.72rem` 与 `.7rem` 阶梯。

## 保留的本地部署

| 项目 | 值 |
| --- | --- |
| URL | `http://127.0.0.1:18872` |
| 进程 | `scriptboard.exe`，PID `50028` |
| 监听边界 | 仅 `127.0.0.1:18872` |
| State Root | `.scratch/header-project-name-local-20260831/state` |
| 登录用户 | `admin` |
| 登录凭据 | `.scratch/header-project-name-local-20260831/login-credentials.txt` |
| 二进制 SHA-256 | `7F6E54CBFA98FACE665EF2EDF9F096F586E0647ED25002B5D4C878317702D9D6` |
| 浏览器结构化结果 | `.scratch/header-project-name-local-20260831/browser-results.json` |
| 五字截图 | `.scratch/header-project-name-local-20260831/brand-five-characters.png` |
| 八字截图 | `.scratch/header-project-name-local-20260831/brand-eight-characters.png` |
| 标准错误日志 | `.scratch/header-project-name-local-20260831/stderr-final.log`，0 字节 |

部署、State Root、八字项目名称测试数据、浏览器结果、截图和本地凭据文件均保留；报告生成后未停止进程或清理测试数据。

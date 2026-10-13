# GitHub Actions 检查与验证

日期：2026-10-13（Asia/Shanghai）。本地分支：`dev`，检查起点：`aa8c168`；初始远端 `dev`：`3f0deb2`。以下为最初诊断与本地验证记录，新版本发布将由修复提交上的 GitHub Actions 验证。

## 发现与修复

| 问题 | 证据 | 修改 |
| --- | --- | --- |
| 定时安全扫描持续失败 | 9 月 21 日、28 日和 10 月 5 日、12 日的 Gitleaks 步骤均失败；最新日志扫描 646 个有新增内容的提交，发现 11 条告警 | 逐项核对历史测试源码，添加 10 个精确指纹到 `.gitleaksignore`；同一行的两个命中共用一个指纹 |
| Tag 发布跳过秘密扫描 | v2.14.0 的 `release-security` 日志显示 `event type: push` 后直接 `No commits to scan` | 使用 Action 安装的 Gitleaks CLI 显式扫描 `--full-history HEAD`，扫描失败阻止安装包构建 |
| 漏洞扫描忽略项目 Go 版本 | 日志显示 `Both go-version and go-version-file inputs are specified, only go-version will be used`，实际使用默认 `stable` | 两个工作流显式清空 `go-version-input`，以 `go.mod` 选择工具链 |
| 项目工具链已有标准库漏洞 | Go 1.26.6 本地 `govulncheck` 返回 3，发现 12 项可达标准库漏洞，均标明修复版本 Go 1.26.9 | 将 `go.mod` 升级到 Go 1.26.9；保留现有第三方依赖版本 |
| 正式发布缺少 Linux 全量门禁 | `ci` 有 `go-linux`，正式发布只有部分包 race 与产物版本检查 | 添加 `release-linux`，在 Tag 上运行全量 test、vet、build，并加入 `installers.needs` |

安全工作流增加手动触发；PR 扫描关闭自动评论，结果仍保留在日志、摘要和扫描产物。README 与发布指南同步更新。

历史告警均来自模拟值：远端网站测试中的固定 `sbk_` 字符串、脱敏测试中的模拟 API Key / Basic 凭据 / PEM，以及仅供回环浏览器 fixture 使用的密码。豁免绑定提交、路径、规则和行号；未跳过测试目录，未缩减历史扫描范围。

近期普通 CI 与 v2.14.0 发布均成功，取消的普通 CI 属于已有并发取消机制。普通 CI 与 `security` 的提交和 PR 目标均为 `dev`，正式发布只由 Tag 触发；远端默认分支也是 `dev`。

## 验证条目

| 检查 | 结果 |
| --- | --- |
| `actionlint v1.7.11`，全部三份工作流 | 通过；本地禁用未安装的 ShellCheck / Pyflakes 扩展 |
| `go test ./scripts -count=1` 发布门禁回归 | 通过 |
| Gitleaks 8.24.3 全历史，修复前 | 返回 1；655 个有新增内容的提交、11 条命中，与远端指纹一致 |
| Gitleaks 8.24.3 全历史，修复后 | 返回 0；655 个有新增内容的提交、0 条命中 |
| 正式发布命令 `gitleaks git --redact --verbose --log-opts="--full-history HEAD"` | 返回 0；647 个有新增内容的提交、0 条命中 |
| 新提交扫描敏感性 | 将历史脱敏 fixture 放到新的测试仓库，沿用相同配置与豁免；检测到 4 条模拟秘密并返回 1，相同路径和行号仍被拦截 |
| Go 1.26.6 全量测试 | 通过 |
| Go 1.26.9 全量测试、vet、命令构建 | 全部通过；Web 包耗时 201.225 秒 |
| Go 1.26.9 `govulncheck ./...` | 返回 0；可达漏洞从 12 项降到 0 项，仍有 4 项依赖模块告警未发现调用路径 |
| 重新部署后的 HTTP 检查 | Go 1.26.9 重建 fixture 并保留原测试数据；10/10 通过 |
| `git diff --check` | 通过 |

## 本地部署

保留测试部署 `http://127.0.0.1:18931`，账号 `admin`，密码 `calibration-ledger-2026`。数据、服务日志、PID、扫描前后结果及 HTTP 检查脚本位于 `.scratch/actions-audit-20261013/`。

通过 HTTP 接口检查：登录页面、未登录访问重定向、缺少 CSRF 的登录拒绝、管理员登录，以及登录后的 `/monitor`、`/workflow`、`/resources/registries`、`/resources/workbench`、`/settings/instance` 和 `/assets/workflow.js`。共 10 项；测试数据保留。

## 验证范围

Windows 本地完成工作流语法、秘密扫描和应用验证。新增 Linux 门禁、GitHub 托管 Runner、CodeQL、签名 Environment 与正式发布依赖的实际执行，需要提交后由 GitHub Actions 验证。本次未创建、移动或修改正式 Tag / release 分支。

## v2.14.1 发布准备

- 三份工作流统一使用 Node 24 Actions：checkout / setup-go / setup-node v7、pnpm v6、Gitleaks v3、upload-artifact v7、download-artifact v8、gh-release v3；保留既有浏览器测试的 Node 22 和 pnpm 10。
- `golang.org/x/crypto` 升级到 v0.56.0，修补 GO-2026-6355、GO-2026-6354、GO-2026-6303；清理未使用的 `x/image` 依赖。
- GO-2026-5932 涉及整个 `x/crypto` 模块内已弃用的 OpenPGP 包，所有版本均无上游修复；本产品未导入该包，因此不属于可达漏洞。依据：[官方漏洞说明](https://pkg.go.dev/vuln/GO-2026-5932)。
- README 中英文版本与新版本发布说明同步更新；发布前完成本地验证并先通过 `dev` CI，再在相同提交创建新 release 分支及正式 Tag。

补充修复后的本地验证全部通过：Go 全量测试（Web 155.855 秒）、vet、所有命令构建、actionlint、655 个历史提交的 Gitleaks 扫描、Chromium 合同与完整桌面回归、重新部署后的 10 项 HTTP 检查。`govulncheck` 可达漏洞和已导入包告警均为 0，模块级告警从 4 项降为仅未导入的 OpenPGP 1 项。浏览器生成截图保存在 `.scratch/actions-audit-20261013/browser-snapshots-release-candidate/`。

运行证据：[10 月 12 日安全扫描](https://github.com/backinfile/ScriptBoard/actions/runs/38238842353)、[v2.14.0 发布](https://github.com/backinfile/ScriptBoard/actions/runs/35495752145)、[最近普通 CI](https://github.com/backinfile/ScriptBoard/actions/runs/35495298761)。

工具行为依据：[Gitleaks Action 的 Tag / Push 处理](https://github.com/gitleaks/gitleaks-action/blob/ff98106e4c7b2bc287b24eaf42907196329070c7/src/index.js)、[govulncheck Action 输入默认值](https://github.com/golang/govulncheck-action/blob/v1/action.yml)、[Go 1.26.9 安全补丁](https://go.dev/doc/devel/release#go1.26.9)。

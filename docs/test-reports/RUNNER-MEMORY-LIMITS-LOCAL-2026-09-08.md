# Runner 内存额度本地验收 · 2026-09-08

## 结果

通过。支持全局总额度、任务默认额度、Windows 单进程额度、Linux swap 额度，以及手动、一次性、快捷和计划任务的独立额度。未配置时沿用原有默认值；unlimited 只取消相应层的限制。Schema 69 保留旧任务并为 Run 记录实际选用的任务额度。

## 验证范围

| 项目 | 结果 |
| --- | --- |
| 容量解析、非法值、溢出、配置优先级 | 通过 |
| 旧 schema 升级、旧任务继承默认值 | 通过 |
| 快捷任务创建、回显、更新 revision、复制与启动 | 通过 |
| 计划任务保存及立即运行的额度传递 | 通过 |
| 一次性脚本、从源码创建任务、历史重跑字段 | 通过 |
| Windows 单进程及单次任务实际分配限制 | 通过 |
| Windows 并发任务共享总额度、自定义额度及不限额 | 通过 |
| Linux 128 MiB 下成功分配 64 MiB；32 MiB 下超限终止 | 通过 |
| Linux 两个子进程共享 64 MiB 预算、退出清理 | 通过 |
| Linux 隔离策略下的限额和环境保留 | 通过 |
| 服务额度更新保留其他 unit 属性；重复应用保持一致 | 通过 |
| 重启后默认额度从 256 MiB 改为 128 MiB | 通过 |
| 登录、概览、快捷任务、计划任务、运行历史访问 | 通过 |
| 桌面和 390px 手机表单 | 通过 |

## 自动检查

- Windows：`go test ./... -count=1`、`go vet ./...`、`go build ./cmd/...` 通过；后续调整运行了相关功能回归。
- Linux：Ubuntu WSL，Go 1.26.6；`go test -p 2 ./... -count=1` 通过。现有一次性脚本夹具需向受管 Runner 账号执行 chown，该环境以 root 执行全量测试。
- 并发：仓库 `scripts/run-race-security-gate.sh` 的全部 15 个包通过。Windows checkout 的脚本仅在临时副本中转换为 LF 后执行。
- 浏览器：`npm test --prefix integration/browser` 通过；外部 Chromium 的本地部署专项检查 16 项通过。
- Linux 实测服务：User=nobody、MemoryMax=384M、MemorySwapMax=0、TasksMax=64，保留 ProtectControlGroups、NoNewPrivileges、RestrictNamespaces、seccomp 与地址族限制；只允许自己的委派 cgroup 子树可写。测试完成后由 systemd 回收。

这些结果来自本地检查；未运行远程 GitHub Actions 或发布安装包。

## 保留的测试部署

- 地址：[http://127.0.0.1:6425](http://127.0.0.1:6425)
- 用户名：`admin`
- 密码：`Memory-Limits-Test-2026!`
- 最终配置：总额度 512 MiB，默认任务额度 128 MiB，Windows 单进程 256 MiB。
- 部署目录：`D:\Github\worktrees\ScriptBoard\runner-memory-limits\.scratch\memory-local`
- 测试脚本：同一 worktree 的 `.scratch/memory-scripts`；任务、计划与运行数据均保留。
- [专项检查记录](/D:/Github/worktrees/ScriptBoard/runner-memory-limits/.scratch/memory-local/browser-results.json)
- [桌面表单](/D:/Github/worktrees/ScriptBoard/runner-memory-limits/.scratch/memory-local/memory-form-desktop.png) · [手机表单](/D:/Github/worktrees/ScriptBoard/runner-memory-limits/.scratch/memory-local/memory-form-mobile.png)

完整日志保存在该 worktree 的 `.scratch/memory-full-tests.log`、`memory-linux-full.log`、`memory-linux-race.log`、`memory-linux-cgroup.log`、`memory-browser-gate.log` 与 `memory-vet.log`。

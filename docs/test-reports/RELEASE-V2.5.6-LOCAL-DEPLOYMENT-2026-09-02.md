# ScriptBoard v2.5.6 发布前本地部署测试报告

测试时间：2026-09-02（Asia/Shanghai）  
测试分支：`dev`  
待发布版本：`v2.5.6`

## 结论

- `v2.5.6` 的回收站幂等清理与 kubeconfig 恢复后 Kubernetes 续采修复已在合并态通过全仓 Go 测试、静态检查、构建、外部 Chromium 门禁和真实本地部署验收。
- 未签名开发安装器已完成四个平台打包，并通过 development installer 元数据与安全边界验证。
- 当前本地部署监听 `127.0.0.1:18926`，测试状态目录与进程均保留。
- 正式签名、provenance attestation、Linux 冒烟和 GitHub Release 创建由 `v2.5.6` Tag 触发的受保护 `release` workflow 执行。

## 测试清单

| 项目 | 命令或范围 | 结果 |
| --- | --- | --- |
| 全仓 Go 测试 | `go test ./... -count=1` | 通过 |
| Windows SQLite 关闭压力回归 | 4 个并发进程各运行目标用例 100 次 | 400/400 通过 |
| 静态检查 | `go vet ./...` | 通过 |
| 命令构建 | `go build ./cmd/...` | 通过 |
| 外部 Chromium 门禁 | `pnpm test`（`integration/browser`） | 通过 |
| 开发安装器打包 | `./scripts/build-release.ps1 -Version development` | 通过 |
| 开发安装器合同 | `./scripts/verify-development-installers.ps1` | 通过 |
| 补丁格式 | `git diff --check` | 通过 |

首次远端 Windows Go job 暴露出调度器启动测试绕过应用数据库所有权、额外打开 WAL 连接后偶发无法释放 `app.db` 的问题。测试已改为在 `internal/web` 的应用自有连接上完成夹具写入与验证；相同压力矩阵在修复前复现文件占用，修复后 400 次全部通过，随后全仓门禁再次通过。

## 本地部署

| 项目 | 值 |
| --- | --- |
| URL | `http://127.0.0.1:18926` |
| PID | `62332` |
| 部署目录 | `.scratch/local-release-v256-20260902` |
| State Root | `.scratch/local-release-v256-20260902/state` |
| 登录用户 | `admin` |
| 初始密码 | 仅保留在 State Root 的 `secrets/initial-admin-password` |
| 二进制 | `.scratch/local-release-v256-20260902/scriptboard-v2.5.6-rc.exe` |
| 二进制 SHA-256 | `A9D6DFD75548D46AAA9CA430EDB013EC6274B3471B5FB2118CEDFC7B0F30A49B` |
| stderr | 0 字节 |

## 基础访问验收

| 编号 | 测试 | 结果 |
| --- | --- | --- |
| B01 | 登录页返回 200 并包含 CSRF Token | 通过 |
| B02 | 匿名访问监控页重定向到登录页 | 通过 |
| B03 | 未知路由返回 404 | 通过 |
| B04 | 使用新部署生成的管理员密码登录并进入监控页 | 通过 |
| B05 | 本地测试二进制保持 `development` 元数据且不是正式发布构建 | 通过 |
| B06 | 服务只监听指定的 IPv4 回环地址和端口 | 通过 |
| B07 | 部署 stderr 为空 | 通过 |

统计：**7 项通过，0 项失败**。

Kubernetes kubeconfig 恢复专项测试、真实本地部署 HTTP 验收和保留测试状态见 `KUBERNETES-KUBECONFIG-RECOVERY-LOCAL-2026-09-01.md`；回收站幂等修复同时由全仓测试与发布候选本地部署覆盖。

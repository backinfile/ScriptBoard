# ScriptBoard v2.5.5 发布前本地部署测试报告

测试时间：2026-09-01（Asia/Shanghai）  
测试分支：`dev`  
待发布版本：`v2.5.5`

## 结论

- `v2.5.5` 的 Registry 标签历史功能已在合并态通过全仓 Go 测试、vet、构建、浏览器门禁和真实本地部署验收。
- 未签名开发安装器已完成四个平台打包，并通过 development installer 元数据与安全边界验证。
- 当前本地部署继续监听 `127.0.0.1:18895`，测试数据和状态目录均保留。
- 正式签名、provenance attestation、Linux 冒烟和 GitHub Release 创建由 `v2.5.5` Tag 触发的受保护 `release` workflow 执行。

## 测试清单

| 项目 | 命令或范围 | 结果 |
| --- | --- | --- |
| 全仓 Go 测试 | `go test ./... -count=1` | 通过 |
| 静态检查 | `go vet ./...` | 通过 |
| 命令构建 | `go build ./cmd/...` | 通过 |
| 外部 Chromium 门禁 | `pnpm test`（`integration/browser`） | 通过 |
| 开发安装器打包 | `./scripts/build-release.ps1 -Version development` | 通过 |
| 开发安装器合同 | `./scripts/verify-development-installers.ps1` | 通过 |
| 补丁格式 | `git diff --check` | 通过，仅有 Windows 换行提示 |

首轮并行执行全仓测试时，Windows 在启动 `internal/privatepath` 测试进程时返回一次 `0xc0000142`。该包随后单独运行通过；释放并行资源后顺序重跑全仓测试全部通过，未出现测试断言失败。

## 本地部署

| 项目 | 值 |
| --- | --- |
| URL | `http://127.0.0.1:18895` |
| PID | `54436` |
| 部署目录 | `.scratch/local-release-v255-20260901` |
| State Root | `.scratch/local-release-v255-20260901/state` |
| 登录用户 | `admin` |
| 初始密码 | 仅保留在 State Root 的 `secrets/initial-admin-password` |
| 二进制 | `.scratch/local-release-v255-20260901/scriptboard-v2.5.5-rc.exe` |
| 二进制 SHA-256 | `3F19BC8F132EFF8C550D07817F6320D7913ADEF8DC4812B0A5338FC082BC3F03` |
| stderr | 0 字节 |

## 基础访问验收

| 编号 | 测试 | 结果 |
| --- | --- | --- |
| B01 | 登录页返回 200 并包含用户名输入框 | 通过 |
| B02 | 匿名访问监控页重定向到登录页 | 通过 |
| B03 | 未知路由返回 404 | 通过 |
| B04 | 使用新部署生成的管理员密码登录并进入监控页 | 通过 |
| B05 | 本地测试二进制保持 `development` 元数据且不是正式发布构建 | 通过 |
| B06 | 服务只监听指定的 IPv4 回环地址和端口 | 通过 |
| B07 | 部署 stderr 为空 | 通过 |

统计：**7 项通过，0 项失败**。

Registry 多标签与逐标签元数据的专项 HTTP 和外部 Chromium 验收、截图及保留测试数据见 `REGISTRY-TAG-HISTORY-LOCAL-2026-09-01.md`。

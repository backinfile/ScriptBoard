# ScriptBoard v2.6.0 发布前本地部署测试报告

测试时间：2026-09-03（Asia/Shanghai）  
测试分支：`dev`  
待发布版本：`v2.6.0`

## 结论

- `v2.6.0` 的 Host Files 批量操作、日期排序和 TXT 预览，Redis 数据库选择与实例编辑，以及 MySQL 备份来源等变更已在合并态通过全仓 Go 测试、静态检查、构建、外部 Chromium 门禁和真实本地部署验收。
- 未签名开发安装器已完成四个平台打包，并通过 development installer 元数据与安全边界验证。
- 当前本地部署监听 `127.0.0.1:18960`，测试状态目录与进程均保留。
- 正式签名、provenance attestation、Linux 冒烟、race、fuzz、CodeQL 和 GitHub Release 创建由 `v2.6.0` Tag 触发的受保护 `release` workflow 执行。

## 测试清单

| 项目 | 命令或范围 | 结果 |
| --- | --- | --- |
| 全仓 Go 测试 | `go test ./... -count=1` | 通过 |
| 静态检查 | `go vet ./...` | 通过 |
| 命令构建 | `go build ./cmd/...` | 通过 |
| 模块完整性 | `go mod verify` | 通过 |
| 外部 Chromium 门禁 | `pnpm test`（`integration/browser`） | 通过 |
| 开发安装器打包 | `./scripts/build-release.ps1 -Version development` | 通过 |
| 开发安装器合同 | `./scripts/verify-development-installers.ps1` | 通过 |
| 补丁格式 | `git diff --check` | 通过 |

## 本地部署

| 项目 | 值 |
| --- | --- |
| URL | `http://127.0.0.1:18960` |
| PID | `65068` |
| 部署目录 | `.scratch/local-release-v260-20260903` |
| State Root | `.scratch/local-release-v260-20260903/state` |
| 登录用户 | `admin` |
| 初始密码 | 保留在 State Root 的 `secrets/initial-admin-password` |
| 二进制 | `.scratch/local-release-v260-20260903/scriptboard-v2.6.0-rc.exe` |
| 二进制 SHA-256 | `61C3205E4BA5DE7B171EAAD0B34EBA55E1B6B9F8F4884F6C0749D71BDE7977EB` |
| stderr | 0 字节 |

## 基础访问验收

| 编号 | 测试 | 结果 |
| --- | --- | --- |
| B01 | 登录页返回 200 并包含 CSRF Token | 通过 |
| B02 | 匿名访问 Host Files 重定向到登录页 | 通过，HTTP 303，目标 `/login` |
| B03 | 未知路由返回 404 | 通过 |
| B04 | 使用新部署生成的管理员密码登录 | 通过，HTTP 303，目标 `/monitor` |
| B05 | 已认证监控页、Host Files 和数据库页返回 200 | 通过 |
| B06 | Host Files 页面包含排序与预览入口，数据库页包含 Redis 入口 | 通过 |
| B07 | 本地测试二进制保持 `development` 元数据且不是正式发布构建 | 通过 |
| B08 | 服务只监听指定的 IPv4 回环地址和端口 | 通过 |
| B09 | 部署 stderr 为空 | 通过 |

统计：**9 项通过，0 项失败**。

部署态验证通过 PowerShell HTTP 客户端直接访问外部监听地址完成，没有使用应用内浏览器。测试数据、State Root、日志和进程均按本地部署约定保留。

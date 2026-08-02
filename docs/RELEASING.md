# ScriptBoard 发布指南

本文面向仓库维护者。普通用户请按根目录 README 的安装与“应用更新”章节操作，不需要接触签名密钥。

## 发布产物

正式 `vX.Y.Z` Tag 必须一次生成并发布：

- `scriptboard-vX.Y.Z-windows-amd64.zip`
- `scriptboard-vX.Y.Z-windows-arm64.zip`
- `scriptboard-vX.Y.Z-linux-amd64.tar.gz`
- `scriptboard-vX.Y.Z-linux-arm64.tar.gz`
- `SHA256SUMS`
- `release-manifest.json`
- `release-manifest.json.sig`

每个归档都必须包含与二进制一致的 `RELEASE.json`。Windows 归档包含服务、托盘、稳定托盘启动器和 updater；Linux 归档包含服务和 updater。正式发布不允许缺少签名、减少平台或使用 prerelease 版本号。

## 首次配置签名密钥

在受控的本地环境生成 Ed25519 密钥：

```powershell
go run ./cmd/scriptboard-release keygen
```

命令会向终端输出 Base64 公钥和私钥。不要把输出重定向到仓库、提交到 Git、粘贴到 Issue/PR、聊天或构建日志。把值直接保存到 GitHub 仓库的 `release` Environment Secrets：

| Secret | 内容 |
| --- | --- |
| `SCRIPTBOARD_UPDATE_KEY_ID` | 稳定且唯一的密钥标识，例如 `release-2026-01` |
| `SCRIPTBOARD_UPDATE_PUBLIC_KEY` | `public_key` |
| `SCRIPTBOARD_UPDATE_SIGNING_KEY` | `private_key` |

`release` Environment 应限制可部署分支/Tag，并为正式发布配置必要的 reviewer。私钥只应存在于该受保护 Environment 和独立的离线恢复介质中；不要创建普通 Repository Secret 副本。

可选的 `SCRIPTBOARD_UPDATE_NEXT_KEY_ID` 与 `SCRIPTBOARD_UPDATE_NEXT_PUBLIC_KEY` 只用于密钥轮换，必须同时设置。它们不会参与当前 Release 签名，只会作为第二个受信公钥编译进二进制。

## 正式发布

1. 确认工作树、版本内容和文档已经完成评审。
2. 在 Windows 与至少两个代表性 systemd Linux 环境完成服务安装、更新、验活失败回滚和人工恢复演练。
3. 确认 `go test ./... -count=1` 与 Chromium 浏览器门禁通过。
4. 创建严格稳定版 Tag，格式只能为 `vX.Y.Z`，并推送该 Tag：

   ```powershell
   git tag -a v1.2.3 -m "ScriptBoard v1.2.3"
   git push origin v1.2.3
   ```

5. GitHub Actions 的 `release` 工作流会测试、构建、签名、对 Linux amd64 二进制执行版本冒烟检查，并在全部通过后创建 GitHub Release。
6. 发布后核对 Release 不是 Draft 或 Prerelease，四个平台归档和三个验证文件齐全，并从一台已安装的上一版本主机执行“立即检查 → 下载并验证 → 安装并重启”。

Tag Release 缺少任何必需 Secret、私钥与公钥不匹配、归档数量错误或清单校验失败时，工作流必须失败，不得手工发布部分产物。

## Pi Runtime 配套资产

启用 AI 的正式 Release 必须固定一个经过兼容测试的 Pi 版本，并为每个平台另外发布
Runtime 资产、`ASSISTANT-RUNTIME.json` 及 `ASSISTANT-RUNTIME.json.sig`。Runtime
清单与主应用 `release-manifest.json` 使用同一信任根但不同的产品域和 Schema，不能把
Runtime 条目塞入既有应用更新清单。

每个 Runtime 资产至少包含：

- 与目标平台/架构匹配的唯一 `pi` 或 `pi.exe` 入口；
- Pi 启动时读取的全部同版本伴随资源（例如内置主题和原生模块）；不得只从上游归档中复制可执行文件；
- 固定的 ScriptBoard Extension（若本版本启用工具）；
- `capabilities.json` 及其显式列出的 Operational Playbook；每个资源记录稳定 ID、版本、
  相对路径、字节数和 SHA-256，且只能位于 Runtime 自身目录；
- Pi LICENSE、上游来源、精确版本、构建方式和 SHA-256 元数据；
- ScriptBoard 兼容版本范围和 RPC contract 版本；
- Runtime 自身的 `runtime.json`，其内容必须与签名清单一致。

发布流水线必须从固定上游引用获取源或资产，先验证仓库、版本和维护者记录的摘要，再在
隔离构建环境重打包；禁止追随 `latest`、执行 npm 全局安装或把维护者机器 PATH 中的 Pi
复制进 Release。正式构建需要通过真实 Pi RPC 合同测试、无工具启动测试和固定 Extension
测试，并验证 Capability Bundle 摘要/路径/重复资源校验、Profile 指导注入、旧 Runtime
确定降级，以及用户 Pi 配置、Extensions、Skills 和 session 不影响受管 Runtime。

Runtime 安装器必须对在线下载和管理员上传的离线资产执行相同检查：签名、产品域、仓库、
兼容版本、平台、架构、文件与解压大小、SHA-256、路径穿越、绝对路径、链接、特殊文件、
重复路径、许可证和唯一入口。验证及 RPC 验活完成前只能写入
`state-root/assistant/downloads/<operation-id>`；通过后移动到
`state-root/assistant/runtime/versions/<version>` 并原子更新 `active.json`。

Runtime 安装、切换和回退必须由管理员明确发起。存在活动 Agent Turn、待处理审批或正在
启动的 Pi 时拒绝切换；失败不得修改活动指针。至少保留活动版本和一个已验证回退版本，
清理不得触碰对话、Provider 凭据、Pi home、session 或 workspace。用户自行安装或更新 Pi
与此流程完全独立。

开发构建可以使用显式放入私有版本目录的 deterministic fake Pi 做 tracer bullet，但
`active.json` 必须标记 development/fixture，且该目录不得进入正式归档。

正式 Tag 构建由 `scripts/build-release.ps1` 自动调用
`scripts/build-assistant-runtime.ps1`。后者只接受 `runtime/pi-runtime-lock.json` 中固定的
Pi 版本、四个平台资产大小与 SHA-256，加入 `runtime/scriptboard-extension.ts`、LICENSE、
上游 commit 和合同元数据，再生成独立域签名的 Runtime 清单。修改 lock 或升级 Pi 后必须
重新完成四个平台打包、真实 RPC/Extension 合同测试、在线安装及回退验收。

## 本地开发构建

无需密钥即可生成明确标记为 `development` 的未签名归档：

```powershell
./scripts/build-release.ps1 -Version development
```

这些归档用于编译和包装验证。它们不会自动检查或应用更新，也不能作为正式服务更新基线。不要把 `development` 产物上传到正式 GitHub Release。

## 发布前检查

至少确认：

- `scriptboard version --json` 的 `tag`、`version`、完整 `commit`、`release_build` 与目标 Tag 一致；
- 四个归档内的 `RELEASE.json` 一致，且平台内容完整；
- `release-manifest.json` 只包含四个平台资产，名称、大小、解压大小和 SHA-256 与实际文件一致；
- 修改清单、签名或归档任意一个字节都会被拒绝；
- ZIP/TAR 路径穿越、绝对路径、符号链接/硬链接、特殊文件、重复路径、超限文件数量或大小都会被拒绝；
- 有活动 Run 时安装返回冲突且 Run 继续执行；
- 新版本启动失败、迁移失败和 HTTP 验活超时都会恢复旧版本与更新前数据库；
- 更新成功或回滚后，Web 显示终态且审计只导入一次。
- 激活和回退后的 Runtime 各自解析自己的 Capability Bundle；不能从另一版本、用户目录
  或项目目录读取同名 Playbook，缺失资源时非通用 Profile 必须拒绝 Turn。

## 密钥轮换

正常轮换必须使用桥接 Release：

1. 生成新密钥，但保留当前签名私钥。
2. 保持三个当前密钥 Secret 不变，设置 `SCRIPTBOARD_UPDATE_NEXT_KEY_ID` 和 `SCRIPTBOARD_UPDATE_NEXT_PUBLIC_KEY`。
3. 发布桥接版本。它由旧私钥签名，但二进制同时信任旧、新公钥。
4. 保留足够升级窗口并确认主要安装已进入桥接版本。
5. 把当前 key ID、公钥和私钥切换为新密钥，清空两个 next Secret，再发布后续版本。

未安装桥接版本的旧实例无法验证只由新密钥签名的最新版，需要管理员手工下载完整归档并按 README 全新安装。不要为了兼容它们重新启用已退役私钥。

如果当前私钥疑似泄露，立即撤销 GitHub Secret、停止发布并调查 GitHub 仓库与 Environment 权限。因为已安装客户端仍信任对应公钥，不能把普通桥接轮换宣称为安全撤销；恢复方案是通过可信渠道发布新基线并要求管理员手工全新安装。

## 更新故障与人工恢复

自动恢复只覆盖尚未提交的更新验证窗口。它不是通用备份/恢复工具，也不能安全撤销已经提交且产生新业务数据的版本。

如果 updater 无法确定安全恢复路径，它会保留 Operation ID 并进入 `needs_recovery`。先停止 ScriptBoard 服务、保存 State Root 与 Install Root 的现场副本，从 `state-root/updates/active.json` 读取 ID，并阅读 `state-root/updates/operations/<id>/operation.json`；若已有 `result.json` 也一并检查。确认目标 ID 后再运行：

```text
scriptboard update recover --operation <id> --confirm-operation <id>
```

不得删除或手工改写 `active.json`、安装元数据、版本目录或更新数据库快照来“清除状态”。恢复仍失败时，停止服务并从组织自己的异机备份恢复；不要使用旧二进制打开已经由新版本正常提交的数据库。

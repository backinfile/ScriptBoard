# ScriptBoard

简体中文 | [English](./README_EN.md)

> 在浏览器里管理、运行和计划一台主机上的可信脚本。

ScriptBoard 是面向单台 Windows 或 Linux 主机的自托管脚本操作台。把现有脚本放进受管目录后，即可通过浏览器管理文件、填写参数、查看实时日志、复用常用操作、设置定时计划，并追踪每一次变更和执行。

[下载最新版本](https://github.com/backinfile/ScriptBoard/releases/latest) · [快速开始](#快速开始) · [部署为系统服务](#部署为系统服务) · [排查问题](#排查问题)

> [!WARNING]
> ScriptBoard 不是安全沙箱。脚本会继承 ScriptBoard 服务进程的操作系统身份、权限和环境变量。请只运行你完全信任的脚本，并且只向可信用户开放。

![ScriptBoard 快捷执行页面](./integration/browser/snapshots/readme-quick-runs-zh.png)

## 导航

- [产品概览](#产品概览)：能力、适用边界和支持平台
- [快速开始](#快速开始)：用便携模式完成第一次运行
- [核心工作流](#核心工作流)：文件、执行、计划、监控和恢复
- [部署与运维](#部署与运维)：系统服务、更新和备份
- [配置与安全](#配置与安全)：配置优先级、网络和用户角色
- [排查问题](#排查问题)：诊断、密码重置和常见故障
- [开发](#开发)：构建、测试、发布和项目文档

## 产品概览

| 使用场景 | ScriptBoard 提供的能力 |
| --- | --- |
| 集中管理脚本 | 浏览、搜索、上传、下载、移动、重命名、预览和在线编辑受管文件 |
| 手动执行 | 运行 PowerShell、Python、Shell、Batch 和 CMD 脚本，实时查看 stdout 与 stderr |
| 复用常用操作 | 把脚本、参数模板和超时保存为快捷执行项，并进行分组、排序、复制和软锁 |
| 定时运行 | 使用五字段 Cron 创建计划，预览触发时间，并设置超时和重叠策略 |
| 观察与排障 | 查看执行历史、审计记录、宿主资源、本机应用、Docker 容器和网站端点 |
| 防止误改 | 使用应用回收站恢复误删文件；可选启用本地 Git 版本保护 |
| 安全更新 | 检查官方稳定版，由管理员确认后完成下载、签名校验、重启和失败回滚 |

Web 界面支持简体中文和美式英语，会根据浏览器语言自动选择，也可以随时切换。桌面和移动浏览器均可使用。

### 适用边界

ScriptBoard 适合个人服务器、家庭实验室、小型工作站，以及由少量可信用户共同维护的脚本主机，尤其适合以下情况：

- 脚本都位于一台固定主机，并且无需逐个注册；
- 希望用浏览器代替远程桌面或反复输入命令；
- 需要实时日志、执行历史、定时运行和基础文件恢复；
- 管理员、维护员、执行员、观察员四种固定角色足以表达权限边界。

如果你需要自定义角色、逐脚本授权、不可信代码隔离、多主机编排、任务队列、公共 API、外部通知、交互式终端或高可用部署，ScriptBoard 当前并不适合。项目也不提供正式的 Docker 部署方案。

### 支持平台

| 操作系统 | 架构 | 发布包 |
| --- | --- | --- |
| Windows 10/11、Windows Server 2019+ | amd64、arm64 | ZIP，包含服务、托盘、托盘启动器和更新程序 |
| 使用 systemd 的 Linux | amd64、arm64 | tar.gz，包含服务和更新程序 |

运行脚本前，宿主机还需要安装对应的解释器，例如 PowerShell、Python 或 Bash。ScriptBoard 会从平台默认的候选解释器中选择实际存在的程序。

## 快速开始

下面使用发布包解压目录中的 `managed` 和 `state` 文件夹进行试用，不会安装系统服务：

- `managed`：浏览器可以管理和执行的文件；
- `state`：数据库、日志、会话和登录凭据。

### 1. 下载完整发布包

从 [GitHub Releases](https://github.com/backinfile/ScriptBoard/releases/latest) 下载与系统匹配的压缩包并完整解压，不要只复制其中一个可执行文件。发布页中的 `SHA256SUMS` 可用于手工校验。

### 2. 启动便携实例

Windows PowerShell：

```powershell
New-Item -ItemType Directory -Force .\managed, .\state
.\scriptboard.exe serve --managed-root "$PWD\managed" --state-root "$PWD\state"
```

另开一个 PowerShell 窗口，在同一目录读取初始密码：

```powershell
Get-Content .\state\secrets\initial-admin-password
```

Linux：

```bash
chmod +x ./scriptboard
mkdir -p ./managed ./state
./scriptboard serve \
  --managed-root "$PWD/managed" \
  --state-root "$PWD/state"
```

另开一个终端，在同一目录读取初始密码：

```bash
cat ./state/secrets/initial-admin-password
```

### 3. 运行第一个脚本

1. 打开 <http://127.0.0.1:8787>。
2. 使用用户名 `admin` 和刚才读取的初始密码登录。
3. 前往“账户”更换初始密码。
4. 在“文件”页面上传脚本，或直接把脚本复制到 `managed`。
5. 打开脚本，选择“运行”，填写参数和超时时间后启动。
6. 在运行详情页查看实时输出、停止运行，或把配置保存为快捷执行项。

参数框使用简化的 shellwords 语法：空格分隔参数，包含空格的参数可以使用单引号或双引号。ScriptBoard 不会展开管道、重定向、通配符或命令替换。

## 核心工作流

### 管理和运行文件

- 上传单个或多个文件、新建目录、搜索和排序；
- 预览文本、Markdown、代码和常见光栅图片；
- 在线编辑不超过 1 MiB 的 UTF-8 文本；
- 直接在脚本所在目录执行，主机上的外部修改会反映到 Web 界面；
- 不跟随符号链接、Windows Junction 或跨卷挂载边界。

### 复用参数与操作

快捷执行项保存脚本路径、参数模板和超时，可分组、排序、复制和软锁。它们可以从受管脚本或历史运行中创建。

变量可以在参数模板中复用，但变量值以明文保存在 SQLite 中。“密码类型”只会在界面上默认隐藏内容，不代表加密存储，也不能替代密码保险库。

### 创建定时计划

计划使用标准五字段 Cron：

```text
分钟 小时 日 月 星期
```

例如，`0 2 * * *` 表示每天 02:00 运行。界面会显示规则摘要和未来五次触发时间。每个计划可单独设置超时，以及同一脚本正在运行时是否跳过本次触发。

服务停机期间错过的计划不会补跑。ScriptBoard 使用内置调度器，不会读取或修改系统 crontab。

### 监控与追踪

- “宿主状态”展示 CPU、内存、存储、磁盘 I/O、网络和 ScriptBoard 服务状态；
- “应用”只读聚合本机进程和 Docker 容器的资源事实，并允许 Pin 重点应用；
- “网站监控”从当前主机检查 HTTP、HTTPS、WebSocket 和 WSS 端点；
- “运行历史”和“审计”保留执行结果与高影响操作的追踪线索；
- Docker 容器和受管文本文件支持按需实时日志。

网站监控支持短期可用性、TLS 证书事实和 Nginx 候选项预览。Nginx 配置只有在管理员主动操作时才会读取，ScriptBoard 不会修改或重载 Nginx，也不会发送邮件、短信或 Webhook 通知。

### 恢复误删和误改

从 Web 界面删除或替换的文件会先进入应用回收站。需要更完整的修改历史时，可以在“设置 → 版本保护”中启用本地 Git 版本保护：

- 自动为受管文件建立变更检查点；
- 查看单个文件的历史版本；
- 通过新的本地提交恢复指定版本；
- 不执行 `push`、`pull`、`fetch` 或其他远程操作。

版本保护用于恢复误改，不是异机备份。

## 部署与运维

### 部署为系统服务

请在完整发布包的解压目录中执行安装命令。ScriptBoard 会复制到版本化安装目录：

- Windows：`C:\Program Files\ScriptBoard`
- Linux：`/opt/scriptboard`

旧式“服务直接指向单个可执行文件”的安装不兼容当前基线。如果主机上已有旧式 `ScriptBoard` 服务，请先停止并卸载旧服务，再进行全新安装。安装程序不会猜测、迁移或删除旧目录。

#### Windows

将配置保存为 `C:\ProgramData\ScriptBoard\config.yaml`：

```yaml
managed_root: C:\ProgramData\ScriptBoard\managed
state_root: C:\ProgramData\ScriptBoard\state
listen: 127.0.0.1:8787
run_timeout_grace_seconds: 30
```

在管理员 PowerShell 中运行：

```powershell
.\scriptboard.exe config validate --config C:\ProgramData\ScriptBoard\config.yaml
.\scriptboard.exe service install --config C:\ProgramData\ScriptBoard\config.yaml
.\scriptboard.exe service start
.\scriptboard.exe service status
```

Windows 服务默认以 `LocalSystem` 身份运行。安装命令还会为当前 Windows 用户配置托盘自启动；退出托盘不会停止服务。

#### Linux

将配置保存为 `/etc/scriptboard/config.yaml`：

```yaml
managed_root: /var/lib/scriptboard/managed
state_root: /var/lib/scriptboard/state
listen: 127.0.0.1:8787
run_timeout_grace_seconds: 30
```

安装并启动 systemd 服务：

```bash
sudo ./scriptboard config validate --config /etc/scriptboard/config.yaml
sudo ./scriptboard service install --config /etc/scriptboard/config.yaml
sudo /opt/scriptboard/current/scriptboard service start
sudo /opt/scriptboard/current/scriptboard service status
```

安装命令会同时创建主服务和独立的更新 helper unit。systemd 服务默认以 `root` 身份运行。卸载服务不会删除配置、受管文件、状态数据、本地 Git 历史或已安装版本目录。

### 应用更新

正式 Release 默认每 6 小时检查一次官方稳定版，但不会在后台自行安装。管理员需要先选择“下载并验证”，再选择“安装并重启”。更新流程会验证签名发布清单、归档 SHA-256、平台和归档安全性；存在活动 Run 时不会切换版本。

更新失败时，系统会恢复旧版本和更新前数据库。便携运行只能检查和下载新版本；源码 `development` 构建不会联网检查。

<details>
<summary>恢复未完成的更新事务</summary>

如果更新页显示 `needs_recovery`，不要删除 `state_root/updates` 或手工覆盖版本目录。先停止 ScriptBoard 服务，保存 Install Root 与 State Root 的现场副本，再使用页面显示的 Operation ID 执行：

```text
scriptboard update recover --operation <ID> --confirm-operation <ID>
```

如果页面无法打开，可从 `state_root/updates/active.json` 读取同一个 ID。该命令只恢复尚未提交的更新事务，不是通用降级或备份恢复工具。

</details>

不希望定期联网时，可设置：

```yaml
update_check: false
```

自动检查只访问 `backinfile/ScriptBoard` 的 GitHub Releases，不上传脚本、配置或主机信息。

### 数据与备份

请将以下位置一起纳入长期、异机备份：

| 数据 | 位置 |
| --- | --- |
| 受管文件 | `managed_root` |
| SQLite、执行日志、会话、审计和内部状态 | `state_root` |
| 服务配置 | Windows：`C:\ProgramData\ScriptBoard\config.yaml`<br>Linux：`/etc/scriptboard/config.yaml` |

应用更新所用的数据库快照和失败回滚不能替代备份。ScriptBoard 启动时会执行只向前兼容的 SQLite 迁移；不要使用旧版本打开已经由新版本升级过的 `state_root`。

## 配置与安全

### 配置优先级

```text
内置默认值 → YAML 配置 → SCRIPTBOARD_* 环境变量 → 命令行参数
```

常用配置项：

| 配置项 | 默认值 | 说明 |
| --- | --- | --- |
| `managed_root` | 平台数据目录下的 `managed` | 浏览器可以管理的唯一文件目录 |
| `state_root` | 平台数据目录下的 `state` | 数据库、日志、会话和内部状态目录 |
| `listen` | `127.0.0.1:8787` | HTTP 或 HTTPS 监听地址 |
| `tls_cert`、`tls_key` | 空 | TLS 证书和私钥；非回环监听时必须配置 |
| `trusted_proxies` | 空 | 允许提供转发头的可信代理 IP 或 CIDR |
| `git_executable` | 自动查找 | 系统 Git CLI 的绝对路径 |
| `run_timeout_grace_seconds` | `30` | 自动超时后强制结束进程树前的宽限秒数 |
| `update_check` | `true` | 是否定期检查官方稳定版；不会自动安装 |
| `update_check_interval_hours` | `6` | 自动检查间隔，允许 1–168 小时 |
| `admin_username` | 空 | 启动时覆盖系统管理员用户名 |
| `admin_password_file` | 空 | 从权限受限文件读取系统管理员启动密码 |
| `executor_chains` | 平台默认 | 按脚本扩展名覆盖解释器链 |

YAML 使用严格字段校验，未知配置项会导致验证或启动失败。修改后先运行：

```text
scriptboard config validate --config CONFIG_PATH
scriptboard doctor --config CONFIG_PATH
```

<details>
<summary>环境变量与默认解释器</summary>

支持的环境变量：

```text
SCRIPTBOARD_MANAGED_ROOT
SCRIPTBOARD_STATE_ROOT
SCRIPTBOARD_LISTEN
SCRIPTBOARD_GIT_EXECUTABLE
SCRIPTBOARD_TLS_CERT
SCRIPTBOARD_TLS_KEY
SCRIPTBOARD_TRUSTED_PROXIES
SCRIPTBOARD_RUN_TIMEOUT_GRACE_SECONDS
SCRIPTBOARD_UPDATE_CHECK
SCRIPTBOARD_UPDATE_CHECK_INTERVAL_HOURS
SCRIPTBOARD_ADMIN_USERNAME
SCRIPTBOARD_ADMIN_PASSWORD
SCRIPTBOARD_ADMIN_PASSWORD_FILE
```

默认解释器：

| 平台 | 扩展名 | 候选解释器 |
| --- | --- | --- |
| Windows | `.ps1` | `pwsh.exe` → `powershell.exe` |
| Windows | `.py` | `py.exe -3` → `python.exe` |
| Windows | `.bat`、`.cmd` | `cmd.exe` |
| Windows | `.sh` | `bash.exe` |
| Linux | `.sh` | `bash` → `sh` |
| Linux | `.py` | `python3` → `python` |
| Linux | `.ps1` | `pwsh` |

候选解释器只在脚本启动前回退。某个解释器成功启动脚本后，即使脚本执行失败，也不会换用下一个解释器重试。

</details>

### 网络边界

- 默认只监听 `127.0.0.1:8787`；
- 明文 HTTP 只允许监听回环地址；
- 监听其他地址时必须配置 `tls_cert` 和 `tls_key`；
- 使用同机 HTTPS 反向代理时，应明确配置 `trusted_proxies`；
- 所有脚本都继承服务身份，应用不会切换身份或提供容器隔离；
- 同一个 `state_root` 同时只允许一个 ScriptBoard 实例运行。

不要直接把 ScriptBoard 暴露到互联网。远程访问应通过可信 VPN、零信任网络或正确配置的 HTTPS 反向代理，并限制可访问来源。

### 用户角色

系统管理员是唯一且始终启用的账户，可以创建其他固定角色用户：

| 角色 | 权限范围 |
| --- | --- |
| 管理员 | 全部能力，包括用户管理和系统设置 |
| 维护员 | 除用户管理外的运维、文件、执行、审计和系统设置 |
| 执行员 | 查看页面和文件、启动执行；只能停止自己启动的 Run |
| 观察员 | 只读查看监控、配置摘要和历史 |

角色是实例级固定权限，不支持自定义角色或逐脚本授权。密码使用 Argon2id 哈希保存；参数变量仍是明文数据。

## 排查问题

先运行只读诊断：

```text
scriptboard doctor --config CONFIG_PATH
```

| 现象 | 优先检查 |
| --- | --- |
| 脚本无法启动 | 对应解释器是否安装；服务身份是否能读取脚本和工作目录；`executor_chains` 是否使用绝对路径 |
| 页面无法打开 | 服务状态、`listen` 地址、端口占用，以及非本机访问的 TLS 或反向代理 |
| 文件写入或 Run 被拒绝 | 磁盘可用空间是否低于 100 MiB；目标是否被活动 Run 的执行租约保护 |
| 定时计划未补跑 | 服务停机期间错过的计划按设计不会补跑 |
| 变量看似已加密 | “密码类型”只隐藏界面显示，变量仍以明文保存 |

### 重置系统管理员密码

先停止服务，再使用原配置执行：

```powershell
.\scriptboard.exe admin reset --config C:\ProgramData\ScriptBoard\config.yaml
```

Linux：

```bash
sudo scriptboard admin reset --config /etc/scriptboard/config.yaml
```

新的一次性密码会写入 `state_root/secrets/initial-admin-password`。

### 命令速查

```text
scriptboard serve
scriptboard service install|uninstall|start|stop|restart|status
scriptboard update status|check
scriptboard update recover --operation ID --confirm-operation ID
scriptboard admin reset
scriptboard config validate
scriptboard doctor
scriptboard version [--json]
```

运行 `scriptboard help` 查看完整参数。

## 开发

从源码构建需要 Go 1.26：

```powershell
go test ./... -count=1
go build ./cmd/scriptboard
go build ./cmd/scriptboard-tray
```

浏览器回归门禁使用测试专用的 Node.js 依赖，不会引入生产运行时依赖：

```powershell
cd integration/browser
pnpm install
pnpm exec playwright install chromium
pnpm test
```

构建 Windows/Linux、amd64/arm64 的便携发布包：

```powershell
./scripts/build-release.ps1 -Version development
```

正式 Tag 构建还需要发布签名密钥，详见[发布指南](./docs/RELEASING.md)。

### 项目文档

| 文档 | 用途 |
| --- | --- |
| [产品需求](./docs/PRD.md) | 产品范围与需求 |
| [验收标准](./docs/ACCEPTANCE.md) | 可验证的完成条件 |
| [数据模型与状态机](./docs/DATA-MODEL.md) | 持久化结构和状态转换 |
| [领域词汇](./CONTEXT.md) | 统一领域语言 |
| [产品与界面原则](./PRODUCT.md) | 产品定位和体验约束 |
| [设计系统](./DESIGN.md) | 视觉和交互规范 |
| [架构决策](./docs/adr/README.md) | ADR 约定、主题索引和取代关系 |
| [Chromium 浏览器门禁](./integration/browser/README.md) | 端到端回归测试说明 |

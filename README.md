# ScriptBoard

简体中文 | [English](./README_EN.md)

> 在浏览器里管理、运行和计划一台主机上的可信脚本。

ScriptBoard 是一个面向单台 Windows 或 Linux 主机的自托管脚本操作台。把现有脚本放进受管目录后，即可通过浏览器管理文件、填写参数、查看实时日志、保存常用操作、设置定时计划，并追踪每一次变更和执行。

它适合个人服务器、家庭实验室、小型工作站，以及由一名管理员维护的脚本主机。无需逐个注册脚本，也不需要代理集群、消息队列或生产环境 Node.js。

[下载最新版本](https://github.com/backinfile/ScriptBoard/releases/latest) · [5 分钟快速开始](#快速开始) · [安装为系统服务](#安装为系统服务) · [常见问题](#常见问题)

> [!WARNING]
> ScriptBoard 不是安全沙箱。脚本会继承 ScriptBoard 进程的操作系统身份、权限和环境变量。请只运行你完全信任的脚本，不要把它交给不受信任的用户使用。

![ScriptBoard 快捷执行](./integration/browser/snapshots/readme-quick-runs-zh.png)

## 你可以用它做什么

| 使用场景 | ScriptBoard 提供的能力 |
| --- | --- |
| 集中管理脚本 | 浏览、搜索、上传、下载、移动、重命名、在线预览和编辑受管文件 |
| 手动执行脚本 | 运行 PowerShell、Python、Shell、Batch 和 CMD 脚本，实时查看 stdout 与 stderr |
| 复用常用操作 | 把脚本、参数和超时设置保存为快捷执行项，并进行分组和排序 |
| 定时运行 | 使用五字段 Cron 创建计划，预览触发时间，并设置超时和重叠策略 |
| 排查问题 | 查看不可从 Web 界面删除的执行历史、运行结果和审计记录 |
| 防止误改 | 使用应用回收站恢复误删文件；可选启用本地 Git 版本保护 |
| 观察主机与应用 | 查看宿主资源，以及本机应用和 Docker 容器的 CPU、内存与磁盘 I/O；可 Pin 重点应用 |
| 保持应用更新 | 自动检查官方稳定版，由管理员确认后完成下载、校验、重启和失败回滚 |

Web 界面支持简体中文和美式英语，会根据浏览器语言自动选择，也可以在界面中随时切换。桌面和移动浏览器均可使用。

## 是否适合你

ScriptBoard 适合以下情况：

- 你在一台固定主机上维护一批可信脚本；
- 你希望用浏览器代替远程桌面或反复输入命令；
- 你需要实时日志、执行历史、定时运行和基础文件恢复；
- 这台主机由一名可信管理员负责。

如果你需要多用户权限、逐脚本授权、不可信代码隔离、多主机编排、任务队列、公共 API、通知、交互式终端或高可用部署，ScriptBoard 当前并不适合。它也不提供正式的 Docker 部署方案。

## 支持的平台

| 操作系统 | 架构 | 发布包 |
| --- | --- | --- |
| Windows 10/11、Windows Server 2019+ | amd64、arm64 | ZIP，包含服务、托盘、托盘启动器和更新程序 |
| 使用 systemd 的 Linux | amd64、arm64 | tar.gz，包含服务和更新程序 |

请从 [GitHub Releases](https://github.com/backinfile/ScriptBoard/releases/latest) 下载与系统匹配的完整压缩包，不要只复制其中一个可执行文件。发布页中的 `SHA256SUMS` 可用于手工校验；应用更新还会强制验证签名发布清单和归档 SHA-256。

运行某种脚本前，宿主机上还需要安装对应的解释器，例如 PowerShell、Python 或 Bash。ScriptBoard 会自动选择实际存在的解释器。

## 快速开始

下面使用当前目录下的 `managed` 和 `state` 文件夹进行试用，不会安装系统服务。`managed` 存放你要管理的文件，`state` 存放 ScriptBoard 的数据库、日志和登录凭据。

### Windows

解压 Windows 发布包，在 PowerShell 中进入解压目录并运行：

```powershell
New-Item -ItemType Directory -Force .\managed, .\state
.\scriptboard.exe serve --managed-root "$PWD\managed" --state-root "$PWD\state"
```

保持当前窗口运行，再打开一个 PowerShell 窗口进入同一解压目录，读取初始密码：

```powershell
Get-Content .\state\secrets\initial-admin-password
```

### Linux

解压 Linux 发布包，在终端中进入解压目录并运行：

```bash
chmod +x ./scriptboard
mkdir -p ./managed ./state
./scriptboard serve \
  --managed-root "$PWD/managed" \
  --state-root "$PWD/state"
```

保持当前终端运行，再打开一个终端进入同一解压目录，读取初始密码：

```bash
cat ./state/secrets/initial-admin-password
```

### 登录并运行第一个脚本

1. 打开 <http://127.0.0.1:8787>。
2. 使用用户名 `admin` 和上一步读取的初始密码登录。
3. 登录后前往“账户”，将初始密码更换为你自己的密码。
4. 在“文件”页面上传脚本，或者直接把现有脚本复制到 `managed` 文件夹。
5. 打开脚本，选择“运行”，填写参数和超时时间后启动。
6. 在运行详情页查看实时输出、停止运行，或把本次配置保存为快捷执行项。

参数框使用简化的 shellwords 语法：空格分隔参数，包含空格的参数可以使用单引号或双引号。ScriptBoard 不会展开管道、重定向、通配符或命令替换。

## 日常使用

### 管理文件

- 在“文件”页面上传单个或多个文件、新建目录、搜索和排序；
- 预览文本、Markdown、代码和常见光栅图片；
- 在线编辑不超过 1 MiB 的 UTF-8 文本；
- 删除的文件会先进入应用回收站，可恢复或永久清理；
- ScriptBoard 不会跟随符号链接、Windows Junction 或跨卷挂载边界。

脚本默认直接在原位置执行，工作目录就是脚本所在目录。你在主机上对受管目录所做的修改，也会直接反映到 Web 界面。

### 保存快捷执行

对于经常重复的操作，可以从文件或某次历史执行中保存快捷执行项。快捷执行项会记录脚本路径、参数模板和超时设置，可分组、排序、复制和软锁。

变量可以在参数模板中复用，但变量值以明文保存在 SQLite 中。“密码类型”只会在界面上默认隐藏内容，不代表加密存储，也不能替代密码保险库。

### 创建定时计划

计划使用标准五字段 Cron：

```text
分钟 小时 日 月 星期
```

例如，`0 2 * * *` 表示每天 02:00 运行。创建或编辑计划时，界面会显示规则摘要和未来五次触发时间。

每个计划都可以单独设置超时，以及脚本已经在运行时是否跳过本次触发。服务停机期间错过的计划不会补跑，ScriptBoard 也不会读取或修改系统 crontab。

### 恢复文件

回收站用于恢复从 Web 界面删除或替换的文件。需要更完整的修改历史时，可以在“设置 → 版本保护”中启用本地 Git 版本保护：

- 自动为受管文件建立变更检查点；
- 查看单个文件的历史版本；
- 通过一个新的本地提交恢复指定版本；
- 不会执行 `push`、`pull`、`fetch` 或其他远程操作。

版本保护用于恢复误改，不是异机备份。重要数据仍应由你自己的备份方案保护。

## 安装为系统服务

确认试用正常后，可以把 ScriptBoard 安装为开机自动运行的系统服务。请在完整发布包的解压目录中执行安装命令；ScriptBoard 会自行复制到版本化安装目录：

- Windows：`C:\Program Files\ScriptBoard`
- Linux：`/opt/scriptboard`

这是新的全新安装基线，不兼容早期“服务直接指向单个可执行文件”的安装方式。如果主机上已经存在旧式 `ScriptBoard` 服务，请先自行停止并卸载旧服务，再按本节全新安装。安装程序不会猜测、迁移或删除旧目录。

### Windows 服务

将下面的配置保存为 `C:\ProgramData\ScriptBoard\config.yaml`：

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

Windows 服务默认以 `LocalSystem` 身份运行。脚本也会继承该身份的权限和环境。

安装命令会为当前 Windows 用户配置托盘自启动。托盘可以显示服务与 HTTP 就绪状态、启停服务、打开管理页面、应用更新页和数据目录。也可以从发布包中手动运行：

```powershell
.\scriptboard-tray.exe --config C:\ProgramData\ScriptBoard\config.yaml
```

退出托盘程序不会停止 ScriptBoard 服务。

### Linux systemd 服务

将下面的配置保存为 `/etc/scriptboard/config.yaml`：

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

安装命令会同时创建主服务和独立的更新 helper unit。systemd 服务默认以 `root` 身份运行。卸载服务不会删除配置、受管文件、数据库、日志、本地 Git 历史或已安装版本目录。

## 应用更新

正式 Release 默认每 6 小时检查一次官方稳定版。打开“设置 → 应用更新”可以查看当前版本、最新版本、发布说明、签名验证状态和最近一次更新操作。

更新不会在后台自行安装。管理员点击“下载并验证”后，ScriptBoard 会先完成签名、SHA-256、平台和归档安全检查；再次点击“安装并重启”才会进入短暂停机：

1. 暂停计划触发并关闭新的 Run 入口；
2. 如果仍有活动 Run，拒绝更新且不会停止这些 Run；
3. 正常退出服务并保存一致的 SQLite 快照；
4. 切换到新版本，以只读验证模式启动；
5. 连续验活成功后恢复正常运行；启动、迁移或验活失败则自动恢复旧版本和更新前数据库。

浏览器会在重启期间自动重连。便携运行可以检查新版本，但不能从任意目录自动替换自身；源码 `development` 构建不会联网检查。

如果更新页显示 `needs_recovery`，不要删除 `state_root/updates` 或手工覆盖版本目录。先停止 ScriptBoard 服务，保存 Install Root 与 State Root 的现场副本，再使用页面显示的 Operation ID 执行；如果页面无法打开，可从 `state_root/updates/active.json` 读取同一个 ID：

```text
scriptboard update recover --operation <ID> --confirm-operation <ID>
```

该命令只恢复尚未提交的更新事务，不是通用降级或备份恢复工具。命令仍失败时，应保持服务停止，并从你自己的异机备份恢复。

自动检查只访问 `backinfile/ScriptBoard` 的 GitHub Releases，不上传脚本、配置或主机信息。若不希望定期联网，可在配置中设置：

```yaml
update_check: false
```

关闭后仍可在更新页或使用 `scriptboard update check` 手工检查。由于本功能不兼容旧式服务安装，第一个包含更新程序的版本必须全新安装；自动更新从它的下一个正式版本开始生效。

## 配置

配置优先级从低到高为：

```text
内置默认值 → YAML 配置 → SCRIPTBOARD_* 环境变量 → 命令行参数
```

常用配置项：

| 配置项 | 默认值 | 说明 |
| --- | --- | --- |
| `managed_root` | Windows：`C:\ProgramData\ScriptBoard\managed`<br>Linux：`/var/lib/scriptboard/managed` | 浏览器可以管理的唯一文件目录 |
| `state_root` | Windows：`C:\ProgramData\ScriptBoard\state`<br>Linux：`/var/lib/scriptboard/state` | 数据库、日志、会话和内部状态目录 |
| `listen` | `127.0.0.1:8787` | HTTP 或 HTTPS 监听地址 |
| `tls_cert`、`tls_key` | 空 | TLS 证书和私钥；非回环监听时必须配置 |
| `trusted_proxies` | 空 | 允许提供转发头的可信代理 IP 或 CIDR |
| `git_executable` | 自动查找 | 系统 Git CLI 的绝对路径 |
| `run_timeout_grace_seconds` | `30` | 自动超时后，强制结束进程树前的宽限秒数 |
| `update_check` | `true` | 是否定期检查官方稳定版；不会自动安装 |
| `update_check_interval_hours` | `6` | 自动检查间隔，允许 1–168 小时 |
| `admin_username` | 空 | 启动时覆盖管理员用户名 |
| `admin_password_file` | 空 | 从权限受限文件读取启动密码 |
| `executor_chains` | 平台默认 | 按脚本扩展名覆盖解释器链 |

YAML 使用严格字段校验，未知配置项会导致验证或启动失败。修改配置后建议先运行：

```text
scriptboard config validate --config CONFIG_PATH
scriptboard doctor --config CONFIG_PATH
```

<details>
<summary>查看完整配置示例</summary>

```yaml
managed_root: C:\ProgramData\ScriptBoard\managed
state_root: C:\ProgramData\ScriptBoard\state
listen: 127.0.0.1:8787

tls_cert: C:\ProgramData\ScriptBoard\tls\server.crt
tls_key: C:\ProgramData\ScriptBoard\tls\server.key
trusted_proxies:
  - 127.0.0.1/32

git_executable: C:\Program Files\Git\cmd\git.exe
run_timeout_grace_seconds: 30
update_check: true
update_check_interval_hours: 6

admin_username: admin
admin_password_file: C:\ProgramData\ScriptBoard\secrets\admin-password

executor_chains:
  .py:
    - C:\Python313\python.exe
```

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

</details>

### 默认脚本解释器

| 平台 | 扩展名 | 候选解释器 |
| --- | --- | --- |
| Windows | `.ps1` | `pwsh.exe` → `powershell.exe` |
| Windows | `.py` | `py.exe -3` → `python.exe` |
| Windows | `.bat`、`.cmd` | `cmd.exe` |
| Windows | `.sh` | `bash.exe` |
| Linux | `.sh` | `bash` → `sh` |
| Linux | `.py` | `python3` → `python` |
| Linux | `.ps1` | `pwsh` |

候选解释器只会在脚本启动前回退。某个解释器成功启动脚本后，即使脚本执行失败，也不会换用下一个解释器重试。

## 网络与安全

- 默认只监听 `127.0.0.1:8787`，不会直接暴露到局域网或互联网；
- 明文 HTTP 只允许监听回环地址；监听其他地址时必须同时配置 `tls_cert` 和 `tls_key`；
- 使用同机 HTTPS 反向代理时，应通过 `trusted_proxies` 明确配置可信代理；
- ScriptBoard 只有一个管理员账户，不提供多用户、RBAC 或逐脚本权限；
- 所有脚本都继承服务进程的运行身份，应用不会切换身份或提供容器隔离；
- 管理员密码使用 Argon2id 保存，但参数变量是明文数据；
- 同一个 `state_root` 同时只允许一个 ScriptBoard 实例运行。

不要直接把 ScriptBoard 暴露到互联网。需要远程访问时，请使用你信任的 VPN、零信任网络或正确配置的 HTTPS 反向代理，并限制可访问的来源。

## 常见问题

### 忘记管理员密码

先停止 ScriptBoard 服务，再使用原来的配置重置管理员：

```powershell
.\scriptboard.exe admin reset --config C:\ProgramData\ScriptBoard\config.yaml
```

Linux 使用：

```bash
sudo scriptboard admin reset --config /etc/scriptboard/config.yaml
```

新的初始密码会写入 `state_root` 下的 `secrets/initial-admin-password`。

## 网站监控

登录后打开“网站监控”，可以从当前主机检查 HTTP/HTTPS 和 WebSocket/WSS
端点。支持手动创建与编辑、立即检查、暂停/恢复、固定顺序、短期可用性和
TLS 证书事实。Nginx 入口只会在管理员主动操作时读取配置并预览候选项，
确认所选项后才导入；它不会修改或重载 Nginx。

WebSocket 应用层文本/二进制消息与 RFC 6455 Ping/Pong 控制帧是两套独立
成功条件。Ping/Pong 检查仅在收到载荷逐字节一致的 Pong 控制帧时成功。
ScriptBoard 当前不发送邮件、短信或 Webhook 通知。

### 脚本无法启动

先运行只读诊断：

```text
scriptboard doctor --config CONFIG_PATH
```

重点检查对应解释器是否已安装、服务账户是否有权读取脚本和工作目录，以及脚本扩展名是否在支持列表中。自定义解释器时，`executor_chains` 中必须使用绝对路径。

### 页面无法打开

- 确认进程仍在运行，并检查 `scriptboard service status`；
- 确认浏览器访问的地址与 `listen` 一致；
- 检查 `8787` 端口是否被其他程序占用；
- 非本机访问不能使用明文 HTTP，必须配置 TLS 或 HTTPS 反向代理。

### 文件或运行被拒绝

当受管目录或状态目录所在磁盘可用空间低于 100 MiB 时，ScriptBoard 会拒绝新的写入或执行。活动运行持有的脚本及其上级目录也会暂时禁止移动、修改和删除。

## 数据与升级

请将 `managed_root` 和 `state_root` 一起纳入备份：

| 数据 | 位置 | 用途 |
| --- | --- | --- |
| 受管文件 | `managed_root` | 脚本和其他用户文件 |
| 内部状态 | `state_root` | SQLite 数据库、执行日志、会话、审计和内部状态 |
| 配置 | Windows：`C:\ProgramData\ScriptBoard\config.yaml`<br>Linux：`/etc/scriptboard/config.yaml` | 服务启动配置 |

对于新版 managed service，优先使用“设置 → 应用更新”。系统会在更新验证窗口内维护自己的数据库快照和失败回滚，但这不能替代你的长期、异机备份。

便携运行仍需手工下载完整发布包、停止旧进程并从新目录启动。不要在服务运行时覆盖版本目录中的文件，也不要把某个新 EXE 单独复制进现有 managed 安装。

ScriptBoard 启动时会自动执行只向前兼容的 SQLite 迁移，并在迁移旧数据库前创建内部快照。不要使用旧版本打开已经由新版本升级过的 `state_root`。

## 命令速查

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

运行 `scriptboard help` 可以查看常用命令行参数。

## 开发者入口

从源码构建需要 Go 1.26：

```powershell
go test ./... -count=1
go build ./cmd/scriptboard
```

构建 Windows 托盘程序：

```powershell
go build ./cmd/scriptboard-tray
```

构建 Windows/Linux、amd64/arm64 的便携发布包：

```powershell
./scripts/build-release.ps1 -Version development
```

正式 Tag 构建还需要发布签名密钥。密钥生成、GitHub Environment 配置和发布步骤见 [发布指南](./docs/RELEASING.md)。

项目设计与开发资料：

- [产品需求](./docs/PRD.md)
- [验收标准](./docs/ACCEPTANCE.md)
- [数据模型与状态机](./docs/DATA-MODEL.md)
- [领域词汇](./CONTEXT.md)
- [产品与界面原则](./PRODUCT.md)
- [设计系统](./DESIGN.md)
- [架构决策](./docs/adr/README.md)
- [Chromium 浏览器门禁](./integration/browser/README.md)

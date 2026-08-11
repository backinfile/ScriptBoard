# ScriptBoard

简体中文 | [English](./README_EN.md)

> 在浏览器里管理、运行和计划一台主机上的可信脚本。

ScriptBoard 是一款面向单台 Windows 或 Linux 主机的自托管脚本操作台。无需登记或搬迁已有脚本，打开浏览器即可管理主机文件、运行脚本、查看日志并安排定时任务。

[下载最新版本](https://github.com/backinfile/ScriptBoard/releases/latest) · [快速开始](#快速开始) · [安装为系统服务](#安装为系统服务) · [常见问题](#常见问题)

> [!WARNING]
> ScriptBoard 不是安全沙箱。脚本会使用 ScriptBoard 服务的系统身份和权限运行，但只接收 ScriptBoard 提供的最小环境变量。请只运行可信脚本，只向可信用户开放，并且不要将服务直接暴露到公网。

![ScriptBoard 快捷执行页面](./integration/browser/snapshots/readme-quick-runs-zh.png)

## 主要功能

- 浏览、搜索、上传、下载、预览和编辑主机文件；
- 运行 PowerShell、Python、Shell、Batch 和 CMD 脚本，实时查看输出；
- 将常用脚本保存为快捷执行，复用参数与变量；
- 使用五字段 Cron 创建计划任务；
- 通过受限外部接口接收日志、文件上传、快捷执行和约束变量修改；
- 审查远程登录活动，并管理 Windows Defender 防火墙或 Linux UFW 与 Fail2Ban；
- 查看宿主资源、本机应用、Docker 容器、网站状态、运行历史和审计记录；
- 管理本机或远程 MySQL/MariaDB 实例，执行带校验和及安全回滚的逻辑备份与恢复；
- 通过可选的 AI 助手引用当前资源并辅助分析；
- 从 ScriptBoard 回收站恢复通过网页误删的文件；
- 在网页中检查、下载并安装经过签名验证的正式更新。

网页支持简体中文和美式英语，可在桌面或移动浏览器中使用。

## 支持环境

| 系统 | 架构 | 发布包 |
| --- | --- | --- |
| Windows 10/11、Windows Server 2019+ | amd64、arm64 | ZIP，包含服务、托盘和更新程序 |
| 使用 systemd 的 Linux | amd64、arm64 | tar.gz，包含服务和更新程序 |

请在主机上安装脚本所需的解释器，例如 PowerShell、Python 或 Bash。ScriptBoard 不提供 Docker 部署包。

## 快速开始

### 1. 下载并解压

从 [GitHub Releases](https://github.com/backinfile/ScriptBoard/releases/latest) 下载与系统和架构匹配的完整压缩包，并解压到独立目录。

### 2. 启动便携实例

Windows PowerShell：

```powershell
New-Item -ItemType Directory -Force .\state
.\scriptboard.exe serve --state-root "$PWD\state"
```

Linux：

```bash
chmod +x ./scriptboard
mkdir -p ./state
./scriptboard serve --state-root "$PWD/state"
```

### 3. 登录

打开 <http://127.0.0.1:8787>，用户名为 `admin`。初始密码位于：

```text
state/secrets/initial-admin-password
```

登录后请先在“账户”中更换密码，然后前往“文件”选择已有脚本。上传脚本或可执行文件时，
内容会先进入私有上传收件箱；核对 SHA-256 与目标并再次认证后发布，才能从文件页运行。

## 安装为系统服务

使用内置默认设置时，无需创建或传入 YAML 配置文件。ScriptBoard 默认只监听本机的 `127.0.0.1:8787`。

> [!IMPORTANT]
> 请从完整的正式发布包执行安装。若主机上仍有旧式 ScriptBoard 服务，请先停止并卸载，再进行全新安装。

### Windows

在管理员 PowerShell 中运行：

```powershell
.\scriptboard.exe service install
.\scriptboard.exe service start
.\scriptboard.exe service status
```

服务默认安装到 `C:\Program Files\ScriptBoard`，状态数据保存在 `C:\ProgramData\ScriptBoard\state`。安装时会为当前 Windows 用户配置托盘自启动。

### Linux

运行：

```bash
sudo ./scriptboard service install
sudo /opt/scriptboard/current/scriptboard service start
sudo /opt/scriptboard/current/scriptboard service status
```

服务默认安装到 `/opt/scriptboard`，状态数据保存在 `/var/lib/scriptboard/state`。

只有需要修改监听地址、TLS、状态目录等设置时，才需要创建 YAML 配置文件，并在安装时通过 `--config CONFIG_PATH` 指定。未指定时，ScriptBoard 会使用平台默认配置路径（Windows 为 `C:\ProgramData\ScriptBoard\config.yaml`，Linux 为 `/etc/scriptboard/config.yaml`）；该文件不存在时直接使用内置默认值。

以系统服务方式安装后，管理员和维护员可在“系统设置 → 更新”中重启 ScriptBoard。重启会短暂中断网页连接并停止所有活动 Run；服务恢复后页面会自动重新连接。便携运行的实例不提供此入口。

## 使用提示

### 文件与脚本

Windows 从各个可用卷开始浏览，Linux 从 `/` 开始浏览。文件页中的操作直接作用于主机文件；通过网页删除或替换的文件会先进入 ScriptBoard 回收站。未知扩展名的文件通过有界内容检测确认为 UTF-8 文本后，也可以只读预览；未通过检测的文件仍只能下载。

普通文档可直接上传。`.ps1`、`.sh`、`.py`、`.cmd`、`.bat`、原生可执行格式以及
`executor_chains` 声明的扩展只会进入“资源 → 上传收件箱”；发布需要近期密码认证、完整
摘要确认和独立审计。为避免把一次上传变成可执行覆盖，已有可执行文件不能经普通上传替换。

脚本参数用空格分隔，包含空格的参数可使用单引号或双引号。参数框不会展开管道、重定向、通配符或命令替换。

### 快捷执行与计划

运行脚本后，可将脚本路径、参数模板和超时保存为快捷执行项。每个快捷执行项都记录发布时的脚本 SHA-256 与配置修订；脚本内容变化后会拒绝启动，必须由管理员解锁、更新并重新发布。计划使用标准五字段 Cron，例如 `0 2 * * *` 表示每天 02:00 运行。服务停止期间错过的计划不会补跑。

### 外部接口

管理员和维护员可在“配置 → 外部接口”创建带有效期的 Key。每个 Key 只能绑定一个不可变的调用功能及目标：向指定且受保护路径策略约束的日志文件追加记录、提交单个文件到私有上传收件箱、启动一个快捷执行、按布尔、整数、枚举、短文本约束修改一个非密码变量，或只读开放本实例的网站监控快照。绑定功能时会自动轮换创建 Key 后显示的临时凭据，并再次只显示一次；要改变动作或目标，必须删除原功能及其专用 Key，再创建新的能力。外部快捷执行只能绑定已锁定且脚本摘要有效的发布修订，配置、锁定状态或脚本内容变化都会让旧绑定立即失效，并且同一脚本最多有一个外部 Run。外部文件不会直接进入目标目录；管理员或维护员必须在“资源 → 上传收件箱”核对文件名、目标和 SHA-256 后确认发布。

调用格式为 `POST /trigger?name=ENTRY_NAME`，并通过 `Authorization: Bearer KEY` 传递 Key。完整 Key 仅在创建或轮换成功页显示一次，服务端只保存不可逆校验值，离开页面后无法再次查看。新建功能默认要求 `X-ScriptBoard-Timestamp`、唯一 `X-ScriptBoard-Nonce` 和 `X-ScriptBoard-Signature`；签名为以完整 Key 为密钥，对换行分隔的 `v1`、Unix 时间戳、nonce、大写 HTTP 方法、原始请求路径（含查询字符串）计算的 HMAC-SHA256，时间误差不得超过 5 分钟且 nonce 不可复用。外部调用同时受每 Key、规范化来源地址、动作类型和全局四层请求/并发配额约束，超过配额统一返回低信息量的 `429`。请立即将 Key 保存到受管秘密系统，只提供给可信调用方；非本机调用必须使用 HTTPS，不再使用时应停用或轮换。

“网站监控”条目使用 `GET`。如需在另一个 ScriptBoard 中查看本实例，请复制完整调用 URL 与 Key，在接收端打开“监控 → 网站”，选择“连接其他 ScriptBoard”。远端监控会显示在独立的只读列表中，接收端不能检查、暂停、编辑、排序或删除远端项目；远端 Key 会以外部主密钥密封后保存在接收端 State Root。

### 主机安全

“监控 → 主机安全”集中显示 Windows 登录事件或 Linux SSH 登录记录、远程登录配置和防火墙状态。Windows 主机可管理 Windows Defender 防火墙规则；Linux 主机可安装 Fail2Ban 与 UFW、查看或解除 SSH 封禁，并在预览差异后同步 UFW 规则和默认策略。

所有角色都可查看检测结果；只有管理员和维护员可修改系统防护。防火墙、远程登录和封禁操作可能中断主机连接，请确认服务以管理员或 root 权限运行，并保留当前管理端口的允许规则和带外恢复方式。

### MySQL 备份与恢复

管理员和维护员可在“资源 → 数据库”登记本机或远程 MySQL/MariaDB 实例、查看数据库与核心状态、执行手动或五字段 Cron 逻辑备份，并从 `.sql` 或 `.sql.gz` 恢复。ScriptBoard 不捆绑数据库客户端；请在宿主 PATH 中安装 `mysqldump` 和 `mysql`，或在页面中配置它们的绝对路径。

每个数据库生成独立的 `.sql.gz` 和 SHA-256。恢复已有数据库或删除数据库前必须先完成安全备份并输入完整库名；恢复失败时会自动尝试回滚。默认产物位于 `state_root/database-backups/mysql`，自定义目录也会加入受保护路径。请仍将这些产物纳入独立的异机备份策略。

### AI 助手

AI 功能默认关闭。管理员可在“系统设置 → AI”安装与当前版本匹配的 Pi Runtime，再添加 OpenAI、Anthropic 或 OpenAI 兼容服务。

对话内容和明确引用的资源会发送到所选模型服务，可能产生费用。启用前请确认服务商的隐私、费用和数据驻留政策。

### 用户角色

| 角色 | 适合人群 |
| --- | --- |
| 管理员 | 管理全部功能、用户和系统设置 |
| 维护员 | 管理文件、运行、计划、监控和系统设置 |
| 执行员 | 查看普通文件并运行脚本 |
| 观察员 | 只读查看监控和历史 |

角色为实例级固定权限，暂不支持自定义角色或逐脚本授权。

## 网络与配置

默认只监听 `127.0.0.1:8787`。如需远程访问，请使用可信 VPN、零信任网络或 HTTPS 反向代理；直接监听非本机地址时必须配置 TLS 证书和私钥。

常用配置：

```yaml
state_root: C:\ProgramData\ScriptBoard\state
listen: 127.0.0.1:8787
allowed_hosts:
  - 127.0.0.1
  - localhost
canonical_external_url: http://127.0.0.1:8787
update_check: true
update_check_interval_hours: 6
```

Linux 请将 `state_root` 改为 `/var/lib/scriptboard/state`。修改配置后运行：

```text
scriptboard config validate --config CONFIG_PATH
scriptboard doctor --config CONFIG_PATH
scriptboard audit verify --config CONFIG_PATH
```

配置优先级为：内置默认值 → YAML 配置 → `SCRIPTBOARD_*` 环境变量 → 命令行参数。

`allowed_hosts` 是 Host Header 白名单；通配或非回环监听必须显式配置。`canonical_external_url` 的主机必须位于该白名单中，生成对外绝对 URL 时只使用这个值。反向代理部署还须显式配置直连代理的 `trusted_proxies`，未受信来源提供的转发头会被忽略。

网站监控默认验证 HTTPS/WSS 证书。关闭验证只会签发一小时的临时例外，页面持续显示警告，
创建或更新审计会记录到期时间；到期后自动恢复验证。连接另一台 ScriptBoard 汇聚网站状态
时必须使用 HTTPS Endpoint，HTTP、重定向、回环、私网和云元数据目标都会被拒绝。

管理员启动凭据不接受明文 `admin_password`、`SCRIPTBOARD_ADMIN_PASSWORD` 或 `--admin-password`；旧配置会以包含迁移指引的错误拒绝启动。需要启动时覆盖凭据时，只能使用绝对路径的 `admin_password_file`、`SCRIPTBOARD_ADMIN_PASSWORD_FILE` 或 `--admin-password-file`。首次启动与 `scriptboard admin reset` 仍会生成 State Root 内权限受限、修改密码后删除的一次性凭据文件。

## 更新与备份

正式版本会定期检查 GitHub Releases，但不会自动安装。管理员可在“系统设置 → 更新”中选择 GitHub 官方源或内置的公开代理源，再下载、验证并安装新版本；成功选择的源也会用于后续自动检查。公开代理的可用性不由 ScriptBoard 保证，所有来源仍必须通过签名清单、平台、大小与 SHA-256 校验；有脚本正在运行时不会切换版本。

请定期备份：

- 需要保留的主机文件；
- `state_root`，其中包含数据库、运行日志、会话、审计和 AI 数据；
- 服务使用的 `config.yaml`（如果创建了自定义配置）。

可恢复的 Provider、MySQL 与远程网站凭据只以密文存在 State Root；解密主密钥位于 State
Root 同级的 `secrets/credential-master-<实例摘要>.key`，文件页把该目录视为受保护路径。
Linux/Unix 迁移或灾难恢复必须把这个 root-only key 作为独立秘密备份；Windows key 文件
还由机器级 DPAPI 保护，只能在原主机解封，跨主机恢复需重新录入这些凭据。只复制 State
Root 不包含解密材料，这是预期安全属性。`scriptboard doctor` 会检查外部 key 是否存在。

从旧版本升级前请先备份。当前版本使用数据库 schema 41，可自动迁移 schema 20–40；更早版本的数据库和旧式配置不会自动迁移。schema 40 会记录会话认证保证级别和最近再次认证时间；新登录视为最近认证，高风险操作在 10 分钟后要求于受保护的浏览器会话中重新输入当前密码。schema 41 为审计事件增加服务端 Request ID 与认证保证字段，并把新字段纳入兼容旧事件的 v2 哈希。旧快捷执行项会以未发布状态迁移，在管理员重新保存前不能启动或绑定外部入口。旧版本中共享一个 Key 的多个外部功能会拆成独立 Key：保留最早功能的原 Key，其余功能迁移到默认停用、必须轮换并显式启用 Key 与功能后才能使用的新 Key。为保持兼容，迁移后的外部功能不会自动要求 HMAC；管理员可在编辑页启用，新建功能默认启用。外部接口页面提供持久化全局紧急开关；暂停后所有有效外部调用以低信息 503 响应拒绝且不会执行操作。审计记录形成带保留锚点与链尾的 SHA-256 哈希链；服务启动时会验证，亦可在面板不可用时运行 `scriptboard audit verify --config CONFIG_PATH` 离线检查中间记录修改、删除或截断。

面板不可用或疑似遭入侵时，可在主机本地使用带外应急命令。写操作要求重复输入固定确认值或完整 Key ID，并作为 `local-administrator` 原子写入审计链；取证导出先验证审计链，只创建新文件且不会覆盖已有证据：

```text
scriptboard emergency pause-external --confirm PAUSE-EXTERNAL --config CONFIG_PATH
scriptboard emergency revoke-key --key-id KEY_ID --confirm-key-id KEY_ID --config CONFIG_PATH
scriptboard emergency export-evidence --output ABSOLUTE_JSONL_PATH --config CONFIG_PATH
```

在隔离或断网主机上，可同时提供正式 Release 的归档、`release-manifest.json` 与 `release-manifest.json.sig`，离线验证内置签名信任根、平台、文件名、大小、SHA-256、归档边界和 `RELEASE.json`，此命令不会安装或修改当前版本：

```text
scriptboard update verify-package --archive ABSOLUTE_ARCHIVE_PATH --manifest ABSOLUTE_MANIFEST_PATH --signature ABSOLUTE_SIGNATURE_PATH
```

受管服务的当前版本文件完整但服务指针损坏时，先停止服务，再验证当前正式版本并重建指针；命令不会自动启动服务：

```text
scriptboard update repair-current --confirm REPAIR-CURRENT --config CONFIG_PATH
```

每次成功更新会保留该 operation 的更新前数据库快照。需要回滚已提交更新时，先停止服务，再重复输入 operation ID；回滚会恢复旧版本及该快照，因此会丢弃更新完成后写入 State Root 数据库的变更，应先保全现状和取证证据：

```text
scriptboard update recover --operation OPERATION_ID --confirm-operation OPERATION_ID --config CONFIG_PATH
```

正式发布的签名密钥轮换、双签、撤销和泄露处置遵循 [更新签名密钥 Runbook](./docs/UPDATE-SIGNING-KEY-RUNBOOK.md)。撤销列表嵌入客户端二进制，尚未取得包含撤销列表版本的旧客户端必须通过独立可信渠道手工升级，不能信任已泄露 Key 自己声明的撤销信息。

## 常见问题

先运行只读诊断：

```text
scriptboard doctor --config CONFIG_PATH
```

| 问题 | 优先检查 |
| --- | --- |
| 页面无法打开 | 服务状态、监听地址、端口占用和 TLS/反向代理设置 |
| 脚本无法启动 | 对应解释器是否安装，服务身份是否有权访问脚本和工作目录 |
| 文件写入或运行被拒绝 | 路径是否受保护或占用，目标磁盘可用空间是否不足 |
| 计划没有补跑 | 服务停止期间错过的计划不会补跑 |

重置管理员密码前先停止服务，然后运行：

```text
scriptboard admin reset --config CONFIG_PATH
```

新的一次性密码会写入 `state_root/secrets/initial-admin-password`。

更多命令可运行 `scriptboard help` 查看。开发、测试和发布说明请参阅 [项目文档](./docs/)。

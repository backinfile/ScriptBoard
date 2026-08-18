# ScriptBoard

简体中文 | [English](./README_EN.md)

> 在浏览器里管理、运行和计划一台主机上的可信脚本。

ScriptBoard 是一款面向单台 Windows 或 Linux 主机的自托管脚本操作台。无需登记或搬迁已有脚本，打开浏览器即可管理主机文件、运行脚本、查看日志并安排定时任务。

[下载最新版本](https://github.com/backinfile/ScriptBoard/releases/latest) · [快速开始](#快速开始) · [安装为系统服务](#安装为系统服务) · [常见问题](#常见问题)

> [!WARNING]
> ScriptBoard 不是不可信代码沙箱。脚本使用独立 Runner 的系统身份和权限运行，只接收 ScriptBoard 提供的最小环境，并受资源与默认拒绝网络边界约束；仍请只运行可信脚本、只向可信用户开放，且不要将 Web 直接暴露到公网。

![ScriptBoard 快捷执行页面](./integration/browser/snapshots/readme-quick-runs-zh.png)

## 主要功能

- 浏览、搜索、上传、下载、预览和编辑主机文件；
- 运行 PowerShell、Python、Shell、Batch 和 CMD 脚本，实时查看输出；
- 将常用脚本保存为快捷执行，复用参数与变量，并快速查看最近执行结果；
- 变量可选择文本、布尔值、整数、浮点数或 `x.y.z` 版本号格式，所有类型在运行时仍安全地作为单个字符串参数展开；
- 使用五字段 Cron 创建计划任务；
- 通过受限外部接口接收日志、文件上传、快捷执行和约束变量修改；
- 审查远程登录活动，并管理 Windows Defender 防火墙或 Linux UFW 与 Fail2Ban；
- 查看宿主资源、本机应用、Docker 容器、多个 Kubernetes 集群、网站状态、运行历史和审计记录，并创建可导入导出的自定义监控看板（含多镜像 Registry 版本卡片）；
- 管理本机或远程 MySQL/MariaDB 实例，执行带校验和及安全回滚的逻辑备份与恢复；
- 通过可选的 AI 助手引用当前资源并辅助分析；
- 从 ScriptBoard 回收站恢复通过网页误删的文件；
- 在网页中检查、下载并安装经过签名验证的正式更新。

网页支持简体中文和美式英语，可在桌面或移动浏览器中使用。

## 支持环境

| 系统 | 架构 | 发布包 |
| --- | --- | --- |
| Windows 10/11、Windows Server 2019+ | amd64、arm64 | 单文件 `*-setup.exe` 安装器 |
| 使用 systemd 的 Linux | amd64、arm64 | 单文件可执行 `.run` 安装器 |

请在主机上安装脚本所需的解释器，例如 PowerShell、Python 或 Bash。ScriptBoard 不提供 Docker 部署包。

## 快速开始

### 1. 下载并解包为便携目录

从 [GitHub Releases](https://github.com/backinfile/ScriptBoard/releases/latest) 下载与系统和架构匹配的单文件安装器。若只需便携模式，可把内嵌的完整发布内容解包到独立目录：

```powershell
.\scriptboard-vX.Y.Z-windows-amd64-setup.exe --extract-to C:\ScriptBoard-Portable
Set-Location C:\ScriptBoard-Portable
```

```bash
chmod +x ./scriptboard-vX.Y.Z-linux-amd64.run
./scriptboard-vX.Y.Z-linux-amd64.run --extract-to "$PWD/scriptboard-portable"
cd ./scriptboard-portable
```

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

便携模式只启动 Web 进程，主机防火墙、Fail2ban、UFW 与系统组件安装等特权写操作默认不可用。需要这些能力时，请使用下文的系统服务安装方式，由安装器同时注册受保护的 `scriptboard-broker` 服务。

### 3. 登录

打开 <http://127.0.0.1:8787>，用户名为 `admin`。初始密码位于：

```text
state/secrets/initial-admin-password
```

登录后请先在“账户”中更换密码，然后前往“文件”选择已有脚本。上传脚本或可执行文件时，
内容会先进入私有上传收件箱；核对 SHA-256 与目标并再次认证后发布，才能从文件页运行。

建议随后在“账户 → 双重认证”注册 Windows Hello、安全密钥或设备 passkey，或配置兼容 RFC 6238 的 TOTP 认证器。启用 TOTP 时会一次性显示 10 个
恢复码；恢复码仅保存摘要、每个只能使用一次。启用后，登录和高风险 step-up 都必须同时提供
动态验证码或未使用的恢复码，启用动作会撤销该账户已有会话。

## 安装为系统服务

使用内置默认设置时，无需创建或传入 YAML 配置文件。ScriptBoard 默认只监听本机的 `127.0.0.1:8787`。

> [!IMPORTANT]
> 请从完整的正式发布包执行安装。若主机上仍有旧式 ScriptBoard 服务，请先停止并卸载，再进行全新安装。

### Windows

下载匹配架构的正式 Setup 后，在管理员 PowerShell 中运行：

```powershell
.\scriptboard-vX.Y.Z-windows-amd64-setup.exe
```

Setup 会安全解开内嵌发布内容，整体安装、复核并启动 ScriptBoard；成功时输出产品版本和 `STATE: RUNNING`。高级诊断可使用已安装的 `scriptboard.exe service status` 和 `service verify`。若需要自定义配置，可传给 Setup，例如 `.\scriptboard-vX.Y.Z-windows-amd64-setup.exe --config C:\secure\scriptboard.yaml`。

服务默认安装到 `C:\Program Files\ScriptBoard`，状态数据保存在 `C:\ProgramData\ScriptBoard\state`。安装会初始化状态并注册 Web、`ScriptBoardBroker`、`ScriptBoardAI` 与 `ScriptBoardRunner` 四个服务；Web 使用低权限 `LocalService` 与独立服务 SID，Broker 保留 LocalSystem，防火墙、主机安全、Docker Named Pipe 与 Kubernetes 集群访问只经保护的本机 Named Pipe 进入 Broker。Web 不需要加入 `docker-users`。AI 与 Runner 使用各自的 restricted service SID 和 SCM demand-start，Web 对二者只有 `START + QUERY_STATUS`；Windows Service Hardening 默认阻断它们的网络，AI 只允许访问 IPv4/IPv6 环回 Provider 代理，Runner 无网络例外。四服务的崩溃恢复采用两次退避重启后停止的有界策略，避免永久重启风暴。安装时还会为当前 Windows 用户配置托盘自启动。

### Linux

下载匹配架构的正式 `.run` 后执行：

```bash
chmod +x ./scriptboard-vX.Y.Z-linux-amd64.run
sudo ./scriptboard-vX.Y.Z-linux-amd64.run
```

`.run` 会安全解开内嵌发布内容，整体安装、复核并启动 ScriptBoard；成功时输出产品版本和 `STATE: RUNNING`。高级诊断仍可使用 `sudo /opt/scriptboard/current/scriptboard service status` 和 `service verify`。若需要自定义配置，可传给安装器，例如 `sudo ./scriptboard-vX.Y.Z-linux-amd64.run --config /etc/scriptboard/custom.yaml`。

服务默认安装到 `/opt/scriptboard`，状态数据保存在 `/var/lib/scriptboard/state`。安装会初始化状态，创建无登录 `scriptboard-web`、`scriptboard-ai` 与 `scriptboard-runner` 系统用户，并注册 Web、Broker、AI Host 与 Runner 四个 systemd 组件；Web 与 Broker 常驻，AI Host 和 Runner 由各自受保护的 Unix Socket 按需激活，未使用 AI 或尚无 Run 时不会预先启动对应执行进程。Web 不以 root 运行，防火墙、主机安全、本机 Docker Socket 与 Kubernetes 集群访问只经校验 peer UID 的本机 Unix Socket 进入 root Broker。AI 只允许环回网络，Runner 默认无 IP 网络；两个 Runtime 服务都使用 systemd seccomp allowlist、空 capability 和资源上限。

只有需要修改监听地址、TLS、状态目录等设置时，才需要创建 YAML 配置文件，并在安装时通过 `--config CONFIG_PATH` 指定。未指定时，ScriptBoard 会使用平台默认配置路径（Windows 为 `C:\ProgramData\ScriptBoard\config.yaml`，Linux 为 `/etc/scriptboard/config.yaml`）；该文件不存在时直接使用内置默认值。

以系统服务方式安装后，管理员和维护员可在“系统设置 → 更新”中重启 ScriptBoard。重启会短暂中断网页连接并停止所有活动 Run；服务恢复后页面会自动重新连接。便携运行的实例不提供此入口。

“系统设置 → 服务日志”只读取四个固定受管服务的 systemd journal 或 Windows System Event Log。每次查询最多扫描 2,000 条、返回 500 条，可按服务、时间、级别与消息筛选并导出当前 CSV；消息在页面和导出前统一脱敏。该入口不接受任意 Unit、Windows Service 名称或文件路径。

## 使用提示

### 文件与脚本

Windows 从各个可用卷开始浏览，Linux 从 `/` 开始浏览。文件页中的操作直接作用于主机文件；通过网页删除或替换的文件会先进入 ScriptBoard 回收站。未知扩展名的文件通过有界内容检测确认为 UTF-8 文本后，也可以只读预览；未通过检测的文件仍只能下载。

批量上传完成后，页面会在弹层中逐项显示成功、跳过或失败结果；关闭弹层即可刷新当前目录，无需离开文件页。
普通文档可直接上传。`.ps1`、`.sh`、`.py`、`.cmd`、`.bat`、原生可执行格式以及
`executor_chains` 声明的扩展只会进入“资源 → 上传收件箱”；发布需要近期密码认证、完整
摘要确认和独立审计。为避免把一次上传变成可执行覆盖，已有可执行文件不能经普通上传替换。

脚本参数用空格分隔，包含空格的参数可使用单引号或双引号。参数框不会展开管道、重定向、通配符或命令替换。

运行详情将输出导航、TXT 下载和实时暂停集中在日志工具栏，可直接移到日志顶部或底部，而不改变整页滚动位置。

### 快捷执行与计划

运行脚本后，可将脚本路径、参数模板和超时保存为快捷执行项。快捷执行列表会显示最近五次执行状态、最近一次耗时，并可直接打开执行详情或脚本所在目录。每个快捷执行项同时记录发布时的脚本 SHA-256 与配置修订；脚本内容变化后会拒绝启动，必须由管理员解锁、更新并重新发布。计划使用标准五字段 Cron，例如 `0 2 * * *` 表示每天 02:00 运行。服务停止期间错过的计划不会补跑。

### Kubernetes 监控

管理员或维护员可在“监控 → Kubernetes”的“集群连接”页签配置多个集群，并在“集群监控”页签通过下拉框切换当前集群。受管部署由 Privileged Broker 读取 kubeconfig；填写 Broker 身份可读取的主机绝对路径，并可选择指定 context，默认使用 kubeconfig 的 `current-context`。首次登记或测试的 kubeconfig 必须内嵌 token、CA、客户端证书和私钥数据，不能引用外部凭据文件；已登记连接按数据库中的路径、Context 和模式精确绑定。连接默认“仅观察”，需要时可明确开启仅包含滚动重部署、单步增减副本和立即运行 CronJob 的“有限操作”。便携模式仍使用启动 ScriptBoard 的当前用户身份。

“本地管理”页签用于管理 ScriptBoard 服务身份的本机 kubeconfig：可在默认配置和已登记连接的配置路径之间切换，导入并按名称合并配置，查看、搜索、切换、编辑、重命名或删除 Context，并下载整份配置或单个 Context 的独立 YAML。导入文件限制为 2 MiB，写入采用同目录临时文件原子替换；所有修改均要求管理员或维护员权限、CSRF 校验并写入审计记录。

Kubeconfig 的 `server` 可使用 `http://` 或 `https://`。HTTPS 连接按 kubeconfig 的 CA、客户端证书或系统信任验证，也保留显式 `insecure-skip-tls-verify` 选择；该选项仍加密传输但无法认证 API Server 身份，可能遭受中间人攻击。HTTP 连接也可使用静态 token 或基本认证，但凭据与集群数据会以明文传输。ScriptBoard 不保存 kubeconfig 中的 token、证书或私钥，并拒绝 `exec`/`auth-provider` 登录插件。若服务以 systemd 或 Windows 服务运行，请确认该服务身份可以读取 kubeconfig 及其引用的 CA、token 或客户端证书文件。

### 自定义看板与网站监控

管理员和维护员可在“配置 → 自定义看板”组合外部 JSON 数据与已有网站监控结果，创建数字、百分比、额度、键值、网站和 Registry 卡片，强制刷新当前看板的数据，并在当前看板导入或导出所选节点配置。JSON 数据源和 Registry 均支持 HTTP 与 HTTPS；Registry 支持匿名访问或由 Broker 加密保存的用户名与密码/Token，Bearer token 服务也可独立使用 HTTP 或 HTTPS。HTTP 会明文传输请求头、凭据和响应。系统管理员可对已保存的 HTTP Registry 卡片执行近期认证后，将其主机幂等注册到 Docker Engine 的 `insecure-registries`；该操作不会把 Registry 凭据写入 Docker 配置，也不会自动重启 Docker Engine。卡片支持使用尚未保存的配置测试请求、从 JSON 结构选择取值字段；刷新失败时保留最后一次成功值，并为有权限的用户提供脱敏请求诊断。测试响应不会写入数据库或审计记录。导入导出保留 URL scheme，Registry 密码不进入导出文件。公开看板只展示通用结果状态，不公开数据源、请求头、公式或诊断信息。

网站检查支持 HTTP、HTTPS、WS 和 WSS，可配置自定义 HTTP/握手请求头，并使用 `{{VARIABLE_NAME}}` 在执行检查时引用变量；导入导出会保留 scheme 与 TLS 验证选项。如需传递密钥，请使用密码变量，避免把密钥直接写入可导出的监控配置。需要汇总多台 ScriptBoard 时，可通过 HTTP 或 HTTPS 的受限外部接口把另一实例的网站监控快照接入当前实例；HTTP 会明文传输 Key 与监控响应。

### 外部接口

管理员和维护员可在“配置 → 外部接口”创建带有效期的 Key。每个 Key 只能绑定一个不可变的调用功能及目标：向指定且受保护路径策略约束的日志文件追加记录、提交单个文件到私有上传收件箱、启动一个快捷执行、按布尔、整数、枚举、短文本约束修改一个非密码变量，或只读开放本实例的网站监控快照。绑定功能时会自动轮换创建 Key 后显示的临时凭据，并再次只显示一次；要改变动作或目标，必须删除原功能及其专用 Key，再创建新的能力。外部快捷执行只能绑定已锁定且脚本摘要有效的发布修订，配置、锁定状态或脚本内容变化都会让旧绑定立即失效，并且同一脚本最多有一个外部 Run。外部文件不会直接进入目标目录；管理员或维护员必须在“资源 → 上传收件箱”核对文件名、目标和 SHA-256 后确认发布。

调用格式为 `POST /trigger/GROUP_NAME/ENTRY_NAME`，并通过 `Authorization: Bearer KEY` 传递 Key。完整 Key 仅在创建或轮换成功页显示一次，服务端只保存不可逆校验值，离开页面后无法再次查看。新建功能默认要求 `X-ScriptBoard-Timestamp`、唯一 `X-ScriptBoard-Nonce` 和 `X-ScriptBoard-Signature`；v2 签名以完整 Key 为密钥，依次签入 Unix 时间戳、nonce、大写 HTTP 方法、原始请求路径（含查询字符串）、原始 `Content-Type`、实际请求体字节数和请求体 SHA-256。multipart 摘要覆盖包括 boundary 在内的实际传输字节，时间误差不得超过 5 分钟且 nonce 不可复用。所有调用在数据库解析 Key 前先受规范化来源地址和全局配额约束；认证后继续执行每 Key、来源、动作和全局请求/并发配额，超过配额统一返回低信息量的 `429`。认证失败写入不包含 Key 的有界审计证据。请立即将 Key 保存到受管秘密系统，只提供给可信调用方；HTTP 调用会明文传输 Key、签名材料与请求内容，应优先使用 HTTPS 或受信网络，不再使用时应停用或轮换。

“网站监控”条目使用 `GET`。如需在另一个 ScriptBoard 中查看本实例，请复制完整调用 URL 与 Key，在接收端打开“监控 → 网站”，选择“连接其他 ScriptBoard”。远端监控会显示在独立的只读列表中，接收端不能检查、暂停、编辑、排序或删除远端项目；远端 Key 会以外部主密钥密封后保存在接收端 State Root。

### 主机安全

“监控 → 主机安全”集中显示 Windows 登录事件或 Linux SSH 登录记录、远程登录配置和防火墙状态。“安全更新”页签以只读方式读取 Windows Update Agent 或 Debian/Ubuntu APT 已有元数据中的待安装安全更新，不刷新软件源，也不下载或安装软件包。“安全基线”把当前运行权限、防火墙、更新元数据，以及 Linux SSH/Fail2Ban 或 Windows 防火墙 Profile 聚合成逐项证据与只读得分；得分只计算可用检测项，不是合规认证，也不会自动修改系统。管理员和维护员可保存最多 90 个基线快照并查看相对最新快照的状态漂移；历史只保存检测项 ID、状态、得分和采集时间，不保存证据正文。Windows 主机可管理 Windows Defender 防火墙规则；Linux 主机可安装 Fail2Ban 与 UFW、查看或解除 SSH 封禁，并在预览差异后同步 UFW 规则和默认策略。

所有角色都可查看检测结果；只有管理员和维护员可修改系统防护。防火墙、远程登录和封禁操作可能中断主机连接，请确认 Privileged Broker 以 LocalSystem 或 root 身份正常运行，并保留当前管理端口的允许规则和带外恢复方式；不要为此提升 Web、Runner 或 AI Host 的权限。

### MySQL 备份与恢复

管理员和维护员可在“资源 → 数据库”登记本机或远程 MySQL/MariaDB 实例、查看数据库与核心状态、执行手动或五字段 Cron 逻辑备份，并从 `.sql` 或 `.sql.gz` 恢复。连接可显式选择关闭 TLS、优先 TLS、要求 TLS 或验证证书与主机名；关闭 TLS 时凭据和数据库流量为明文。ScriptBoard 不捆绑数据库客户端；请在宿主 PATH 中安装 `mysqldump` 和 `mysql`，或在页面中配置它们的绝对路径。

每个数据库生成独立的 `.sql.gz` 和 SHA-256。恢复已有数据库或删除数据库前必须先完成安全备份并输入完整库名；恢复失败时会自动尝试回滚。默认产物位于 `state_root/database-backups/mysql`，自定义目录也会加入受保护路径。请仍将这些产物纳入独立的异机备份策略。

### AI 助手

AI 功能默认关闭。管理员可在“系统设置 → AI”安装与当前版本匹配的 Pi Runtime，再添加 OpenAI、Anthropic 或 OpenAI 兼容服务。模型 Endpoint 支持 HTTP 与 HTTPS；HTTP 会明文传输 API Key、提示词和模型响应。

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

默认只监听 `127.0.0.1:8787`，也可通过配置文件、环境变量或 `--listen` 显式监听其他地址。非回环监听会把高权限管理界面暴露给网络，生产环境强烈建议配置 TLS，或仅通过可信 VPN、零信任网络及 HTTPS 反向代理访问。

常用配置：

```yaml
state_root: C:\ProgramData\ScriptBoard\state
listen: 127.0.0.1:8787
allowed_hosts:
  - 127.0.0.1
  - localhost
canonical_external_url: http://127.0.0.1:8787
# 可选：远端安全事件接收端必须使用 HTTPS；token 只从受保护文件读取。
# security_event_endpoint: https://siem.example/api/scriptboard
# security_event_token_file: C:\ProgramData\ScriptBoard\secrets\siem-token
# security_event_allow_private: false
# 可选：由 Privileged Broker 独立投递固定模板到 HTTPS 邮件中继；Web 不读取 relay token。
# notification_email_relay_endpoint: https://mail-relay.example/v1/scriptboard
# notification_email_relay_token_file: C:\ProgramData\ScriptBoard\broker-secrets\mail-relay-token
# notification_email_recipient: admin@example.com
# notification_email_relay_allow_private: false
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

配置 `security_event_endpoint` 后，已提交的审计事件会先原子写入 State Root 内的有界 outbox，再按审计链顺序发送到 HTTPS 接收端；失败会指数退避并在重启后继续。Bearer token 只能通过绝对路径 `security_event_token_file` 提供，URL 禁止内嵌凭据且不跟随重定向。默认出站策略拒绝私网、回环和云元数据地址；确需同网段 SIEM 时必须显式开启 `security_event_allow_private`，元数据地址仍不可放行。审计事件可携带纳入 v3 哈希链的结构化资源 revision 与 SHA-256；Broker 参数、Quick Run 发布版本和一次性脚本摘要会随 CSV、取证 JSONL 与远端载荷输出。认证失败、权限拒绝与外部 Trigger 拒绝的突发，以及签名/Runner/Runtime 边界失败，会同时写入权限受限且轮转的 `logs/security-alerts.jsonl`。网站监控确认故障与恢复会生成固定 `website-monitor-result-v1` 结构化模板；故障同时进入本地告警，两个状态转换都随 Webhook 发送，模板只携带监控资源 ID 和固定摘要，不复制探测响应正文或技术错误。管理员和维护员可在“系统设置 → 通知与告警”只读查看接收端主机名、outbox 占用、本地告警状态、连续失败、下次尝试时间与模板状态；页面不会显示 URL 路径、查询参数、认证令牌或事件正文。连续失败八次后投递会熔断五分钟，开路期间新事件仍安全进入 outbox。

邮件通知由常驻 Privileged Broker 从已提交审计链按独立持久游标消费；Web 只显示中继主机和脱敏收件人，不读取邮件中继 token，也不拥有邮件出站能力。Broker 仅生成 `security-alert-v1`、`run-result-v1`、`website-monitor-result-v1`、`state-backup-result-v1` 和 `update-result-v1` 固定模板，通过共享 OutboundPolicy 投递到显式 HTTPS relay。relay token 只允许来自绝对路径普通文件，且父目录必须命名为独立 `broker-secrets`；Broker 启动时把该真实目录收紧为 root 或 SYSTEM/Administrators 私有权限，受管 Web 不获得目录 ACL。邮件 outbox 与 Webhook outbox 隔离、容量限制为 10,000 条，并使用相同的有界退避与五分钟熔断。中继负责把版本化 JSON 协议转换成实际邮件，ScriptBoard 不接受任意主题、正文或动态收件人。

网站监控默认验证 HTTPS/WSS 证书。关闭验证只会签发一小时的临时例外，页面持续显示警告，
创建或更新审计会记录到期时间；到期后自动恢复验证。连接另一台 ScriptBoard 汇聚网站状态
时必须使用 HTTPS Endpoint，HTTP、重定向、回环、私网和云元数据目标都会被拒绝。

管理员启动凭据不接受明文 `admin_password`、`SCRIPTBOARD_ADMIN_PASSWORD` 或 `--admin-password`；旧配置会以包含迁移指引的错误拒绝启动。需要启动时覆盖凭据时，只能使用绝对路径的 `admin_password_file`、`SCRIPTBOARD_ADMIN_PASSWORD_FILE` 或 `--admin-password-file`。首次启动与 `scriptboard admin reset` 仍会生成 State Root 内权限受限、修改密码后删除的一次性凭据文件。

本地账户的新口令至少包含 15 个 Unicode 字符且不超过 256 个 UTF-8 字节。策略允许空格且不强制大小写、数字或符号组合，但会拒绝用户名、内置常见口令、单字符重复以及复用当前口令；随机首次凭据和管理员重置凭据满足同一强度边界。

## 更新与备份

正式版本会定期检查 GitHub Releases，但不会自动安装。管理员可在“系统设置 → 更新”中选择 GitHub 官方源或内置的公开代理源，再下载、验证并安装新版本；成功选择的源也会用于后续自动检查。公开代理的可用性不由 ScriptBoard 保证，所有来源仍必须通过签名清单、平台、大小与 SHA-256 校验；有脚本正在运行时不会切换版本。

请定期备份：

- 需要保留的主机文件；
- `state_root`，其中包含数据库、运行日志、会话、审计和 AI 数据；
- 服务使用的 `config.yaml`（如果创建了自定义配置）。

可恢复的 Provider、MySQL 与远程网站凭据只以密文存在 State Root；解密主密钥位于 State
Root 同级的 `secrets/credential-master-<实例摘要>.key`，文件页把该目录视为受保护路径。
Linux/Unix 与 Windows 的迁移或灾难恢复都必须独立保护 credential master；Windows 日常 key 文件
还由机器级 DPAPI 保护，不能直接复制到另一主机解封。`backup export-recovery` 会把主密钥与用途隔离的
审计签名密钥放入独立 Argon2id + XChaCha20-Poly1305 加密材料，新主机只在明确的 `recover-host`
流程中将主密钥重新封装到本机 DPAPI。只复制 State Root 不包含解密材料，这是预期安全属性。
相同外部目录还保存用途隔离、经主密钥密封的
`audit-checkpoint-signing-<实例摘要>.enc` 与公开的签名 checkpoint
`audit-checkpoint-<实例摘要>.json`；备份和同路径恢复必须同时保留这三项外部材料，否则不能
证明恢复前后的审计连续性。`scriptboard doctor` 会分别检查它们是否存在。

可使用本机 CLI 生成认证加密的私有状态备份。口令必须放在绝对路径普通文件中且至少 16
字节；输出路径必须位于 State Root 外，已有文件不会被覆盖。`inspect` 会完整认证包、清单和
逐文件摘要。恢复前停止 ScriptBoard 的全部服务，并重复输入 `inspect` 返回的 Backup ID：

```text
scriptboard backup create --output ABSOLUTE_BACKUP_PATH --passphrase-file ABSOLUTE_PASSPHRASE_FILE --config CONFIG_PATH
scriptboard backup inspect --archive ABSOLUTE_BACKUP_PATH --passphrase-file ABSOLUTE_PASSPHRASE_FILE
scriptboard backup restore --archive ABSOLUTE_BACKUP_PATH --passphrase-file ABSOLUTE_PASSPHRASE_FILE --confirm-backup-id BACKUP_ID --config CONFIG_PATH
scriptboard backup export-recovery --output ABSOLUTE_RECOVERY_PATH --passphrase-file ABSOLUTE_RECOVERY_PASSPHRASE_FILE --config CONFIG_PATH
scriptboard backup recover-host --archive ABSOLUTE_BACKUP_PATH --passphrase-file ABSOLUTE_PASSPHRASE_FILE --recovery-material ABSOLUTE_RECOVERY_PATH --recovery-passphrase-file ABSOLUTE_RECOVERY_PASSPHRASE_FILE --confirm-backup-id BACKUP_ID --config CONFIG_PATH
```

受管安装还可在“系统设置 → 私有状态备份”中创建、验证和暂存恢复。页面只接受服务器本地绝对路径；所有写操作均要求近期身份验证（已配置双重认证时验证第二因素，否则验证当前密码），并由同版本 Privileged Broker 执行。暂存恢复会验证加密包、SQLite、schema 和签名审计 checkpoint，撤销备份内 Web Session，并绑定暂存后文件摘要；它不会替换在线数据库。最终提交仍须停止 Web、Broker、Runner 与 AI Host 后使用离线恢复流程。

状态包只包含一致性 SQLite snapshot、Broker 密文和固定私有证据，不包含外部主密钥、审计签名
私钥、配置、TLS 材料、诊断日志、上传 inbox 或 MySQL 备份。常规 `restore` 要求同一 State Root
路径的现有外部密钥和当前签名 checkpoint 仍可验证；成功后保留恢复前私有状态和 checkpoint，
并在审计链写入绑定前后锚点的恢复事件。整机恢复要求相同 canonical State Root 路径上的空、未初始化
目录，且拒绝覆盖任何已有外部信任材料；它使用单独口令认证 recovery 材料、重新封装主密钥、验证备份
内 checkpoint 与恢复出的审计链，撤销全部 Web Session，再记录 `state_backup.recover_host` 并推进新主机 checkpoint。

从旧版本升级前请先备份。当前版本使用数据库 schema 49，可自动迁移 schema 20–48；更早版本的数据库和旧式配置不会自动迁移。schema 45 增加 Registry 连接跨进程操作日志，schema 46 增加崩溃安全的完成阶段，schema 47 为外部接口分组拆分显示名称与 URL 调用名，schema 48 将单个 Kubernetes 连接及其历史迁移为可独立监控的多连接结构，schema 49 为变量增加值类型并将旧变量迁移为 `text`。schema 40 会记录会话认证保证级别和最近再次认证时间；新登录视为最近认证，高风险操作在 10 分钟后要求于受保护的浏览器会话中重新输入当前密码。schema 41 为审计事件增加服务端 Request ID 与认证保证字段，并把新字段纳入兼容旧事件的 v2 哈希。schema 42 为 Administrator/Maintainer 记录 MFA 注册截止时间；到期未注册 TOTP 或 passkey 的账户只能进入 MFA 设置与登出路径。旧实例获得七天注册窗口，首次管理员与新 Maintainer 获得 24 小时窗口。旧快捷执行项会以未发布状态迁移，在管理员重新保存前不能启动或绑定外部入口。旧版本中共享一个 Key 的多个外部功能会拆成独立 Key：保留最早功能的原 Key，其余功能迁移到默认停用、必须轮换并显式启用 Key 与功能后才能使用的新 Key。为保持兼容，迁移后的外部功能不会自动要求 HMAC；管理员可在编辑页启用，新建功能默认启用。外部接口页面提供持久化全局紧急开关；暂停后所有有效外部调用以低信息 503 响应拒绝且不会执行操作。审计记录形成带保留锚点与链尾的 SHA-256 哈希链，并由 State Root 外 Ed25519 checkpoint 锚定；服务启动时会 fail closed 验证，正常关闭及每五分钟刷新。面板不可用时可运行 `scriptboard audit verify --config CONFIG_PATH`，离线检查中间记录修改、删除、截断，以及链尾和同库状态一起回退；取证导出也执行相同验证。此 checkpoint 仍是主机本地边界，不替代远端不可变日志。

面板不可用或疑似遭入侵时，可在主机本地使用带外应急命令。写操作要求重复输入固定确认值或完整 Key ID，并作为 `local-administrator` 原子写入审计链；取证导出先验证审计链，只创建新文件且不会覆盖已有证据：

```text
scriptboard emergency pause-external --confirm PAUSE-EXTERNAL --config CONFIG_PATH
scriptboard emergency revoke-key --key-id KEY_ID --confirm-key-id KEY_ID --config CONFIG_PATH
scriptboard emergency export-evidence --output ABSOLUTE_JSONL_PATH --config CONFIG_PATH
```

在隔离或断网主机上，可同时提供正式 Release 的单文件安装器、`release-manifest.json` 与 `release-manifest.json.sig`，离线验证内置签名信任根、平台、文件名、大小、SHA-256、内嵌载荷边界和 `RELEASE.json`，此命令不会安装或修改当前版本：

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

新的一次性密码会写入 `state_root/secrets/initial-admin-password`；本机重置同时清除管理员的
TOTP 配置，以便在认证器和恢复码都丢失时带外恢复，并撤销全部会话。

更多命令可运行 `scriptboard help` 查看。开发、测试和发布说明请参阅 [项目文档](./docs/)。

# ScriptBoard

设置按“我的账户、用户、实例设置、接入与通知、系统维护”组织；编辑在抽屉中完成，显示偏好仅保存在当前浏览器。

简体中文 | [English](./README_EN.md)

**在浏览器中管理 Windows 或 Linux 主机上的文件、脚本和运行状态。**

ScriptBoard 适合个人服务器、小团队工具机和内部运维主机。安装后即可使用主机上已有的脚本，无需迁移文件或搭建额外的脚本仓库。

[下载最新版本](https://github.com/backinfile/ScriptBoard/releases/latest) · [发布说明](./docs/RELEASE_NOTES.md) · [快速安装](#快速安装) · [开始使用](#开始使用)

> [!WARNING]
> ScriptBoard 可以运行主机上的脚本。请只向可信用户开放，并避免将管理界面直接暴露到公网。

![ScriptBoard 机器状态](./docs/images/readme/overview-zh.png)

## 主要功能

- 浏览、搜索、预览、编辑、上传和下载文件；通过“跳转”输入路径或搜索当前目录、子目录及全部主机位置，自动进入文件所在分页并高亮定位
- 收藏和分组管理常用文档，通过独立快捷访问页整理常用目录
- 使用灵感空间整理笔记、链接、待办、计时器和自由画板，所有登录用户共同查看和编辑
- 运行 PowerShell、Python、Shell、Batch 和 CMD 脚本
- 保存常用任务，并按计划自动运行
- 查看 CPU、内存、存储、应用和运行历史
- 查看 Docker、Kubernetes（含 NodePort、LoadBalancer 与 Ingress 外部入口）和网站状态；Kubernetes 外部访问、工作负载与节点默认折叠，手动刷新、自动刷新开关与全部展开/收起按钮集中在同一行，筛选、搜索与排序仅更新工作负载区域
- 在统一数据库工作台中备份和恢复 MySQL/MariaDB，并查看 Redis 数据
- 在“资源 → 镜像仓库”切换 Registry 连接，查看版本，并按 namespace、名称前缀或 tag 规则预览和批量删除镜像
- 管理用户、角色、审计记录和外部调用

<p align="center">
  <img src="./docs/images/readme/files-zh.png" alt="ScriptBoard 文件页面" width="49%">
  <img src="./docs/images/readme/redis-zh.png" alt="ScriptBoard 数据库工作台（Redis）" width="49%">
</p>

界面支持简体中文和美式英语，并适配桌面与移动浏览器。

界面预览：[Kubernetes 折叠区域](./docs/previews/kubernetes-collapsible-sections.html)（下载后用浏览器打开）。

## 支持环境

| 系统 | 架构 | 安装包 |
| --- | --- | --- |
| Windows 10/11、Windows Server 2019+ | amd64、arm64 | `*-setup.exe` |
| 使用 systemd 的 Linux | amd64、arm64 | `.run` |

请先安装脚本所需的解释器，例如 PowerShell、Python 或 Bash。ScriptBoard 暂不提供 Docker 部署包。

## 快速安装

从 [GitHub Releases](https://github.com/backinfile/ScriptBoard/releases/latest) 下载与系统和架构匹配的安装包。

### Windows

在管理员 PowerShell 中运行：

```powershell
.\scriptboard-vX.Y.Z-windows-amd64-setup.exe
```

### Linux

```bash
chmod +x ./scriptboard-vX.Y.Z-linux-amd64.run
sudo ./scriptboard-vX.Y.Z-linux-amd64.run
```

安装完成后，ScriptBoard 会作为系统服务运行，默认访问地址为 <http://127.0.0.1:8787>。

## 开始使用

1. 打开 <http://127.0.0.1:8787>，首次访问会进入管理员设置页。
2. 使用启动窗口的初始化链接，或读取令牌文件后粘贴到页面：
   - Windows：`C:\ProgramData\ScriptBoard\state\secrets\initialization-token`
   - Linux：`/var/lib/scriptboard/state/secrets/initialization-token`
3. 设置用户名和密码，保存后自动进入应用。令牌有效期为 24 小时；未完成设置时重启服务会生成新令牌。
4. 前往“资源 → 文件”，选择已有脚本或上传文件，然后开始运行。

已有账号直接登录。自动部署可配置 `--admin-password-file` 跳过首次设置；每次启动时会将指定凭据写入数据库一次，运行期间以账户设置为准。忘记密码时使用下方的本机恢复命令。

编辑 Redis 连接时，密码留空会保留原密码；无密码实例修改地址、端口或 TLS 设置时，请明确勾选“使用空密码”。

镜像仓库支持 Docker Registry V2 目录接口、HTTP、HTTPS 和显式跳过证书验证（存在中间人攻击风险）。删除前会在抽屉中列出同一 digest 关联的全部 tag；规则按镜像创建时间手动执行。Registry 需启用删除功能，释放磁盘空间还需服务端垃圾回收。目录或 tag 枚举超出限制时会拒绝生成清理计划，请缩小范围。

## 脚本内存限制

在“设置 → 实例设置 → 内存限制”抽屉中调整总额度、默认任务额度及平台专属额度。管理员和维护者可修改；额度保存在实例状态库中。默认任务额度保存后立即用于新任务，运行中任务不变；Runner 总额度和平台专属额度重启后生效。

任务或快捷执行的“任务内存上限”留空继承最新默认值，也可填写 `512MiB`、`8GiB` 或 `unlimited`。额度包含子进程；运行详情保留当次任务额度。

Windows 默认总额度 4 GiB、每次运行 4 GiB、单进程 2 GiB；单进程额度在内存限制抽屉中调整。Linux 默认 Runner 服务总额度 2 GiB，每次运行不另设上限；swap 额度默认为 `0`，也可设置容量或 `unlimited`。Linux 受管执行需要 cgroup v2；按任务设置内存不会自动改变 swap 策略。

全局配置修改后，以管理员身份运行 `scriptboard service restart` 生效；Windows 便携运行重新启动进程。Linux 全局额度由受管服务实施，便携运行的任务限额需要置于委派了 memory 控制器的 systemd 服务中。Linux 请使用此命令同步服务额度，直接 `systemctl restart` 不会重写额度。Runner 启动日志显示已应用的配置。

`unlimited` 只取消对应层的限制：任务仍受全局额度和 Windows 单进程额度约束。取消所有额度可能耗尽宿主内存；系统自身或上级服务的限制仍然有效。

## 远程访问

默认情况下，ScriptBoard 仅允许本机访问。如需远程使用，建议通过可信 VPN、零信任网络或 HTTPS 反向代理接入，并限制可访问的用户和网络。

## MCP Agent 接入

ScriptBoard 默认在主服务的 `POST /mcp` 提供 Streamable HTTP MCP，并使用浏览器 OAuth + PKCE 登录，不需要也不接受静态 Token。将支持远程 MCP OAuth 的 Agent 指向：

在“设置 → 接入与通知 → MCP 客户端与授权”注册客户端。浏览器授权后，可通过 MCP 查询状态及运行已发布的快捷执行；内容写入由快捷执行脚本完成。

```text
http://127.0.0.1:8787/mcp
```

首次连接会收到 401，Agent 随后发现授权元数据并打开浏览器请求授权。观察员只能读取状态、Quick Run 和 Run 日志；执行员及以上角色可在批准 `scriptboard.execute` 后启动已发布的 Quick Run。MCP 不开放任意文件、源码或系统配置。

可在 YAML 中关闭整组 MCP/OAuth 路由：

```yaml
mcp_enabled: false
```

MCP 复用 `listen`、TLS、`allowed_hosts`、`trusted_proxies` 和 `canonical_external_url`。修改 `listen` 使服务监听非回环地址时，MCP 会随主服务一起开放；非回环明文 HTTP 会暴露登录和操作流量，应优先使用 TLS、可信 HTTPS 反向代理或受控专用网络。

## 常用命令

```text
scriptboard service status
scriptboard doctor --config CONFIG_PATH
scriptboard help
```

如果忘记管理员密码，请先停止服务，再运行：

```text
scriptboard admin reset --config CONFIG_PATH
```

## 更新与备份

管理员可以在“系统设置 → 更新”中安装新版本。更新前建议备份需要保留的主机文件、ScriptBoard 状态目录和自定义配置。

## 更多信息

- [项目文档](./docs/)
- [发布说明](./docs/RELEASE_NOTES.md)
- [安全问题报告](./SECURITY.md)

## 灵感空间

进入“资源 → 灵感空间”，点击顶部“新建空间”并填写名称。内容自动保存；新建空间保存失败时可重试。单击页签即可切换空间，右侧“空间操作”菜单可重命名或删除；误删可在底部工具栏撤销。底部悬浮工具栏切换“查看 / 编辑”，编辑时可添加笔记、链接、待办、计时器和画板，并修改模块标题、内容或排列顺序。画板支持 10 种颜色、局部橡皮和可编辑文字；文字可换行、调整字号与颜色、拖动和删除，操作可撤销。查看时仍可跳转链接、勾选待办、使用计时器和平移画布。

画板支持画笔、直线、箭头、矩形和椭圆，拖拽绘制并可撤销。提供小、中、大、超大四档视窗；尺寸切换不缩放已有笔迹。选择“移动画布”后拖动，或使用方向键平移；鼠标中键也可临时平移。“回到原点”重置视图。超大视窗适配右侧可用区域，面板切换栏始终固定。

保存后通过 SSE 通知其他客户端获取最新状态；断线重连会重新同步。查看模式自动更新，编辑模式自动更新未编辑的模块，并保护正在输入的内容。内容自动保存在服务端，所有登录用户均可查看和编辑，未登录不可访问。不同模块分别进行版本比较和保存；同一模块冲突时保留本地内容，可选择共享版本或明确保存本地版本。面板结构冲突会提示并保留本地备份。新建共享空间默认最多 32 个面板、总计 300 个模块，单面板最多 100 个模块；单模块最多 200 条待办或链接，总笔迹最多 60,000 个点，总文档大小最多 4 MiB。升级时将已有个人面板完整迁入共享空间；合并数据超过默认容量时保留足够容量供继续编辑。无 JavaScript 时仍可使用基础表单编辑笔记、链接与待办；自由绘画、画布平移和实时计时需要 JavaScript。

### 嵌入另一个 ScriptBoard

在被嵌入实例的“设置 → 页面嵌入”中选择“指定地址”或“全部地址”，保存后立即生效并保留。指定地址每行填写一个完整来源（协议、主机及可选端口，不含路径或末尾斜杠）。选择“全部地址”允许任何网站嵌入本实例，可能被用于诱导点击或操作。

默认禁止嵌入。首次在设置页保存前，沿用 `config.yaml` 的 `frame_ancestors`、环境变量 `SCRIPTBOARD_FRAME_ANCESTORS`（逗号分隔）或 `--frame-ancestor` 启动参数；支持 `"*"` 允许所有来源。设置页保存后优先于启动配置。

在父实例“自定义页签”中填写目标实例 URL，选择“保留目标登录态”并启用，使用目标实例自己的账号登录。两个实例应使用不同主机名，避免同名 Cookie 冲突。同站 HTTP 可用；跨站登录需要目标 HTTPS 且浏览器允许第三方 Cookie，HTTPS 父页不能嵌入 HTTP 目标。浏览器拦截时使用“在新窗口打开”。嵌入后可切换并保留界面语言。这不会共享两个实例的账号或 Key 登录。

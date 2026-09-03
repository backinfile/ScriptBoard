# ScriptBoard

简体中文 | [English](./README_EN.md)

**在浏览器中管理 Windows 或 Linux 主机上的文件、脚本和运行状态。**

ScriptBoard 适合个人服务器、小团队工具机和内部运维主机。安装后即可使用主机上已有的脚本，无需迁移文件或搭建额外的脚本仓库。

[下载最新版本](https://github.com/backinfile/ScriptBoard/releases/latest) · [快速安装](#快速安装) · [开始使用](#开始使用)

> [!WARNING]
> ScriptBoard 可以运行主机上的脚本。请只向可信用户开放，并避免将管理界面直接暴露到公网。

![ScriptBoard 机器状态](./docs/images/readme/overview-zh.png)

## 主要功能

- 浏览、搜索、预览、编辑、上传和下载文件
- 运行 PowerShell、Python、Shell、Batch 和 CMD 脚本
- 保存常用任务，并按计划自动运行
- 查看 CPU、内存、存储、应用和运行历史
- 查看 Docker、Kubernetes（含 NodePort、LoadBalancer 与 Ingress 外部入口）和网站状态
- 在统一数据库工作台中备份和恢复 MySQL/MariaDB，并查看 Redis 数据
- 管理用户、角色、审计记录和外部调用

<p align="center">
  <img src="./docs/images/readme/files-zh.png" alt="ScriptBoard 文件页面" width="49%">
  <img src="./docs/images/readme/redis-zh.png" alt="ScriptBoard 数据库工作台（Redis）" width="49%">
</p>

界面支持简体中文和美式英语，并适配桌面与移动浏览器。

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

1. 打开 <http://127.0.0.1:8787>。
2. 使用用户名 `admin` 登录。
3. 从以下位置读取初始密码：
   - Windows：`C:\ProgramData\ScriptBoard\state\secrets\initial-admin-password`
   - Linux：`/var/lib/scriptboard/state/secrets/initial-admin-password`
4. 登录后立即修改密码。
5. 前往“资源 → 文件”，选择已有脚本或上传文件，然后开始运行。

## 远程访问

默认情况下，ScriptBoard 仅允许本机访问。如需远程使用，建议通过可信 VPN、零信任网络或 HTTPS 反向代理接入，并限制可访问的用户和网络。

## MCP Agent 接入

ScriptBoard 默认在主服务的 `POST /mcp` 提供 Streamable HTTP MCP，并使用浏览器 OAuth + PKCE 登录，不需要也不接受静态 Token。将支持远程 MCP OAuth 的 Agent 指向：

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

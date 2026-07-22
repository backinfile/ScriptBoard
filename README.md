# ScriptBoard

[简体中文](./README.md) | [English](./README_EN.md)

ScriptBoard 是一个面向单机、单管理员的可信脚本管理器。它通过浏览器管理指定目录中的文件，并提供脚本执行、实时日志、快捷执行、定时计划、审计记录和可选的本地 Git 版本保护。

> [!WARNING]
> ScriptBoard 不是沙箱。脚本会继承 ScriptBoard 服务进程的操作系统权限和环境变量。请只运行你完全信任的脚本。

## 主要功能

- 在浏览器中上传、下载、移动、重命名、预览和编辑受管文件
- 执行 PowerShell、Python、Shell、Batch 等脚本并实时查看输出
- 保存不可由界面删除的运行历史和审计记录
- 将常用任务保存为 Quick Run，或使用五字段 Cron 表达式定时执行
- 两阶段停止、运行超时以及 Windows/Linux 子进程树清理
- 可选的本地 Git 版本保护、文件历史和单文件恢复
- Windows 服务与系统托盘控制器；Linux systemd 服务
- 响应式 Web 界面，可从桌面或手机浏览器使用

## 支持的平台

| 系统 | 架构 | 发布包 |
| --- | --- | --- |
| Windows 10/11、Windows Server 2019+ | amd64、arm64 | ZIP，包含服务程序和托盘程序 |
| 使用 systemd 的 Linux | amd64、arm64 | tar.gz，包含服务程序 |

从 [GitHub Releases](https://github.com/backinfile/ScriptBoard/releases/latest) 下载适合当前系统的压缩包，并使用 `SHA256SUMS` 验证文件完整性。

## 快速开始

### Windows

解压发布包后，在 PowerShell 中运行：

```powershell
.\scriptboard.exe serve
```

打开 <http://127.0.0.1:8787>。默认用户名是 `admin`，初始一次性密码位于：

```text
C:\ProgramData\ScriptBoard\state\secrets\initial-admin-password
```

首次登录后必须设置新密码。直接运行模式下不要关闭 PowerShell 窗口；需要开机自动运行时，请按下文安装 Windows 服务。

### Linux

解压发布包并安装二进制：

```bash
tar -xzf scriptboard-v1.0.0-linux-amd64.tar.gz
chmod +x scriptboard
sudo install -m 0755 scriptboard /usr/local/bin/scriptboard
sudo scriptboard serve
```

打开 <http://127.0.0.1:8787>。默认用户名是 `admin`，初始一次性密码位于：

```text
/var/lib/scriptboard/state/secrets/initial-admin-password
```

## 安装为系统服务

服务安装会记录当前 `scriptboard` 可执行文件和配置文件的位置，因此请先把程序放到长期使用的位置，并创建配置文件。

最小配置示例：

```yaml
managed_root: C:\ProgramData\ScriptBoard\managed
state_root: C:\ProgramData\ScriptBoard\state
listen: 127.0.0.1:8787
run_timeout_grace_seconds: 30
```

Windows 将其保存为 `C:\ProgramData\ScriptBoard\config.yaml`，然后在管理员 PowerShell 中运行：

```powershell
.\scriptboard.exe config validate --config C:\ProgramData\ScriptBoard\config.yaml
.\scriptboard.exe service install --config C:\ProgramData\ScriptBoard\config.yaml
.\scriptboard.exe service start
.\scriptboard.exe service status
```

安装服务后可运行托盘控制器：

```powershell
.\scriptboard-tray.exe --config C:\ProgramData\ScriptBoard\config.yaml
```

退出托盘不会停止 ScriptBoard 服务。

Linux 可将配置保存为 `/etc/scriptboard/config.yaml`：

```yaml
managed_root: /var/lib/scriptboard/managed
state_root: /var/lib/scriptboard/state
listen: 127.0.0.1:8787
run_timeout_grace_seconds: 30
```

然后安装并启动 systemd 服务：

```bash
sudo scriptboard config validate --config /etc/scriptboard/config.yaml
sudo scriptboard service install --config /etc/scriptboard/config.yaml
sudo scriptboard service start
sudo scriptboard service status
```

## 配置

配置优先级从低到高为：内置默认值、YAML 配置、`SCRIPTBOARD_*` 环境变量、命令行参数。

完整示例：

```yaml
managed_root: C:\ProgramData\ScriptBoard\managed
state_root: C:\ProgramData\ScriptBoard\state
listen: 127.0.0.1:8787
git_executable: C:\Program Files\Git\cmd\git.exe
run_timeout_grace_seconds: 30
trusted_proxies:
  - 127.0.0.1/32
admin_password_file: C:\ProgramData\ScriptBoard\secrets\admin-password
executor_chains:
  .py:
    - C:\Python313\python.exe
```

常用命令：

```text
scriptboard serve
scriptboard service install|uninstall|start|stop|restart|status
scriptboard admin reset
scriptboard config validate
scriptboard doctor
scriptboard version
```

如果忘记管理员密码，可以在停止服务后使用相同配置运行：

```powershell
.\scriptboard.exe admin reset --config C:\ProgramData\ScriptBoard\config.yaml
```

新的初始密码将写入 State Root 的 `secrets/initial-admin-password`。

## 网络与安全

- 默认只监听 `127.0.0.1:8787`，不会直接暴露到局域网或互联网。
- 非回环地址必须配置 `tls_cert` 和 `tls_key`，否则程序会拒绝启动。
- 通过本机反向代理提供访问时，可使用 `trusted_proxies`；只有可信代理传入的转发头会被接受。
- ScriptBoard 只支持一个管理员，不提供多用户、RBAC、脚本隔离或公共 API。
- Windows 服务默认以 LocalSystem 运行，Linux systemd 服务默认以 root 运行；可以通过操作系统服务配置降低权限。

## 升级

停止服务，用新版本替换原有二进制，然后重新启动：

```text
scriptboard service stop
scriptboard service start
```

ScriptBoard 启动时会自动执行向前兼容的数据库迁移。不要使用旧版本打开已经由新版本升级过的 State Root。

## 从源码构建

需要 Go 1.26：

```powershell
go test ./...
go build ./cmd/scriptboard
./scripts/build-release.ps1 -Version development
```

发布脚本会在 `dist/` 中生成 Windows/Linux 的 amd64/arm64 便携归档和 `SHA256SUMS`。

## 项目文档

- [产品需求](./docs/PRD.md)
- [验收标准](./docs/ACCEPTANCE.md)
- [数据模型与状态机](./docs/DATA-MODEL.md)
- [领域词汇](./CONTEXT.md)
- [架构决策](./docs/adr/README.md)

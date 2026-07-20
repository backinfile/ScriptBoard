# ScriptBoard

ScriptBoard 是面向单机、单管理员的受管目录文件服务器，并为可信脚本提供本地执行、实时日志、不可删除的 Run 历史、Quick Run、内置 Schedule、审计和可选的本地 Git Version Protection。

## 快速开始

需要 Go 1.26。开发环境可直接运行：

```powershell
go run ./cmd/scriptboard serve --managed-root D:\ScriptBoard\managed --state-root D:\ScriptBoard\state
```

默认监听 `127.0.0.1:8787`。首次启动会把一次性 admin 密码写入 State Root 的 `secrets/initial-admin-password`；首次登录必须修改密码。

常用命令：

```text
scriptboard serve
scriptboard config validate
scriptboard doctor
scriptboard admin reset
scriptboard service install|uninstall|start|stop|restart|status
scriptboard version
```

配置优先级为默认值、YAML、`SCRIPTBOARD_*` 环境变量、命令行参数。非回环监听必须配置内置 TLS；由回环地址上的反向代理终止 TLS 时可显式声明可信代理。只有可信代理的 `X-Forwarded-For` 和 `X-Forwarded-Proto` 会被采信。YAML 示例：

```yaml
managed_root: D:\ScriptBoard\managed
state_root: D:\ScriptBoard\state
listen: 127.0.0.1:8787
run_timeout_grace_seconds: 30
trusted_proxies:
  - 127.0.0.1/32
admin_password_file: D:\ScriptBoard\secrets\admin-password
executor_chains:
  .py:
    - C:\Python313\python.exe
```

Windows 发布归档同时包含 `scriptboard.exe` 和独立的 `scriptboard-tray.exe`；Linux 归档包含服务二进制。托盘退出不会停止服务。便携归档可通过以下命令生成：

```powershell
./scripts/build-release.ps1 -Version 1.0.0
```

输出包括 Windows/Linux 的 amd64/arm64 归档及 `SHA256SUMS`。

## 设计与验收

- [MVP 产品需求](./docs/PRD.md)
- [数据模型与状态机](./docs/DATA-MODEL.md)
- [验收标准](./docs/ACCEPTANCE.md)
- [领域词汇](./CONTEXT.md)
- [架构决策](./docs/adr/README.md)

ScriptBoard 只执行管理员信任的脚本，不提供沙箱、RBAC、公共 API、远程 Git、系统 crontab 集成或用户可见的数据库备份命令。

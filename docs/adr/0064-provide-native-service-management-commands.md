# 提供原生服务管理 CLI

> **2026-08-12 扩展**：`service verify` 会把安装元数据、三个组件的 Installed Release 与平台服务定义作为一个整体复核；Windows 还检查服务身份、启动类型、依赖、恢复动作和 Web 对 demand-start 服务的精确 ACL。`service install` 在注册后自动执行相同验证，`--start` 进一步启动并输出产品级状态，供发布包中的单一安装入口使用。

主二进制提供 `serve`、`service install|uninstall|start|stop|restart|status`、`admin reset`、`config validate`、`doctor` 和 `version` 命令。Windows 直接集成服务控制管理器而不依赖 NSSM，Linux 安装并管理 systemd unit；需要改变服务定义的操作要求管理员或 root 权限，托盘复用相同控制契约。`service uninstall` 只删除服务定义，绝不删除配置、主机文件或内部状态，MVP 不提供一键删除全部数据。

# 提供原生服务管理 CLI

主二进制提供 `serve`、`service install|uninstall|start|stop|restart|status`、`admin reset`、`config validate`、`doctor` 和 `version` 命令。Windows 直接集成服务控制管理器而不依赖 NSSM，Linux 安装并管理 systemd unit；需要改变服务定义的操作要求管理员或 root 权限，托盘复用相同控制契约。`service uninstall` 只删除服务定义，绝不删除配置、受管文件或内部状态，MVP 不提供一键删除全部数据。

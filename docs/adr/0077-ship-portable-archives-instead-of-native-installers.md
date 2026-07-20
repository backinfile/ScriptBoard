# MVP 发布便携归档而非平台安装包

Windows 按架构发布包含 `scriptboard.exe`、`scriptboard-tray.exe`、README 和许可证的 ZIP，Linux 发布包含单个服务二进制及文档的 tar.gz，并为所有产物提供 SHA-256 校验文件。MVP 不制作 MSI、DEB、RPM 或安装向导；解压后由 `scriptboard service install` 创建目录、注册服务，并在 Windows 默认配置当前用户的托盘登录自启动，允许参数关闭。

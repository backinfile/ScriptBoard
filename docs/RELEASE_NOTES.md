# ScriptBoard v2.14.3

## 修复

- 修复 Windows 服务被识别为便携安装、无法使用内置更新的问题。
- Windows 内置更新会保留 SQLite WAL 中的已提交数据，并在目标版本通过验证后继续推进签名审计检查点。

## 升级

如果 Windows 服务的「应用更新」显示为便携安装，请先备份状态目录和配置，再以管理员身份运行已安装的 `scriptboard.exe service uninstall` 移除旧服务注册，然后用此版本的 Windows 安装器和原有配置重新安装。自定义配置可通过 `--config CONFIG_PATH` 指定。

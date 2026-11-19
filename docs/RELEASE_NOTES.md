# ScriptBoard v2.14.3

## 修复

- 修复 Windows 服务被识别为便携安装、无法使用内置更新的问题。
- Windows 内置更新会保留 SQLite WAL 中的已提交数据，并在目标版本通过验证后继续推进签名审计检查点。

## 升级

如果 Windows 服务的「应用更新」显示为便携安装，请使用此版本的 Windows 安装器升级。升级前备份 ScriptBoard 状态目录与自定义配置。

# 使用平台数据目录保存受管文件与私有状态

Linux 默认使用 `/var/lib/scriptboard/managed` 和 `/var/lib/scriptboard/state`，Windows 默认使用 `%ProgramData%\ScriptBoard\managed` 和 `%ProgramData%\ScriptBoard\state`；手动及服务启动采用相同默认值，两个路径均可覆盖。首次启动自动创建缺失目录，但路径不是目录、真实路径相同或互相包含、权限不足时拒绝启动，以保持受管文件与私有状态的隔离。

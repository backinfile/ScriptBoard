# 使用平台特定的默认执行器链

Linux 默认将 `.sh` 映射为 `bash`、`sh`，`.py` 映射为 `python3`、`python`，`.ps1` 映射为 `pwsh`；Windows 默认将 `.ps1` 映射为 `pwsh.exe`、`powershell.exe`，`.py` 映射为 `py.exe -3`、`python.exe`，`.bat` 与 `.cmd` 映射为 `cmd.exe`，`.sh` 映射为 `bash.exe`。每个候选可包含固定前缀参数，可按 PATH 查找或在 YAML 中配置绝对路径；Windows Terminal 和 WSL 不作为默认执行器。回退仍只发生在进程成功启动之前。

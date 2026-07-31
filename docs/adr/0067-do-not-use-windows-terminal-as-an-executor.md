# Windows Terminal 只作为人工入口而非执行器

Windows Terminal 是承载 PowerShell、CMD、WSL 等 Shell 的交互终端宿主，不加入脚本执行器链；ScriptBoard 直接调用 `pwsh.exe`、`powershell.exe`、`cmd.exe`、Python 或 Bash，以保留 stdout/stderr 捕获、PID 监督、超时和进程树终止。托盘程序不从主机文件页推导或打开任意终端工作目录，交互终端始终与服务执行分离。

# Windows Terminal 只作为人工入口而非执行器

Windows Terminal 是承载 PowerShell、CMD、WSL 等 Shell 的交互终端宿主，不加入脚本执行器链；ScriptBoard 直接调用 `pwsh.exe`、`powershell.exe`、`cmd.exe`、Python 或 Bash，以保留 stdout/stderr 捕获、PID 监督、超时和进程树终止。托盘在检测到 `wt.exe` 时可显示“在受管目录打开 Terminal”，该操作运行在登录用户会话中并与服务执行完全分离。

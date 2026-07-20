# 独立保存并滚动服务诊断日志

服务诊断日志与 Run 日志及审计记录分离，写入内部状态目录 `logs/scriptboard.log`，单文件最大 10 MiB 并保留最近 5 个滚动文件。Linux 同时写 stderr 供 systemd/journald 收集，Windows 托盘可打开日志目录并选中当前文件。默认级别为 info，可在启动配置改为 debug；任何级别都禁止输出密码、Cookie、CSRF Token、变量值、完整环境或请求正文，文件仅允许运行身份和系统管理员读取。

# 使用 Windows Service Hardening 与 systemd seccomp 隔离 Runtime Host

Windows 受管 AI Host 与 Runner 都使用 `LocalService`、各自的 restricted service SID 和私有目录 ACL。安装器进一步通过 Windows Firewall `INetFwServiceRestriction` 启用 Windows Service Hardening：系统为服务建立默认阻断入站、出站流量的 WFP 过滤器；AI 只在专用 WSH 规则集合中获得到 `127.0.0.1` 与 `::1` 的 TCP 出站例外，用于访问 Web 进程持有的 Provider/Tool Broker 代理；Runner 没有网络例外。安装或版本切换必须先为新 executable 建立限制，服务定义切换成功后才撤销旧 executable 的限制；任一步失败都不得以未受限服务继续安装。卸载只移除本产品拥有的规则和两个服务限制。

Linux 的 AI Host 与 Runner 在独立 UID、空 capability、`NoNewPrivileges` 和 systemd 地址限制之外，启用 `SystemCallArchitectures=native`、`SystemCallFilter=@system-service` 与 `SystemCallErrorNumber=EPERM`，由 systemd 生成 seccomp allowlist；同时隔离设备、时钟、主机名、内核日志、namespace 与实时调度。AI 服务维持仅环回地址例外并限制为 1 GiB/64 tasks，Runner 不允许 IP 地址族并限制为 2 GiB/64 tasks。两个服务同时设置 `MemorySwapMax=0`；否则进程可在到达 `MemoryMax` 后继续把工作集换出，绕过“有限内存占用”的安全目标并耗尽宿主 swap。

所有受管前台入口都订阅平台关闭信号；Unix 同时处理 `SIGINT` 和 systemd 默认发送的 `SIGTERM`，确保正常停止会执行 IPC 清理。Broker 启动时只接管由 Broker 服务身份或唯一授权 Web UID 拥有的遗留 Unix Socket，并要求 socket 父目录仍由 Broker 身份拥有且不可被组或其他用户写入；其他所有权继续 fail closed。这样既能从 Web 已连接后被强制终止留下的 socket 恢复，也不会把任意第三方 endpoint 当作可信残留删除。

Ubuntu 26.04 的真实 systemd 安装已经验证完整 stop/start、四个主进程逐一 `SIGKILL` 后自动恢复，以及 AI/Runner 不能读取应用数据库和实例主密钥。使用相同 systemd 策略的主动探针还验证了 AI 仅能连接环回、Runner 不能创建 TCP socket、超额 task 被拒绝，以及设置 `MemorySwapMax=0` 后超额工作集被 cgroup 终止。发布平台仍须实际验证所支持 Windows 版本上的服务生命周期、网络阻断与资源耗尽；不能仅以 unit 文本或 mock 测试代替这些门禁。

# 使用 Windows Service Hardening 与 systemd seccomp 隔离 Runtime Host

Windows 受管 AI Host 与 Runner 都使用 `LocalService`、各自的 restricted service SID 和私有目录 ACL。安装器进一步通过 Windows Firewall `INetFwServiceRestriction` 启用 Windows Service Hardening：系统为服务建立默认阻断入站、出站流量的 WFP 过滤器；AI 只在专用 WSH 规则集合中获得到 `127.0.0.1` 与 `::1` 的 TCP 出站例外，用于访问 Web 进程持有的 Provider/Tool Broker 代理；Runner 没有网络例外。安装或版本切换必须先为新 executable 建立限制，服务定义切换成功后才撤销旧 executable 的限制；任一步失败都不得以未受限服务继续安装。卸载只移除本产品拥有的规则和两个服务限制。

Linux 的 AI Host 与 Runner 在独立 UID、空 capability、`NoNewPrivileges` 和 systemd 地址限制之外，启用 `SystemCallArchitectures=native`、`SystemCallFilter=@system-service` 与 `SystemCallErrorNumber=EPERM`，由 systemd 生成 seccomp allowlist；同时隔离设备、时钟、主机名、内核日志、namespace 与实时调度。AI 服务维持仅环回地址例外并限制为 1 GiB/64 tasks，Runner 不允许 IP 地址族并限制为 2 GiB/64 tasks。发布平台仍须实际验证所支持 Windows 与 systemd 版本上的服务生命周期和阻断行为，不能仅以 unit 文本或 mock 测试代替平台门禁。

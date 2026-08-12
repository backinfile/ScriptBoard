# ADR-0148 将 AI Runtime 隔离到独立 Host 身份

受管安装不再由 Web 进程直接启动 Pi。正式包增加 `scriptboard-ai-host`；Web 只通过受保护的本机
IPC 提交领域级 `RuntimeLaunchRequest`。请求不包含可执行文件路径、扩展路径、工作目录或父进程
环境。AI Host 从自己的受信 State Root 解析当前签名 Runtime，并在 Host 内重建固定参数、最小
环境和私有目录。IPC 生产适配器明确拒绝通用 `LaunchSpec`，避免把新边界变成命令执行代理。

Linux 使用独立无登录 `scriptboard-ai` UID。Runtime 载荷由 Web 身份管理、AI 组只读；Pi Home、
Session 和 Workspace 只归 AI 身份。systemd 清空 capability，使用只读系统视图、私有临时目录，
并通过 `IPAddressDeny=any`、`IPAddressAllow=localhost` 将出站限制为环回 Provider/Tool Broker。
Windows AI Host 使用 `LocalService` 与 restricted `NT SERVICE\ScriptBoardAI` SID；安装目录只读，只有
Assistant 私有目录授予该 SID 修改权，Web、Broker 和 AI 使用不同服务 SID。Windows Service
Hardening 默认阻断该服务的入站与出站网络，只给 Provider/Tool Broker 的 IPv4/IPv6 环回 TCP
建立服务专用例外。

便携开发和 Runtime 安装健康检查继续使用进程内适配器；它不是受管部署的安全边界。Linux
systemd 单元还使用 `@system-service` seccomp allowlist、native syscall architecture、私有设备和
资源上限。正式发布仍须在支持的 Windows/systemd 版本执行真实服务与网络阻断门禁。

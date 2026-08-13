# ADR-0149 将 Run 隔离到独立 Worker 身份

受管 Web 不再直接构造或持有脚本子进程。正式包增加 `scriptboard-runner`，Web 通过受保护本机
IPC 发送不可变作业描述。描述只包含 Run ID、脚本路径、Web 已记录的 SHA-256、工作目录和类型化
参数，不包含 executable、环境或命令字符串。Worker 在自己的身份内重新计算完整脚本摘要、拒绝
符号链接工作目录、按自己读取的受保护配置解析并复核 executor，再使用最小 Run 环境启动。

stdout、stderr 和退出状态通过有界二进制帧分别返回；优雅/强制终止通过反向控制帧传递。Manager
只依赖 `ProcessLauncher`/`ManagedProcess` 接口，本地与 IPC 两个适配器共享相同监督、日志、超时
和状态机测试。一次性脚本只给 Web owner 与 Runner SID/组读取，Worker 不读取应用数据库或秘密。

Linux 使用无登录 `scriptboard-runner`、空 capability、`NoNewPrivileges`、进程/内存上限、
`@system-service` seccomp allowlist 和默认拒绝网络。Windows 使用 `LocalService` 与 restricted
`NT SERVICE\ScriptBoardRunner` SID，通过 Windows Service Hardening 默认拒绝网络，并通过每个 Run
的 Job Object 限制进程数和内存。每 Run 独立 cgroup 不是当前实现边界；正式发布必须在真实服务
管理器中验证 service-wide 限额、系统调用和网络阻断。

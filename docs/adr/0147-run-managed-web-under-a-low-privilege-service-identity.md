# 受管 Web 使用低权限、实例专用服务身份

正式安装不再让 Web 与特权 Broker 共享 root/LocalSystem。Linux 安装器创建无登录、无 home 的
`scriptboard-web` 系统用户，递归把 State Root 与实例外部密钥目录交给该用户，并把显式配置
限制为 root/服务组可读；systemd unit 清空 capability 集并启用基础进程与内核保护。Broker 仍
以 root 运行，Unix Socket 只接受 `scriptboard-web` 的 peer UID。

Windows Web 改用低权限 `NT AUTHORITY\LocalService`，同时启用 `NT SERVICE\ScriptBoard` 独立
服务 SID。安装器只向该 SID 授予安装目录读/执行、配置只读、State Root 与实例外部密钥目录修改
权限；Named Pipe DACL 也只接受该服务 SID 与 SYSTEM。Broker 继续以 LocalSystem 运行。Windows
外部主密钥仍使用 machine-scope DPAPI，文件 ACL 决定哪些服务能够读取密文材料。

身份不仅由安装器声明：受管版本每次启动都会核对实际进程 token。Linux 必须是已登记的
`scriptboard-web` UID 且不能是 UID 0；Windows 必须同时是 LocalService，并带有启用状态的
`NT SERVICE\ScriptBoard` SID。身份不符时 Web 在打开数据库和监听端口前 fail closed。

这使 Web 漏洞不再自动继承防火墙、包管理和其他 Broker 主机权限。Run 与 Assistant Runtime
已经迁移到独立 Runner/AI Host 身份；Ubuntu 26.04 的实际权限探针也确认这两个身份无法读取应用
数据库和实例主密钥。但 P0-02 仍未全部完成：Host Files 与凭据解封仍需要移出 Web 的进程/身份
边界。目前授予 Web 外部密钥目录访问是兼容现有凭据消费者的过渡措施，后续迁移完成后必须收回。

# 四个信任边界作为一个带版本的产品整体发布

ScriptBoard 保留 Web、Privileged Broker、Runner 与 AI Host 四个独立进程，不把它们合并为持有全部权限和秘密的单体。Web 与 Broker、Web 与 Runner 是必要安全边界；启用 AI 时，AI Host 也是必要边界。Web 处理不可信 HTTP 与页面状态但保持低权限；Broker 只接受固定特权领域操作；Runner 在独立身份中复核并执行已绑定摘要的脚本；AI Host 只持有受限 Runtime 与短期 Provider capability。任何一个面向输入的进程被攻破，都不应自动取得其他进程的 OS 权限、秘密或执行面。

部署体验收敛为“一套发布包、一个产品版本、四个内部组件”，而不是四个可任意组合升级的产品。正式 manifest 必须绑定四个二进制的名称、平台、版本、SHA-256 和 IPC 协议版本；安装、升级、回滚与卸载整体准备、验证和提交，拒绝未知或不兼容的混合版本。每条 IPC 在建立连接时交换组件身份与协议版本，不支持的组合 fail closed。诊断页可以展示组件版本与摘要，但不能提供绕过整体事务的单组件升级入口。

Web 与 Broker 是常驻服务。Linux 受管安装通过两个独立的 systemd `.socket` unit 激活 Runner 与 AI Host：Web 只依赖受保护 Unix Socket，未发生 Run 或未使用 AI 时对应执行进程不预先启动；服务进程接管 systemd 传入的唯一 FD 时会复核 PID、FD 数、FD 名和实际 Unix endpoint，随后仍逐连接校验 Web peer UID。Windows 按需激活保留为真实 SCM 发布门禁。按需启动只能减少常驻攻击面，不能把 Runner 或 AI 逻辑搬回 Web，也不能让 Web 直接继承它们的凭据、工作目录或网络能力。Broker 是否进一步按需激活，要等平台身份、审计 checkpoint 和恢复路径能在激活期间保持 fail closed 后再单独评审。

这与 Cockpit 的非特权 Web/bridge 与按需提权模型最接近；Webmin 的全 root 单体虽然部署简单，但会显著扩大 Web 漏洞的爆炸半径；Portainer 的 Server/Agent 也支持控制面与执行面拆分。参考：[Cockpit 开发指南](https://cockpit-project.org/guide/latest/)、[Cockpit 安全模型](https://cockpit-project.org/blog/is-cockpit-secure.html)、[Webmin 简介](https://webmin.com/docs/intro/) 与 [Portainer 架构](https://docs.portainer.io/start/architecture)。这些参考用于验证边界方向，不意味着复制其通用终端、插件或远程管理功能。

Windows `--development-current-user` 四进程部署只证明功能与协议路径，不是生产身份门禁。正式发布仍必须在真实 SCM 中验证独立服务 SID、Named Pipe DACL、文件 ACL、服务启动依赖、按需激活、逐组件崩溃恢复，以及整体升级、回滚和卸载矩阵；Ubuntu systemd 也必须验证 unit/socket 依赖和混合版本拒绝。通过这些平台门禁前，不宣称正式四服务安装完成。

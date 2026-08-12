# 架构决策记录

本目录保存 ScriptBoard 的 Architecture Decision Records（ADR）。实现、评审或修改某个领域前，先阅读相关的当前决策，再把已取代的 ADR 仅作为历史背景。

[返回项目 README](../../README.md) · [领域词汇](../../CONTEXT.md) · [产品需求](../PRD.md) · [数据模型](../DATA-MODEL.md) · [验收标准](../ACCEPTANCE.md)

## 阅读约定

- 文件名采用 `NNNN-kebab-case.md`，正文标题描述决策结果；
- 没有状态声明的 ADR 默认视为已接受；
- `status: superseded by ...`、`> 状态：已被 ... 取代` 或正文中的明确取代声明具有同等效力；
- 已取代的 ADR 不再指导新实现；出现冲突时采用取代它的新 ADR；
- 新决定应新增 ADR，并在新旧文档中互相链接，不直接重写历史结论；
- 本目录历史上存在两个 `0109` 文件。它们的完整路径是稳定标识，后续 ADR 不应为修正编号而重命名历史文件。

## 主题索引

下表提供当前实现的入口，不替代完整 ADR 集。

| 主题 | 建议先读 |
| --- | --- |
| 执行模型与进程生命周期 | [ADR-0001 跨平台执行](./0001-capability-based-cross-platform-execution.md)、[ADR-0006 原位执行](./0006-execute-scripts-in-place-without-registration.md)、[ADR-0027 无队列并发](./0027-allow-unbounded-concurrent-runs-without-a-queue.md)、[ADR-0050 Run 状态机](./0050-use-an-explicit-run-state-machine-without-queueing.md)、[ADR-0120 一次性源码](./0120-separate-one-time-source-ownership-from-workdir.md)、[ADR-0134 统一进程启动策略](./0134-centralize-process-launch-policy.md) |
| 主机文件系统与恢复 | [ADR-0122 主机文件系统与受保护路径](./0122-browse-the-host-filesystem-with-protected-paths.md)、[ADR-0004 不跟随链接](./0004-do-not-follow-filesystem-links.md)、[ADR-0036 原子替换](./0036-replace-uploaded-files-atomically-through-trash.md)、[ADR-0045 文本编辑](./0045-provide-bounded-optimistic-text-editing.md) |
| 快捷执行与变量 | [ADR-0016 默认立即执行](./0016-quick-runs-start-immediately-by-default.md)、[ADR-0017 普通参数变量](./0017-use-plain-parameter-variables.md)、[ADR-0061 引用完整性](./0061-enforce-variable-reference-integrity.md)、[ADR-0111 创建快捷执行](./0111-create-quick-runs-from-files-or-run-history.md)、[ADR-0112 组织与软锁](./0112-organize-edit-copy-and-soft-lock-quick-runs.md) |
| 计划与重叠策略 | [ADR-0028 重叠确认](./0028-confirm-human-overlaps-and-configure-cron-overlaps.md)、[ADR-0030 内置调度器](./0030-use-an-internal-scheduler-instead-of-crontab.md)、[ADR-0031 不补跑](./0031-do-not-catch-up-missed-schedules.md)、[ADR-0032 实例时区](./0032-use-one-instance-time-zone.md)、[ADR-0033 五字段 Cron](./0033-use-five-field-minute-granularity-schedules.md) |
| 身份、权限与审计 | [ADR-0018 路由鉴权](./0018-authenticate-all-protected-web-routes.md)、[ADR-0041 审计事件](./0041-record-admin-and-system-audit-events.md)、[ADR-0044 Argon2id](./0044-use-only-versioned-argon2id-password-hashes.md)、[ADR-0121 固定用户角色](./0121-use-fixed-instance-wide-user-roles.md)、[ADR-0131 移除明文管理员密码配置](./0131-remove-plaintext-admin-password-configuration.md)、[ADR-0132 统一秘密脱敏](./0132-redact-secrets-at-observability-and-export-boundaries.md)、[ADR-0133 审计请求关联](./0133-correlate-audit-events-with-requests-and-authentication-assurance.md)、[ADR-0144 TOTP 与单次恢复码](./0144-add-totp-with-one-time-recovery-codes.md)、[ADR-0145 外部签名审计 checkpoint](./0145-anchor-audit-chain-outside-state-root.md)、[ADR-0146 特权操作 Broker](./0146-route-host-mutations-through-a-privileged-broker.md)、[ADR-0150 passkey 与高权限注册策略](./0150-require-verified-passkeys-for-privileged-accounts.md) |
| 配置、网络与部署 | [ADR-0019 回环 HTTP](./0019-bind-plain-http-to-loopback-only.md)、[ADR-0020 Windows 服务与托盘](./0020-separate-windows-service-and-tray-controller.md)、[ADR-0056 配置分层](./0056-layer-startup-configuration-with-cli-highest.md)、[ADR-0064 服务管理 CLI](./0064-provide-native-service-management-commands.md)、[ADR-0082 非回环 TLS](./0082-require-tls-for-non-loopback-access.md) |
| 历史、日志与诊断 | [ADR-0014 Run 事件日志](./0014-use-one-ordered-run-event-log.md)、[ADR-0039 单次日志上限](./0039-cap-each-run-log-while-preserving-head-and-tail.md)、[ADR-0040 总日志空间](./0040-retain-run-metadata-and-bound-total-log-storage.md)、[ADR-0078 历史不可删除](./0078-run-history-cannot-be-deleted.md)、[ADR-0090 doctor](./0090-provide-a-read-only-local-doctor-command.md)、[ADR-0132 统一秘密脱敏](./0132-redact-secrets-at-observability-and-export-boundaries.md) |
| Web 架构与体验 | [ADR-0060 纯 Go 服务端渲染](./0060-use-a-server-rendered-pure-go-stack.md)、[ADR-0108 双语 Web](./0108-localize-the-web-in-zh-cn-and-en-us.md)、[ADR-0109 按意图分组路由](./0109-group-web-routes-by-operator-intent.md)、[ADR-0110 Chromium 门禁](./0110-use-desktop-chromium-as-the-browser-gate.md)、[ADR-0113 主机 Markdown 渐进增强](./0113-progressively-render-managed-markdown.md) |
| 主机、应用与网站观测 | [ADR-0107 宿主状态](./0107-provide-bounded-local-host-status.md)、[ADR-0109 网站监控模块](./0109-own-website-monitoring-in-a-bounded-module.md)、[ADR-0118 应用与容器观测](./0118-observe-host-applications-and-local-docker-containers.md)、[ADR-0119 源日志](./0119-stream-source-logs-on-demand.md)、[ADR-0128 实例间只读网站监控](./0128-share-website-monitoring-read-only-between-instances.md) |
| AI 对话、工具与 Pi Runtime | [ADR-0123 私有 Pi RPC Runtime](./0123-use-pi-rpc-as-a-private-assistant-runtime.md)、[ADR-0124 Tool Broker 与一次性审批](./0124-broker-assistant-tools-and-bind-state-changes-to-approvals.md)、[ADR-0125 签名 Runtime 发布](./0125-pin-pi-runtime-to-signed-scriptboard-releases.md)、[ADR-0135 会话级 Provider 凭据代理](./0135-proxy-assistant-provider-credentials-per-runtime-session.md)、[ADR-0136 Windows Job Object 限制](./0136-bound-windows-assistant-runtime-with-job-objects.md) |
| 发布与应用更新 | [ADR-0065 支持平台](./0065-support-modern-windows-and-systemd-linux.md)、[ADR-0115 管理员确认更新](./0115-auto-check-and-require-admin-approval-for-updates.md)、[ADR-0116 签名发布清单](./0116-use-signed-release-manifests-for-updates.md)、[ADR-0117 版本化安装](./0117-use-versioned-service-installs-and-an-external-updater.md) |

## 已取代决策

| 当前决策 | 已取代的历史决策 |
| --- | --- |
| [ADR-0131 管理员启动凭据只接受密码文件或一次性引导](./0131-remove-plaintext-admin-password-configuration.md) | [ADR-0021 自动初始化并允许启动时重设管理员凭据](./0021-bootstrap-and-reset-the-admin-credential.md) 中的明文 `--admin-password` 部分 |
| [ADR-0130 安全边界默认拒绝并最小化子进程环境](./0130-default-to-closed-security-boundaries.md) | [ADR-0047 脚本继承服务进程环境](./0047-inherit-the-service-process-environment.md) |
| [ADR-0023 默认使用宿主系统最高权限](./0023-default-to-highest-host-privileges.md) | [ADR-0002 信任管理员脚本](./0002-trust-admin-provided-scripts.md) |
| [ADR-0027 允许无队列并发](./0027-allow-unbounded-concurrent-runs-without-a-queue.md) | [ADR-0012 拒绝并发](./0012-reject-instead-of-queueing-executions.md)、[ADR-0025 繁忙时跳过](./0025-skip-busy-cron-triggers-without-queueing.md) |
| [ADR-0030 使用内置调度器](./0030-use-an-internal-scheduler-instead-of-crontab.md) | [ADR-0024 管理 crontab 条目](./0024-manage-only-owned-entries-in-runtime-users-crontab.md)、[ADR-0026 本机控制套接字](./0026-trigger-cron-runs-through-a-local-control-socket.md) |
| [ADR-0162 带审计连续性的加密私有状态备份](./0162-provide-encrypted-private-state-backup-with-audit-continuity.md) | [ADR-0074 CLI 备份与恢复](./0074-provide-cli-backup-for-private-application-state.md)、[ADR-0100 不提供用户备份命令](./0100-do-not-provide-user-facing-backup-commands.md) |
| [ADR-0108 Web 提供中英文](./0108-localize-the-web-in-zh-cn-and-en-us.md) | [ADR-0081 仅简体中文](./0081-ship-a-simplified-chinese-only-mvp.md) |
| [ADR-0110 使用桌面 Chromium 门禁](./0110-use-desktop-chromium-as-the-browser-gate.md) | [ADR-0083 现代浏览器自动化范围](./0083-support-modern-desktop-and-mobile-browsers.md) |
| [ADR-0111 从文件或历史创建快捷执行](./0111-create-quick-runs-from-files-or-run-history.md) | [ADR-0015 仅显式保存快捷执行](./0015-save-quick-runs-explicitly.md) |
| [ADR-0115 检查更新并要求管理员确认](./0115-auto-check-and-require-admin-approval-for-updates.md) | [ADR-0076 不自更新](./0076-do-not-self-update.md) |
| [ADR-0117 版本化服务安装与外置更新程序](./0117-use-versioned-service-installs-and-an-external-updater.md) | [ADR-0077 仅便携归档](./0077-ship-portable-archives-instead-of-native-installers.md) |
| [ADR-0122 主机文件系统与受保护路径](./0122-browse-the-host-filesystem-with-protected-paths.md) | [ADR-0003 受管根目录](./0003-managed-root-is-a-file-server.md)、[ADR-0005 受管文件与状态分离](./0005-separate-managed-files-from-private-state.md)、[ADR-0035 单一应用回收站](./0035-move-web-deletions-to-an-application-trash.md)、[ADR-0055 平台数据路径默认值](./0055-use-platform-data-directories-for-managed-and-private-state.md)、[ADR-0071 禁止跨文件系统](./0071-do-not-cross-filesystem-or-volume-boundaries.md)、[ADR-0091 至 ADR-0099](./0091-version-eligible-managed-files-in-one-local-git-repository.md)、[ADR-0101 至 ADR-0106](./0101-use-a-fixed-identity-and-structured-git-commit-messages.md) 的本地 Git 版本保护决策 |

有些新 ADR 是扩展而不是取代。例如 [ADR-0121](./0121-use-fixed-instance-wide-user-roles.md) 在既有会话与密码决策上增加固定角色用户系统；[ADR-0118](./0118-observe-host-applications-and-local-docker-containers.md) 在 [ADR-0107](./0107-provide-bounded-local-host-status.md) 之外增加独立的应用观测页面。未冲突的旧决定仍然有效。

## 最新决策

- [ADR-0162 提供带审计连续性的加密私有状态备份](./0162-provide-encrypted-private-state-backup-with-audit-continuity.md)，用一致性 SQLite snapshot、分块认证加密、固定归档边界和受控审计重锚建立 State Root 带外恢复基础。
- [ADR-0161 将受管 Host Files 宿主访问限制在 Broker 内](./0161-keep-managed-host-file-access-inside-the-broker.md)，把目录、内容、日志、发布、移动与无人值守固定资源访问移出 Web，并以用户绑定短期句柄和执行点复核收口。
- [ADR-0160 将受管 MySQL 凭据与执行能力限制在 Broker 内](./0160-keep-managed-mysql-credentials-and-execution-inside-the-broker.md)，让 Broker 持有密码、数据库连接、客户端进程、备份计划与中断恢复，Web 只编排无秘密元数据。
- [ADR-0159 将受管 Assistant Provider 凭据与代理会话限制在 Broker 内](./0159-keep-managed-assistant-provider-credentials-and-proxies-inside-the-broker.md)，由 Broker 绑定 Owner、模型、Endpoint 与 Key，并只向 Web 返回短期环回代理能力和撤销句柄。
- [ADR-0158 将受管远程网站连接凭据与 Endpoint 一并限制在 Broker 内](./0158-keep-managed-remote-website-credentials-inside-the-broker.md)，由 Broker 绑定目标和 Key、注入认证并执行受控 HTTPS 获取，Web 只接收经过验证的领域响应。
- [ADR-0157 将受管 Passkey 公钥凭据状态限制在 Broker 内](./0157-keep-managed-passkey-state-inside-the-broker.md)，要求变更操作重新验证同用户近期会话，并把登录计数器更新限制为不可替换注册身份的字段集合。
- [ADR-0156 将受管 MFA 状态限制在 Broker 内](./0156-keep-managed-mfa-state-inside-the-broker.md)，让受管 Web 只调用状态、注册、确认、验证与重置五个领域操作，并拒绝把 Broker 退化为通用 Seal/Unseal 服务。
- [ADR-0147 受管 Web 使用低权限、实例专用服务身份](./0147-run-managed-web-under-a-low-privilege-service-identity.md)，让 Linux Web 使用无登录 `scriptboard-web`、Windows Web 使用 `LocalService` 与独立服务 SID，并按目录授予最小 ACL；Broker 保持 root/LocalSystem。
- [ADR-0148 将 AI Runtime 隔离到独立 Host 身份](./0148-isolate-ai-runtime-behind-a-dedicated-host.md)，让受管 Web 仅通过领域级本机 IPC 启动 Runtime；Linux 使用独立 UID 和环回网络，Windows 使用独立服务 SID 与私有目录 ACL。
- [ADR-0149 将 Run 隔离到独立 Worker 身份](./0149-isolate-runs-behind-a-dedicated-worker.md)，让受管 Web 只发送不可变作业描述，由独立 Runner 复核摘要、执行器、目录和资源边界。
- [ADR-0151 通过持久 outbox 转发已提交审计事件](./0151-forward-committed-audit-events-through-a-durable-outbox.md)，把本地链成功提交后的事件可靠发送到 HTTPS SIEM，并生成有界窗口安全告警。
- [ADR-0152 在版本回切前验证并分阶段恢复数据库](./0152-verify-and-stage-database-rollback-before-version-switch.md)，用双重 SQLite 校验、可逆文件替换与故障注入避免损坏 snapshot 覆盖活动库。
- [ADR-0153 使用 Windows Service Hardening 与 systemd seccomp 隔离 Runtime Host](./0153-use-service-hardening-and-seccomp-for-runtime-hosts.md)，让 AI 仅能访问环回代理、Runner 默认无网络，并在两个平台收紧服务 Token、系统调用和资源边界。
- [ADR-0154 将审计事件绑定到资源 revision 与 digest](./0154-bind-audit-events-to-resource-revisions-and-digests.md)，用 audit chain v3 保护 Broker 参数、Quick Run 发布版本和脚本摘要等结构化资源身份。
- [ADR-0146 主机写操作通过独立、单次授权的特权 Broker](./0146-route-host-mutations-through-a-privileged-broker.md)，把首批防火墙与主机安全写操作移出 Web 调用路径，并重新验证会话、step-up、资源 revision 和单次 capability。
- [ADR-0145 使用 State Root 外 Ed25519 checkpoint 锚定审计链](./0145-anchor-audit-chain-outside-state-root.md)，使本地链尾与同库状态一起回退时仍能由外部信任材料发现。
- [ADR-0144 为账户增加无降级旁路的 TOTP 与单次恢复码](./0144-add-totp-with-one-time-recovery-codes.md)，使已配置账户的登录和 step-up 都达到 AAL2，并提供带外管理员恢复。
- [ADR-0143 可恢复凭据使用 State Root 外部、操作系统保护的主密钥](./0143-seal-recoverable-secrets-with-an-external-host-key.md)，让 Provider、MySQL 与远程网站 Key 的密文和解密材料不再同库存放。
- [ADR-0142 Host Files 可执行上传先进入私有收件箱再发布](./0142-stage-executable-host-uploads-before-publication.md)，把接收字节与使脚本进入可执行主机路径拆成两个有审计的动作。
- [ADR-0141 TLS 验证例外一小时到期且远程汇聚只接受 HTTPS](./0141-expire-monitor-tls-exceptions-and-require-https-aggregation.md)，移除网站探测的永久弱校验状态，并收紧跨实例只读汇聚传输。
- [ADR-0140 Custom Dashboard 数据源使用共享出站策略且不跟随重定向](./0140-route-custom-dashboard-sources-through-outbound-policy.md)，阻止数据卡片访问本机、私网或元数据服务，并避免保存的凭据跨重定向转发。
- [ADR-0139 配置导入先验证文件边界再进入领域解码](./0139-validate-configuration-imports-before-domain-decoding.md)，为 JSON 配置建立文件名、MIME、编码和对象根约束。
- [ADR-0138 可信代理只接受一套有界 X-Forwarded 合同](./0138-use-one-bounded-trusted-proxy-header-contract.md)，拒绝多规范混用、坏值、重复字段和无界代理链。
- [ADR-0137 MySQL 恢复禁用本地客户端命令并限制 gzip 展开](./0137-disable-local-mysql-commands-during-restore.md)，防止导入 SQL 调用宿主命令并限制压缩展开资源。
- [ADR-0136 使用 Job Object 限制 Windows Assistant Runtime](./0136-bound-windows-assistant-runtime-with-job-objects.md)，为 Pi 增加单进程、内存、CPU、UI 与强制回收边界。
- [ADR-0135 按 Runtime 会话代理 Assistant Provider 凭据](./0135-proxy-assistant-provider-credentials-per-runtime-session.md)，从 Pi 环境与配置移除真实上游地址和凭据，并用进程生命周期 capability 收口正常 Provider 流量。
- [ADR-0134 统一生产子进程启动策略](./0134-centralize-process-launch-policy.md)，让所有生产 Go 子进程显式选择环境策略并用仓库级门禁阻止旁路。
- [ADR-0133 审计事件记录请求关联与认证保证](./0133-correlate-audit-events-with-requests-and-authentication-assurance.md)，用可验证的结构字段关联 Web、External Interface 和认证上下文。
- [ADR-0132 在可观测性与配置导出边界统一脱敏秘密](./0132-redact-secrets-at-observability-and-export-boundaries.md)，让日志、审计、错误、配置导出和 doctor 共用一套格式化脱敏规则。
- [ADR-0131 管理员启动凭据只接受密码文件或一次性引导](./0131-remove-plaintext-admin-password-configuration.md)，移除 YAML、环境变量和 CLI 的长期明文密码入口。
- [ADR-0130 安全边界默认拒绝并最小化子进程环境](./0130-default-to-closed-security-boundaries.md)，落实安全加固计划的首个可发布切片。
- [ADR-0127 仅开放有界的外部触发动作](./0127-expose-only-bounded-external-trigger-actions.md)，取代 ADR-0063 的全面不开放公开接口结论。
- [ADR-0128 在 ScriptBoard 实例之间只读共享网站监控](./0128-share-website-monitoring-read-only-between-instances.md)，扩展 External Interface 的有界只读能力。

## 基线文档

ADR 解释“为什么这样决定”，以下文档说明“产品要做什么”和“如何验收”：

- [MVP 产品需求](../PRD.md)
- [数据模型与状态机](../DATA-MODEL.md)
- [验收标准](../ACCEPTANCE.md)
- [领域词汇](../../CONTEXT.md)

修改基线文档时，应检查相关 ADR 是否仍然一致；若产品方向发生冲突，先新增或取代 ADR。

# ADR-0183 可配置的 Runner 与任务内存额度

取代 ADR-0172 与 ADR-0153 中固定内存数值的实现约束。执行身份和资源额度继续独立；任务数限制保持原有值。

## 配置与记录

Runner 总额度、任务默认额度、Windows 单进程额度与 Linux swap 额度保存在 State Root 的 runner-memory.db。首次启动将已有启动额度写入状态库；以后 Web、Runner 和服务重启流程读取持久值。

内存容量使用正整数及 B/KiB/MiB/GiB/TiB；unlimited 是明确的不限额选择。swap 额外接受 0。负数、溢出和不支持的平台选项必须报错。默认值保持原有 Windows 4 GiB 总量、4 GiB Run、2 GiB 单进程，以及 Linux 2 GiB 总量、swap 0。

手动、一次性、快捷和计划任务支持继承或指定额度。快捷项内存变更发布新 revision；复制、重跑、外部快捷触发与 MCP 快捷触发保留额度。Schema 69 为 quick_runs、schedules 与 runs 添加 memory_limit；旧任务默认继承，Run 创建时记录解析后的当次额度。

## 设置页

`/settings/memory` 由系统管理权限和最近认证保护，抽屉保存固定内存字段到独立 SQLite 状态库。受管 Web 通过 Broker 的一次性 capability 写入启动时绑定的 State Root；便携模式直接写入。数据库 revision 拒绝过期表单。页面区分已保存额度与生效额度，保存不触发自动重启。

Runner 只获得此非秘密策略数据库的读取权；无需读取账户数据库。SQLite 使用 DELETE 日志模式，读取不需要目录写权限。私有状态备份以一致性快照包含内存策略并随恢复替换。

## 执行边界

Windows 以聚合 Job Object 约束并发总量，每 Run 保留独立 Job Object。进程先挂起创建，完成 Job 分配后恢复，避免脚本提前派生子进程。unlimited 只清除对应内存标志，保留任务数和退出清理机制。

Linux 在受管 cgroup v2 服务上委派 memory 控制器。Runner 位于 supervisor 子组，任务在各自子组内执行。固定参数的启动器先写入 cgroup.procs 再 exec 解释器，兼容现有 RestrictNamespaces 与 seccomp；不向 shell 插入用户命令文本。任务设置 memory.max 和 memory.oom.group，退出时终止并清理子树。

隔离模式保留 ProtectControlGroups=true，仅对 /sys/fs/cgroup/system.slice/scriptboard-runner.service 添加 ReadWritePaths 例外，其他宿主 cgroup 维持只读。原有身份、网络、namespace 与 seccomp 策略继续适用。参考 [systemd 委派规则](https://github.com/systemd/systemd/blob/main/docs/CGROUP_DELEGATION.md)。这些额度用于可信脚本的资源管理，不构成不可信代码沙箱。

## 生效与诊断

默认任务额度在数据库保存成功后发布到运行管理器，通过读写锁保证并发安全；保存与发布串行执行，保存失败不改变默认值。新任务在启动时解析并固定额度，快捷执行显式额度优先，留空继承最新默认值。Runner 总额度和平台专属额度在 Runner 重启时生效。Linux 服务启动、应用级重启及版本切换同步受管 unit 的资源字段，不覆盖其他属性；Runner 再核验实际 memory.max 与 memory.swap.max，不一致或委派不可用时明确失败。doctor 列出配置，Runner 启动日志报告应用结果。按任务修改只影响后续运行。

Windows 额度约束提交内存，Linux memory.max 约束 cgroup 内存记账，二者不是完全相同的物理内存指标。任务指定更高额度不会突破总额度；unlimited 不突破系统或上级约束，也不隐式修改 swap。

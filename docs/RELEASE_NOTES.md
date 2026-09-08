# ScriptBoard v2.10.0

## 新增功能

- 支持配置 Runner 总内存额度、默认任务额度，以及 Windows 单进程额度和 Linux swap 额度。
- 任务表单新增“任务内存上限”，可填写 `512MiB`、`8GiB` 或 `unlimited`；留空继承默认值，额度包含子进程。
- 创建、编辑、复制、定时执行和重新运行均保留任务额度；运行详情显示当次使用的额度。

## 升级

可从 `v2.0.25` 或更高版本在“系统设置 → 更新”中直接升级。升级前建议备份自定义配置与 ScriptBoard 状态目录。

默认总额度保持不变：Windows 为 4 GiB，Linux 为 2 GiB。任务设为 `unlimited` 时仍受全局额度及 Windows 单进程额度约束。

Linux 受管执行需要 cgroup v2。修改全局额度后，以管理员身份运行 `scriptboard service restart` 同步配置；Windows 便携运行需重新启动进程。配置示例见 README 的“脚本内存限制”。

# 管理员启动凭据只接受密码文件或一次性引导

ScriptBoard 不再从 YAML `admin_password`、环境变量 `SCRIPTBOARD_ADMIN_PASSWORD` 或命令行 `--admin-password` 接受管理员明文密码。这些入口会把长期凭据复制到配置备份、服务定义、进程环境、进程列表或 Shell 历史，且无法通过应用内脱敏可靠撤回。

旧入口即使配置为 YAML `null` 也会 fail closed，并返回指向对应 password file 入口的迁移错误。需要在启动时权威覆盖管理员凭据时，只接受绝对路径的 `admin_password_file`、`SCRIPTBOARD_ADMIN_PASSWORD_FILE` 或 `--admin-password-file`；文件内容继续执行有界读取、UTF-8 与密码长度规则，文件路径纳入 Host Filesystem 受保护路径。测试和浏览器 fixture 也必须通过真实临时密码文件进入同一边界，不保留仅供测试使用的明文内部字段。

首次启动和 `scriptboard admin reset` 不属于长期配置：它们仍生成随机高熵密码并写入 State Root 的权限受限一次性文件，用户修改密码后删除。后续 OS secret provider 可以成为 password file 的来源或替代，但不能重新引入配置值、环境值或命令行明文。

此决策取代 [ADR-0021](./0021-bootstrap-and-reset-the-admin-credential.md) 中允许 `--admin-password` 的部分；该 ADR 关于首次启动、网页修改和本机重置的其余决定继续有效。

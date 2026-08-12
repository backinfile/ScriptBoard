# 在版本回切前验证并分阶段恢复数据库

更新回滚不能仅相信创建 snapshot 时的成功结果；snapshot 在目标版本验证期间仍可能被截断、替换或发生介质错误。因此每次 rollback 都必须在停止服务和切换 executable 之前，以只读 SQLite `quick_check(1)` 重新验证 snapshot。失败时操作进入 `needs_recovery`，活动版本和数据库保持不变。

数据库恢复先把 snapshot 同步复制到同目录 staging 文件，并再次对 staging 副本执行 `quick_check(1)`。验证通过后，现有数据库先改名为 `.update-replaced`，staging 再改名为正式数据库；第二次 rename 失败时立即尝试把旧库改回。成功后删除旧库与 WAL/SHM 残留并同步父目录。这样截断 snapshot 与 staging 写入错误不会先删除活动数据库，而中断留下的命名文件也有明确含义。

故障注入测试固定三个不变量：损坏 snapshot 不改变活动库；有效 snapshot 经 staging 校验替换且不遗留临时文件；自动 rollback 在 snapshot 失败时不触及平台服务边界。Linux 安装测试还在 `current` symlink 已替换、`install.json` staging 写失败的精确边界注入错误，并验证既有 `SetCurrent`/`repair-current` 语义可按可信 metadata 确定性重建指针。State Root schema 41→42 迁移测试在新增 MFA 列后的数据回填点注入失败，要求 schema 版本、列定义和原有管理员数据随同一个 SQLite 事务完整回滚。

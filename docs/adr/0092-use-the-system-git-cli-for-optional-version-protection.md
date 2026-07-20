# 可选版本保护使用宿主 Git CLI

本地 Git 保护默认关闭，并依赖宿主安装的 Git CLI；启用时验证可执行文件和兼容版本，YAML 可配置绝对路径以适配 Windows LocalSystem 缺少用户 PATH 的情况。找不到 Git 时网页解释原因并拒绝启用。所有 Git 调用通过固定参数数组执行，不经过 Shell；应用不引入 libgit2、CGO 或自行实现 Git 协议，`doctor` 负责检查 Git 版本、仓库完整性和权限。

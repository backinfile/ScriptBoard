# 不支持 Git LFS 与可执行 attributes

版本保护不执行 Git LFS、clean/smudge filter、外部 diff 或自定义 merge driver；接管时发现 `filter=...` 等会启动外部程序的 `.gitattributes` 配置便拒绝并列出位置，启用后新增则使 Git 状态异常。允许 `text`、`binary`、`eol` 等纯数据属性。超过 10 MiB 的文件保持未跟踪而不交给 LFS，避免 root/LocalSystem Git 操作间接执行仓库提供的程序。

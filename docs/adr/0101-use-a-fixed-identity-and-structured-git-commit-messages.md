# Git 自动提交使用固定身份与结构化消息

所有版本保护提交固定使用 `ScriptBoard <scriptboard@localhost>`，不读取系统、用户或被接管仓库的 `user.name` / `user.email`。提交标题由操作类型生成，例如文件变更、运行前后检查点和单文件恢复，并通过 trailers 保存操作类型、Run ID 与审计事件 ID；管理员不能输入任意提交信息。Git 对象保存绝对时间，页面按实例统一时区展示。

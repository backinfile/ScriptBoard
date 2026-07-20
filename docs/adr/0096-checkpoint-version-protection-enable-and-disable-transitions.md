# Git 保护启停转换必须创建检查点

只有没有活动 Run 时才能在网页切换版本保护。首次初始化创建 baseline，接管外部仓库净化后创建 adoption checkpoint，停用前创建 final checkpoint，再启用时先展示停用期间差异并经确认创建 re-enable checkpoint。停用不删除 `.git/`、历史或仓库标记，期间文件仍可变化。所有切换要求二次确认并写审计事件，使版本保护空窗清晰可见。

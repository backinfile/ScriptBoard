# 使用内置调度器而非系统 crontab

ScriptBoard 将计划项、cron 风格时间表达式、重叠策略和触发历史保存在 SQLite，并由服务进程内的调度器发起执行；不读取或修改 Linux crontab，也不需要 Unix Domain Socket 触发命令。这个决定取代 ADR-0024 和 ADR-0026，使计划执行直接复用变量解析、执行器选择、运行监督、日志和历史能力，并自然支持 Windows 与 Linux；代价是服务停止期间调度器不会运行，必须由产品明确漏执行和时区语义。

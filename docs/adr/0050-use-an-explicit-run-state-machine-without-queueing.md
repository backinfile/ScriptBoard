# 使用无队列的明确执行状态机

Run 从 `starting` 进入 `running` 或因进程创建失败进入 `failed`；运行后正常退出码 0 为 `succeeded`，非零为 `failed`，人工停止经过 `stopping` 并最终为 `cancelled`，自动超时经过 `timing_out` 并最终为 `timed_out`，服务重启失去监督关系则为终态 `disconnected`。请求在创建进程前校验失败不创建 Run。停止或超时一旦发生，最终状态由该原因决定而不再由随后退出码改写。模型不包含 `queued`。

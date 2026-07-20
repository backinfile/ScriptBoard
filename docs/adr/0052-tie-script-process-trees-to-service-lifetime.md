# 将脚本进程树绑定到服务生命周期

每次执行使用独立进程组或 Job Object，并尽量让异常服务退出连带终止完整脚本进程树：Linux 设置父进程死亡信号，Windows Job Object 启用关闭时终止。这样避免 ScriptBoard 崩溃或被强杀后遗留无人监督的 root/LocalSystem 进程。下次启动仍将原 Run 标记为 `disconnected`，因为应用无法可靠取得最终退出原因和退出码；已落盘日志继续可读但不再增长。

# 使用 Job Object 限制 Windows Assistant Runtime

Windows 上每个受管 Pi Runtime 必须分配到独立 Job Object。Job 只允许一个活动进程，
因此 Runtime 不能创建子进程；单进程与整个 Job 的提交内存均限制为 1 GiB，每个 Runtime
进程累计用户态 CPU 限制为 15 分钟，未处理异常直接终止，Job 句柄关闭时强制回收全部
残留进程。

Job 同时启用全部适用的 Basic UI Restrictions，禁止访问或修改桌面、显示设置、关机、
全局 atom、外部用户句柄、剪贴板和系统参数。配置任一资源或 UI 限制失败时，Pi 启动
fail closed，不以仅有生命周期回收的弱 Job 降级运行。

Job Object 约束资源和 UI 面，但不会降低进程 Token 权限，也不能阻止其直接网络访问或
读取同一身份可访问的文件。受限 Token/AppContainer 可行性、秘密目录 ACL 与网络默认
拒绝仍是 P0-08 的后续必需边界。

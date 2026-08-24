# ADR-0172 默认 Runner 使用最高宿主权限

ScriptBoard 的默认受管部署面向可信管理员的服务器完全控制面板，而不是默认受限脚本平台。因此 Run 的默认操作系统执行身份改为宿主最高权限：Linux Runner 以 root 运行，Windows Runner 以 LocalSystem 运行。管理员在文件页面或快捷执行中选择的普通主机脚本应当在部署后开箱可执行，不要求额外给低权限 Runner 账号授权脚本目录。

Web 仍保持低权限服务身份，Privileged Broker 仍只提供固定领域特权操作，AI Host 仍使用隔离身份。默认提升只适用于 Runner，因为执行受信脚本是产品的核心控制面能力；让 Web 也最高权限会把认证、模板、上传、预览和浏览器攻击面直接变成整机控制面。

保留显式 `runner_identity_mode: isolated` 部署模式。该模式继续使用独立 Runner 身份、网络和系统调用限制以及脚本目录授权要求，适合希望把 ScriptBoard 当作受限执行入口的部署。默认 `runner_identity_mode: privileged` 代表所有可执行脚本等同于最高权限宿主代码，文档、安装验证和诊断输出必须按这个风险表述。

运行身份权限与宿主资源配额是两个独立维度。两种模式的受管 Runner 都共享服务级总量限制：Linux 使用同一个 `MemoryMax=2G`、`MemorySwapMax=0` 和 `TasksMax=64` cgroup；Windows 的所有 Run 同时加入一个 4 GiB/64 进程的聚合 Job Object，并继续保留每次 Run 的嵌套 Job Object。最高权限允许访问宿主能力，但不允许单个或并发 Run 无界消耗宿主内存。


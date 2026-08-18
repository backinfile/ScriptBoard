# ADR 0174: 下线跨实例网站监控

ScriptBoard 不再通过 External Interface 暴露 `website_monitor` 动作，也不再允许网站监控页保存、读取或展示远端 ScriptBoard 来源。本决策取代 [ADR-0128](./0128-share-website-monitoring-read-only-between-instances.md) 与 [ADR-0158](./0158-keep-managed-remote-website-credentials-inside-the-broker.md)；本地网站监控和自定义面板引用本地网站监控的能力不受影响。

新建与修改 External Interface 时只接受日志、上传、快捷执行和变量动作。升级时删除旧 `website_monitor` 条目与远端来源元数据，旧调用立即 fail closed；同时删除 State Root 中专用于这项功能的 `secrets/remote-website-connections.enc`。删除目标使用固定的 State Root 内路径，不扫描或删除其他凭据。历史数据库仍可经历旧版迁移步骤，但应用打开后不得保留可调用条目、来源行或专用密文。

Web 与受管启动组合不再初始化远程网站服务，网站监控路由、表单、列表与读取逻辑一并移除。Broker 的旧 wire 字段在协议兼容期内可以保留为不可用操作，但生产组合不提供服务实现；它们不得重新形成 UI 或可达执行路径。后续协议大版本可物理删除这些保留字段。

网站状态卡片继续直接引用本实例的网站监控结果。卡片标题中的“最近成功刷新时间”表示面板最近成功读取到数据的时间，由所选监控中最新的已完成检查时间派生；网站处于故障状态仍是有效监控数据，不因探测结果失败而隐藏刷新时间。本决策不创建跨实例快照或新的远程聚合层。

# 将 Assistant Playbook 与签名 Runtime 一起版本化

ScriptBoard 不开放 Pi Package 市场、用户级 Skill 或项目提示词发现。生产中的 Operational
Playbook 作为 Assistant Capability Bundle 的资源随 Pi Runtime 归档发布；
`capabilities.json` 显式声明稳定 ID、版本、相对路径、字节数、SHA-256、适用角色和所需
Broker 工具。Runtime 安装与解析阶段拒绝未知字段、重复 ID、路径越界、链接、大小或摘要
不符。旧 Runtime 可以没有能力清单，但不能承载非通用 Conversation Profile。

Conversation Profile 是对话持久状态，不是权限。启动 Agent Turn 时，Go 服务从活动
Runtime 的固定目录重新解析完全匹配的 Playbook，将内容作为 ScriptBoard 受信系统指导
传给受管 Pi；它不改变固定角色、工具清单、实时授权、一次性审批或审计。解析失败时拒绝
Turn 并提示安装匹配 Runtime，不降级到用户目录或在线内容。通用 Profile 不加载 Playbook。

日志、Run、计划和审计增强使用固定 Extension 声明的 Evidence Query。每次查询仍由 Tool
Broker 授权、限量、脱敏并记录；分页游标签名且绑定用户、对话、工具、目标与查询。图片
上下文也不构成新文件读取权限：只有用户明确引用且当前角色仍可读取的普通文件才进入
Safe Raster Processor，处理后最多四张、只驻留内存，并且模型配置与 Pi session 都必须
确认支持图片输入。

外部知识搜索不是 Capability Bundle 的基础能力。本 ADR 不启用任意网络、浏览器控制、
Shell、第三方 OAuth 或外部 Adapter；未来若增加，必须单独决策出站披露、域名限制、凭据
隔离、SSRF 防护、审计和默认关闭语义。

# 使用 Pi RPC 作为私有 Assistant Runtime

ScriptBoard 的 AI 对话使用原生服务端渲染工作区；不嵌入 Pi TUI、第三方 Web UI 或用户已经启动的 Pi 进程。Go 服务通过 Pi 的 headless RPC 模式在标准输入和标准输出上交换严格 LF 分隔的 JSONL，协议解析、请求关联、事件映射和进程生命周期全部封装在 Assistant Runtime 边界内。

每个活动 AI 对话最多拥有一个受管 Pi 进程。进程只从 State Root 内活动 Runtime 指针解析到的绝对路径启动，使用按用户和对话隔离的 session 目录、workspace 与私有 Pi home。启动参数明确关闭内置工具、资源发现、用户或项目 Extensions、Skills、Prompts、Themes 与上下文文件；子进程环境采用最小集合，只注入当前 LLM 所需的一项凭据。浏览器不接触原始 Pi RPC、凭据、环境、绝对路径或隐藏 reasoning。

Pi 进程不监听网络端口。Windows 以专属 Job Object 管理进程树，Linux 以独立进程组和父进程退出语义管理；停止一个对话只停止对应受管进程。ScriptBoard 不查询 PATH，不读取用户 Pi 配置，也不与用户 Pi 共用可执行程序、session 或更新目录，因此两者可以同时运行而不互相覆盖。

Agent Turn 只在收到 `agent_settled` 后完成。文本增量先持久化再发送浏览器安全 SSE；慢消费者有有界缓冲，不能阻塞 Pi stdout。服务退出或异常恢复把未完成消息标记为中断且不隐式重放。

没有已安装并激活的私有 Runtime 时，历史和设置仍可查看，但新 Prompt 返回明确的不可用状态。Runtime 的来源、安装和更新受 [ADR-0125](./0125-pin-pi-runtime-to-signed-scriptboard-releases.md) 约束；工具能力受 [ADR-0124](./0124-broker-assistant-tools-and-bind-state-changes-to-approvals.md) 约束。

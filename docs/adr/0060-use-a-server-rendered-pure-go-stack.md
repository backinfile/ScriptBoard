# 使用服务端渲染的纯 Go 技术栈

ScriptBoard 采用单 Go 模块，服务端使用标准库 `net/http`、`html/template`、少量原生 JavaScript、SSE、纯 Go SQLite 驱动和 `embed` 资源，构建为 Windows/Linux 单服务二进制；不引入 Node.js 构建链、SPA 框架、Redis 或消息队列。Windows 托盘作为同一模块中的独立轻量 GUI 可执行文件，共享配置解析与服务契约代码，以保留跨平台交叉编译和轻量部署。

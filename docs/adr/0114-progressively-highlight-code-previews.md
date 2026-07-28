# 在浏览器中渐进高亮代码预览

受管脚本预览继续由服务端完成会话验证、路径边界检查、最多 1 MiB 的 UTF-8 文本读取与 HTML 转义，并始终输出完整纯文本。服务端只按已支持的脚本扩展名确定语法：`.ps1` 使用 PowerShell，`.cmd` 与 `.bat` 使用 DOS，`.sh` 使用 Bash，`.py` 使用 Python；不做内容猜测或自动语言检测。

浏览器仅在独立脚本预览或 Markdown 显式代码围栏需要时，按需加载固定版本、自托管并由 Go `embed` 发布的 Highlight.js。使用官方通用浏览器包直接提供 Bash、Python 和常见 Markdown 围栏语言，PowerShell 与 DOS 语法作为小型补充资源按需加载；不使用 CDN，也不增加生产 Node.js 构建链。Highlight.js 的输出再由 DOMPurify 收窄为 `span` 与 `class`，整条链路成功后才替换纯文本；依赖失败、未知语言、无 JavaScript 和高亮异常均保持原文可用。

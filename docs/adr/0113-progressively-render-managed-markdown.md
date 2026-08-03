# 在浏览器中渐进渲染主机 Markdown

`.md` 主机条目由服务端完成会话与角色验证、受保护路径检查和最多 1 MiB 的 UTF-8 文本读取；服务端页面始终包含经 `html/template` 转义的原始 Markdown，作为无 JavaScript、依赖加载失败或渲染失败时的完整回退。启用 JavaScript 时，页面才按需加载内嵌于单一 Go 二进制的 `markdown-it` 与 DOMPurify：关闭 Markdown 原生 HTML，渲染后再次净化，并且仅在整条链路成功后替换原文视图。

相对 `.md` 链接、目录链接、文件下载和栅格图片统一解析为规范化绝对路径，再改写到使用 `path` 查询参数的文件路由；直接请求也不能绕过受保护路径、链接或特殊文件边界。远程图片不自动加载，只显示为可访问的外部链接，避免预览文档静默向第三方发送请求；主机文件仍不能作为 SVG、HTML 或其他同源主动内容执行。渲染依赖固定版本并随 `embed` 资源发布，不使用 CDN、不增加生产 Node.js 构建链，也不将文件正文复制到额外 AJAX API。

带有显式语言标记的 Markdown 代码围栏按 [ADR-0114](./0114-progressively-highlight-code-previews.md) 渐进高亮；无语言、未知语言或高亮失败时保留净化后的纯代码文本。

此决定修订 [ADR-0054](./0054-preview-only-text-and-safe-raster-images.md) 中 Markdown 与普通文本使用同一种视觉预览的部分；其会话验证、受限栅格图片和主动内容限制继续有效。它与 [ADR-0060](./0060-use-a-server-rendered-pure-go-stack.md) 的服务端渲染和单二进制部署边界一致。

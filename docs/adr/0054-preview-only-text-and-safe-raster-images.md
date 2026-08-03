# 只内嵌预览文本和安全栅格图片

网页仅内嵌预览已转义的 UTF-8 文本，以及内容检测与扩展名一致、尺寸受限的 PNG、JPEG、GIF 和 WebP 栅格图片。SVG、HTML、PDF、Office 文档、音视频和未知格式不内嵌，只能下载。预览与下载路由统一验证会话、角色和 [ADR-0122](./0122-browse-the-host-filesystem-with-protected-paths.md) 的主机路径保护策略；下载响应使用 `Content-Disposition: attachment` 与 `X-Content-Type-Options: nosniff`，避免主机文件成为同源主动内容入口。

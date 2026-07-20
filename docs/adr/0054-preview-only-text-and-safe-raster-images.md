# 只内嵌预览文本和安全栅格图片

网页仅内嵌预览已转义的 UTF-8 文本，以及内容检测与扩展名一致、尺寸受限的 PNG、JPEG、GIF 和 WebP 栅格图片。SVG、HTML、PDF、Office 文档、音视频和未知格式不内嵌，只能下载。预览路由统一验证管理员会话；下载响应使用 `Content-Disposition: attachment` 与 `X-Content-Type-Options: nosniff`，避免受管文件成为同源主动内容入口。

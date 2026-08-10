# 只内嵌预览文本和安全栅格图片

网页仅内嵌预览已转义的 UTF-8 文本，以及内容检测与扩展名一致、尺寸受限的 PNG、JPEG、GIF 和 WebP 栅格图片。文件页只对当前分页内可能作为文本处理的普通文件采样最多 64 KiB，经二进制文件头、严格 UTF-8、NUL 和控制字符比例检测后提供文本操作；已知文本或脚本扩展名未通过检测时也不显示预览与编辑入口。未知扩展名通过检测后只提供只读预览，真正打开时仍在 1 MiB 上限内校验全文，不开放编辑、日志跟踪或脚本执行。SVG、PDF、Office 文档、音视频和未通过文本检测的未知格式不内嵌，只能下载。预览与下载路由统一验证会话、角色和 [ADR-0122](./0122-browse-the-host-filesystem-with-protected-paths.md) 的主机路径保护策略；下载响应使用 `Content-Disposition: attachment` 与 `X-Content-Type-Options: nosniff`，避免主机文件成为同源主动内容入口。

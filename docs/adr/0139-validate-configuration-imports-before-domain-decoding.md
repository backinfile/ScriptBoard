# 配置导入先验证文件边界再进入领域解码

Website Monitor 与 Custom Dashboard 导入在读取有界 multipart 内容后、领域 JSON 解码
前执行外层文件策略。文件名必须是安全的 `.json` 名称，不得使用路径、Windows 保留名或
活动后缀；part MIME 只接受 `application/json`、`text/json`、`text/plain` 或浏览器常见
的 `application/octet-stream`。

内容必须在功能自己的大小上限内，是有效 UTF-8、无 NUL，并在去除空白后以 JSON 对象
开始。外层通过后仍由各领域以 `DisallowUnknownFields`、格式版本、字段长度、URL/请求
约束、重复项和最多 100 条记录进行完整验证。文件名、MIME 或外层内容不匹配时不得尝试
宽松解码或按另一种导入类型降级。

这套外层约束只适用于 JSON 配置，不扩展为普通 Host Files 的“全局允许列表”；数据库、
Runtime 包、图片和未来证书继续拥有各自的 magic、归档和发布策略。

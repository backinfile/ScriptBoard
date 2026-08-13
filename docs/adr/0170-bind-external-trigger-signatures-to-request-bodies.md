# 外部 Trigger v2 签名绑定完整请求体

External Interface 的签名证明必须覆盖会影响动作结果的全部请求语义。服务端只接受 v2 HMAC-SHA256：换行分隔签入版本、Unix 时间戳、nonce、大写 HTTP 方法、原始 Request URI、原始 Content-Type、实际 body 字节数和 body SHA-256。multipart 直接摘要实际传输字节及 boundary，不定义第二套规范化格式。

请求先经过不依赖凭据的 source/global 门禁，再解析 Key 和 Entry，并在认证后配额内把签名请求体有界暂存到 State Root 私有 `0600` 文件。只有实际摘要、长度和 HMAC 全部通过后才解析 body、创建 upload inbox、修改变量、写日志或启动 Run；失败与崩溃路径删除暂存文件。无效 Key 与未知 Entry 对外返回相同响应，详细原因只进入不含 Key 的审计证据。

v1 未绑定 body，不能作为兼容降级继续由生产入口接受。HTTP 仍会暴露 Bearer Key，v2 完整性证明不能替代 TLS。

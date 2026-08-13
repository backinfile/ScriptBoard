# Custom Dashboard 数据源使用共享出站策略且不跟随重定向

Custom Dashboard 的数据卡片允许管理员保存外部 JSON API 地址和请求头，因此它既是
服务器端出站能力，也是可恢复凭据的使用边界。数据源默认客户端必须使用共享
`OutboundPolicy`：不读取环境代理，DNS 解析后只连接固定且已验证的公网地址，阻止回环、
私网、链路本地、保留地址、云元数据地址及未授权端口。

数据卡片请求一律不跟随重定向。这样即使源站返回跨 origin 跳转，保存的 Authorization、
Cookie 或自定义凭据也不会被带往另一个目标。数据源 URL 只接受带主机的 HTTP(S)，拒绝
内嵌 userinfo 和 fragment。管理员仍可保存业务所需的 Authorization、Cookie 等普通头，
但头部总数和总大小有界，名称和值必须符合 HTTP 语法，并拒绝 Host、Proxy-Authorization、
Connection、Transfer-Encoding 等可改变连接、代理或传输语义的字段。

测试可以注入专用 HTTP Client 访问本地测试服务器；Manager 会复制该 Client、补上有界
超时并覆盖重定向策略，避免修改调用方实例。生产调用不提供“允许内网”旁路。若未来确需
内网 Dashboard 数据源，应像网站监控一样新增逐资源、可审计的高风险能力，而不是放宽本
决策的默认客户端。

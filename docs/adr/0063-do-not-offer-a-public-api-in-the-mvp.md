# MVP 不提供公共 API

ScriptBoard 的页面使用内部表单、少量 JSON 与 SSE 路由，但它们全部受 admin Session 和 CSRF 边界保护，且不承诺外部版本兼容性。MVP 不提供 API Token、Basic Auth、远程脚本触发接口或通用 CLI 客户端；本机 CLI 只承担安装、配置和维护。未来自动化集成必须另行设计可撤销凭据、权限范围与审计模型。

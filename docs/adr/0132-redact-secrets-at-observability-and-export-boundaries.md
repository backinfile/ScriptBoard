# 在可观测性与配置导出边界统一脱敏秘密

ScriptBoard 使用 `internal/secretredaction` 作为日志、审计、错误、配置导出和本机诊断输出的共享脱敏模块。模块识别具名 password、secret、token、API key、Authorization 与 Cookie 字段，以及 Bearer/Basic 凭据、URL 用户信息、敏感查询参数、ScriptBoard External Key、常见云与代码托管 Token、JWT 和私钥块；输出统一使用 `[REDACTED]`，同时尽量保留字段名和错误上下文。

脱敏尽量发生在持久化之前：审计事件先脱敏再计算串行哈希；Run 输出、Run/调度错误、文件操作错误、更新状态、网站与主机探测错误、自定义 Dashboard 刷新错误均不得把匹配的秘密写入其状态存储。文件和容器源日志不由 ScriptBoard 拥有，因此在统一日志条目边界脱敏后才进入 HTML、JSON 或 SSE。Web 错误响应无论是 HTML、纯文本还是 JSON，都在共享响应包装器提交前脱敏；CLI、Windows 服务日志和 `doctor` 报告使用同一模块。

审计 CSV、自定义 Dashboard 配置和网站监控配置导出在编码后再次防御性脱敏，且必须保持合法 CSV/JSON。配置导出因此不是凭据备份：Authorization、Cookie、URL Token 或正文中的常见秘密会变为占位符，导入后需要重新配置凭据。经过明确授权的原始业务载荷下载（例如用户选择的文件和 MySQL 备份）不属于诊断或配置导出，不能由文本脱敏器改写；它们继续依赖鉴权、step-up 和下载审计保护。

规则采用保守的格式识别而不是熵猜测，避免把摘要、版本、路径、ID 和普通运维文本误判为秘密。新增格式必须同时提供“应脱敏”和“不得改变”的回归测试；任何新日志、错误持久化、诊断或配置导出功能必须在其共享边界接入该模块，不能依靠调用方记得手工替换。

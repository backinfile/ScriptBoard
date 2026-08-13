# 将审计事件绑定到资源 revision 与 digest

schema 43 为 `audit_events` 增加结构化 `resource_revision` 和 `resource_digest_sha256`。任一字段存在时，事件使用 audit chain v3，把 request ID、认证保证、revision 与 digest 一起纳入长度前缀 SHA-256；字段为空的历史 v1/v2 事件保持原算法，不重写已有链。CSV、离线取证 JSONL、Web 审计页与远端已提交事件载荷都携带新字段，字段被修改后完整链验证必须失败。

特权 Broker 把授权时绑定的资源 revision 和规范参数 SHA-256 写入 intent 与 result 两条独立事件；Quick Run 的创建、更新、复制、锁定与启动事件记录发布 revision 和脚本 SHA-256；一次性 Run 的接受事件记录实际落盘源文件摘要。自由文本 `target` 只保留稳定资源 ID，不再兼任 revision 容器。后续资源类型应在领域边界已有不可变版本或内容摘要时填充，不能为满足字段而伪造版本号。

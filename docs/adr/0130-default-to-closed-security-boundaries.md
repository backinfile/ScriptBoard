---
status: accepted
---

# 安全边界默认拒绝并最小化子进程环境

ScriptBoard 的 Web 授权、代理信任、外部上传、出站网络和 Run 子进程环境采用默认拒绝。这个决定是运维面板安全加固计划的首个可发布切片，并取代 ADR-0047 的“完整继承服务环境”决定。

- 受保护请求必须命中已声明的权限分类；未知路径不再回落为观察权限。新增受保护 Handler 若未登记分类，将在进入业务 Handler 前被拒绝。
- `trusted_proxies` 默认为空。非可信 peer 提供的 `Forwarded` 与 `X-Forwarded-*` 头在入口删除；状态修改请求若携带 `Origin`，必须与有效请求 origin 一致。
- 外部上传必须配置非空扩展名 allowlist。空列表表示未授权上传，不表示允许任意文件。
- 外部 HTTP/WebSocket 访问不使用进程环境代理；DNS 解析结果在连接前逐个校验，并默认拒绝回环、私网、链路本地、元数据、保留地址和非标准端口。只有明确标为本地的网站探测可以访问私网及自定义端口，元数据和链路本地地址仍拒绝。
- Run 由独立 Runner 身份执行，不继承 Web/Broker 身份或进程环境。Runner 只提供固定的最小系统路径、临时目录和 locale，加上 `SCRIPTBOARD_RUN_ID`、`SCRIPTBOARD_SCRIPT_PATH`；代理、云凭据、动态加载钩子和任意服务变量不会进入脚本环境。

后续 ADR-0147、ADR-0149 与 ADR-0163 已完成 Web/Runner/Broker 的操作系统身份拆分；本文继续定义默认拒绝与最小环境合同，不再作为共享服务身份的依据。

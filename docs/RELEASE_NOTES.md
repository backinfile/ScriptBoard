# ScriptBoard v2.4.7

## MCP Agent 接入

- 在主服务的 `/mcp` 提供 Streamable HTTP MCP，并通过标准 OAuth Authorization Code + PKCE 完成本地用户授权，不需要静态 Token。
- MCP 工具按当前用户角色、批准的 Scope、授权版本和 Run 所有权持续校验；文件、源码和系统配置不通过 MCP 暴露。
- 账号设置新增 Agent 连接管理，可查看和撤销已经批准的 OAuth 客户端。

## 快捷执行与并发

- 修复确认重叠执行后仍被底层路径租约拒绝的问题；同一已发布脚本允许显式确认的并发 Run，同时继续阻止运行期间修改脚本。

## Windows 主机文件上传

- 修复受管 Windows 安装中通过特权 Broker 批量上传新文件时，将“目标文件尚不存在”误判为权限或路径检查失败的问题。
- 新增经过 Web、Broker 和主机文件系统完整链路的回归测试，防止普通 Windows 文件上传再次出现同类故障。

## 升级

可从 `v2.0.25` 或更高版本在“系统设置 → 更新”中直接升级。升级前建议备份自定义配置与 ScriptBoard 状态目录。

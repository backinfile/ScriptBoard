# MCP 黑盒、白盒与本地部署验收 — 2026-08-27

## 结论

- 最终外部 Playwright Chromium + HTTP 黑盒套件：55 项通过，0 项失败。
- `go test -count=1 ./...`、MCP 重点包非缓存测试、`go vet`、`go mod verify` 和 `git diff --check` 通过。
- 测试发现并修复 1 个真实缺陷：`confirm_overlap=true` 到达 Run 控制层后，底层独占路径租约仍拒绝第二个 Run。现在同一不可变发布脚本的 Run 使用独立的共享只读租约；文件修改继续被阻止到最后一个 Run 结束。
- Windows 当前 Go 工具链未启用 CGO，因此 `go test -race` 未执行；此项不计为通过。

## 保留部署与证据

- 启用态：`D:\Github\worktrees\ScriptBoard\mcp-streamable-http\.scratch\mcp-validation-2026-08-27`
- URL：`http://127.0.0.1:18791`
- PID：`42012`
- 登录用户：`admin`。初始密码只保留在部署 State Root 的受保护凭据文件中，未复制到脚本、命令输出或报告。
- 结构化结果：`.scratch/mcp-validation-2026-08-27/artifacts/blackbox-results.json`
- 浏览器截图：`oauth-execute-consent.png`、`account-agent-connections-after-revoke.png`
- DCR 客户端、预注册客户端、授权、Quick Run、Run、日志、撤销记录和缺陷复现数据均保留。
- 禁用态：`http://127.0.0.1:18788`，PID `45572`；MCP/OAuth 的 7 个公开路径均返回 404。

## 已执行黑盒测试

| ID | 测试条目 | 结果 |
| --- | --- | --- |
| BB-BASIC-01 | `GET /login` 基础访问 | 200，通过 |
| BB-BASIC-02 | 登录页安全响应头 | CSP 存在，通过 |
| BB-BASIC-03 | 未登录根路径 | 303 到 `/login`，通过 |
| BB-BASIC-04 | 未登录受保护页面 | 303 到 `/login`，通过 |
| BB-NET-01 | 默认监听地址 | 仅 `127.0.0.1:18791`，通过 |
| BB-NET-02 | 不允许的 Host | 421，通过 |
| BB-NET-03 | 点段、重复斜杠、absolute-form 请求目标 | `curl --path-as-is` 均为 400，通过 |
| BB-NET-04 | 未知路径 | 404，通过 |
| BB-NET-05 | `mcp_enabled=false` | `/mcp`、两类发现、authorize/token/register/revoke 共 7 路由均为 404，通过 |
| BB-DISC-01 | Protected Resource Metadata | resource 精确等于 Canonical URL + `/mcp`，通过 |
| BB-DISC-02 | Protected Resource scopes | 仅声明 observe/execute，通过 |
| BB-DISC-03 | Authorization Server Metadata | 仅 PKCE S256，通过 |
| BB-DISC-04 | OAuth grant 类型 | authorization_code/refresh_token；无 client_credentials，通过 |
| BB-AUTH-01 | 无凭据 `POST /mcp` | 真正 HTTP 401，通过 |
| BB-AUTH-02 | 401 发现行为 | `WWW-Authenticate` 含 resource_metadata，正文非 HTML，通过 |
| BB-AUTH-03 | 无效 Bearer | JSON `invalid_token` 401，通过 |
| BB-DCR-01 | 合法 DCR | 201，公开客户端，无 Client Secret，通过 |
| BB-DCR-02 | 非回环明文 redirect URI | 400，通过 |
| BB-DCR-03 | Client Secret 认证元数据 | 400，通过 |
| BB-DCR-04 | client_credentials | 400，通过 |
| BB-DCR-05 | 未知 DCR 字段 | 400，通过 |
| BB-UI-01 | 外部 Chromium 登录 | 成功进入 `/monitor`，通过 |
| BB-UI-02 | 账户 Agent connections | 页面可见，通过 |
| BB-UI-03 | MCP 管理页权限 | Administrator 可访问，通过 |
| BB-UI-04 | 近期登录后预注册客户端 | 创建成功，通过 |
| BB-UI-05 | 浏览器控制台 | 0 error，通过 |
| BB-CONSENT-01 | execute Consent 页面 | 浏览器实际渲染，通过 |
| BB-CONSENT-02 | Consent approve | Session + CSRF 提交后 303 回调，通过 |
| BB-CONSENT-03 | Consent deny | 返回 `access_denied`，不签发 Token，通过 |
| BB-OAUTH-01 | Authorization Code + PKCE | 成功换取 Token，通过 |
| BB-OAUTH-02 | Access Token TTL | `expires_in=600`，通过 |
| BB-OAUTH-03 | execute 自动包含 observe | Scope 正确，通过 |
| BB-OAUTH-04 | Authorization Code 重放 | `invalid_grant`，通过 |
| BB-OAUTH-05 | observe-only Grant | 不包含 execute，通过 |
| BB-REFRESH-01 | Refresh 轮换 | 新 Refresh Token 与旧值不同，通过 |
| BB-REFRESH-02 | 旧 Refresh Token 复用 | `invalid_grant`，通过 |
| BB-REFRESH-03 | Token family 撤销 | 轮换后 Access Token 立即 401，通过 |
| BB-REVOKE-01 | 账户页列出 Agent 授权 | 当前授权可见，通过 |
| BB-REVOKE-02 | 用户撤销自己的 Agent 授权 | 原 Access Token 立即 401，通过 |
| BB-MCP-01 | Bearer `initialize` | 返回 ScriptBoard serverInfo，通过 |
| BB-MCP-02 | execute `tools/list` | 6 个工具，通过 |
| BB-MCP-03 | observe `tools/list` | 4 个只读工具，通过 |
| BB-MCP-04 | 隐藏写工具直接调用 | 通用错误且不回显目标，通过 |
| BB-MCP-05 | `get_host_status` | 返回 structuredContent，通过 |
| BB-MCP-06 | 已认证请求大于 1 MiB | 413，通过 |
| BB-RUN-01 | 浏览器创建并发布测试 Quick Run | 成功并保留，通过 |
| BB-RUN-02 | `list_quick_runs` | 可见目标，但无路径、源码和变量，通过 |
| BB-RUN-03 | `start_quick_run` | 使用 OAuth admin 启动，通过 |
| BB-RUN-04 | 启动 request_id 幂等 | 重复请求返回同一 Run，通过 |
| BB-RUN-05 | 活动脚本冲突 | 返回 `active_run` 和重试提示，通过 |
| BB-RUN-06 | `confirm_overlap=true` | 修复后成功产生第二个 Run，通过 |
| BB-RUN-07 | `stop_run` | 停止指定 Run，通过 |
| BB-RUN-08 | 停止 request_id 幂等 | 重复请求不重复副作用，通过 |
| BB-RUN-09 | `get_run` | 返回 initiator `admin` 快照，通过 |
| BB-RUN-10 | `get_run_logs limit=500` | 服务端裁剪为最多 200 条，通过 |
| BB-RUN-11 | 重叠 Run 结束后的租约生命周期 | 两个 Run 均可停止，部署继续可用，通过 |

## 已执行白盒测试

| ID | 测试面 | 证据与结果 |
| --- | --- | --- |
| WB-ALL-01 | 全仓非缓存测试 | `go test -count=1 ./...`，通过 |
| WB-STATIC-01 | 重点包静态检查 | `go vet ./internal/mcpaccess ./internal/mcpserver ./internal/runcontrol ./internal/web ./internal/config ./internal/bootstrap`，通过 |
| WB-SUPPLY-01 | Go 模块完整性 | `go mod verify`，通过 |
| WB-FORMAT-01 | Patch/空白检查 | `git diff --check`，通过 |
| WB-CONFIG-01 | MCP 默认启用、显式禁用 | `TestLoadMCPEnabledDefaultsTrueAndCanBeDisabled`，通过 |
| WB-NET-01 | 回环派生 Allowed Hosts/Canonical URL | `TestLoadDerivesLoopbackAllowedHostsAndCanonicalURL`，通过 |
| WB-NET-02 | 通配监听必须显式 hosts、Canonical host 绑定 | `TestLoadRequiresExplicitHostsForWildcardListenAndBindsCanonicalHost`，通过 |
| WB-NET-03 | TLS 配置完整性 | `TestValidateNetworkConfigurationRequiresCompleteTLSAndHostPort`，通过 |
| WB-NET-04 | 可信代理与 Host 边界 | Web proxy/allowed-host suites，通过 |
| WB-MCP-01 | 401 challenge | `TestMCPUnauthenticatedRequestAdvertisesOAuthDiscovery`，通过 |
| WB-MCP-02 | Canonical resource | `TestMCPProtectedResourceMetadataUsesCanonicalResource`，通过 |
| WB-MCP-03 | 禁用态不注册路由 | `TestMCPDisabledDoesNotRegisterProtocolRoutes`，通过 |
| WB-MCP-04 | 官方 Go SDK Streamable HTTP 客户端 | `TestToolCatalogueIsFilteredByCurrentScope`，Viewer 4/Operator 6，通过 |
| WB-OAUTH-01 | 登录后恢复 OAuth authorize | `TestOAuthAuthorizationResumesAfterLogin`，通过 |
| WB-OAUTH-02 | Code 一次性、PKCE、Token hash-only、Refresh 轮换/复用 | `TestAuthorizationCodeAndRefreshRotation`，通过 |
| WB-OAUTH-03 | redirect URI 精确匹配和回环端口变化 | `TestRedirectURIMatching`，通过 |
| WB-OAUTH-04 | 用户 auth_version 即时失效 | `TestCurrentUserStateInvalidatesAccessToken`，通过 |
| WB-CIMD-01 | 私网/回环地址拒绝 | `TestCIMDRejectsNonPublicAddresses`，通过 |
| WB-RUN-01 | 发布摘要变化拒绝 | `TestStartRejectsScriptThatNoLongerMatchesPublishedDigest`，通过 |
| WB-RUN-02 | 禁止重叠检查串行化 | `TestStartSerializesPublishedRunOverlapCheck`，通过 |
| WB-RUN-03 | 相同发布脚本共享 Run 只读租约 | `TestRunLeasesShareAnExactPublicationAndStillBlockMutations`，通过 |
| WB-RUN-04 | Run 目录包含关系仍冲突 | `TestRunLeasesDoNotShareContainingPaths`，通过 |
| WB-MIGRATION-01 | 数据库迁移兼容性 | migrations package，全通过 |

## 完整测试目录中尚未做本轮真实黑盒的条目

这些项目仍属于发布前测试目录。它们没有被本轮 55 项结果掩盖或误报为通过。

| 优先级 | ID | 条目 | 当前覆盖/后续方式 |
| --- | --- | --- | --- |
| P0 | GAP-AUTH-01 | Viewer、Operator、Maintainer、Administrator 四角色真实账户矩阵 | Viewer/Operator 工具目录有 SDK 白盒；仍需四个真实用户验证 Scope、启动和停止所有权 |
| P0 | GAP-AUTH-02 | 用户停用、改角色、改密码后 Access/Refresh 立即失效 | auth_version 白盒已过；仍需真实 Session 管理操作和 Token 复验 |
| P0 | GAP-RUN-01 | Operator 只能停止自己的 Run，Maintainer/Admin 可停止任意 Run | 控制器代码存在；需多用户黑盒 |
| P0 | GAP-SECRET-01 | 日志中注入真实秘密后的 MCP 脱敏、256 KiB 总响应上限 | 200 条上限已黑盒；需创建秘密变量和大日志专门数据集 |
| P0 | GAP-CIMD-01 | CIMD 公网 HTTPS 获取、内容类型/大小/超时、DNS 重绑定、禁止重定向逃逸 | 私网 IP 白盒已过；需受控公网 HTTPS fixture |
| P0 | GAP-LIMIT-01 | MCP、DCR、Token 端点 429、Retry-After 和并发上限 | 本轮未压测；应增加可注入时钟的 limiter 单测和本地并发测试 |
| P1 | GAP-TTL-01 | Code 5 分钟、Access 10 分钟、Refresh 30 天绝对期限的边界时刻 | Access `expires_in` 已黑盒；其余需可控时钟单测，避免真实等待 30 天 |
| P1 | GAP-OAUTH-01 | 错误 verifier、错误 resource/audience、错误 client、错误 redirect 的完整交换矩阵 | redirect 和合法 PKCE 已覆盖；需表驱动 Store/HTTP 测试 |
| P1 | GAP-REVOKE-01 | 管理员撤销 Client/任意 Authorization、revocation endpoint 的 Access/Refresh 矩阵 | 用户自撤已黑盒；其余需补 |
| P1 | GAP-RUN-02 | 脚本发布后被篡改、变量不可用、工作目录不可用、取消请求、30 秒 MCP 超时 | 摘要变化有白盒；其余需故障注入 |
| P1 | GAP-RUN-03 | 日志稳定 cursor、跨页无重复/遗漏、截断标志、Run 终态重复停止 | 单页上限已黑盒；需大日志分页数据集 |
| P1 | GAP-NET-01 | 显式非回环明文 HTTP 的真实网卡访问 | 未开放开发机 LAN；配置白盒通过，发布环境用隔离网段验证并确认风险提示 |
| P1 | GAP-NET-02 | 真实 TLS、跳过证书验证选择、可信 HTTPS 反代 | 本轮为回环 HTTP；已有 TLS/代理基础套件，仍需部署矩阵 |
| P1 | GAP-CLIENT-01 | CIMD、DCR、预注册 Client ID 优先级冲突 | DCR/预注册分别通过；优先级冲突需集成测试 |
| P1 | GAP-AUDIT-01 | OAuth 全生命周期、拒绝、撤销、写工具审计链；正文不含 Token/Code | 数据已产生并保留；需数据库和审计链专门断言 |
| P2 | GAP-COMPAT-01 | 真实 Codex、MCP Inspector、另一 DCR 客户端 | 官方 Go SDK 和原始 HTTP 已通过；客户端产品兼容仍需单独执行 |
| P2 | GAP-RACE-01 | Go race detector | 当前 Windows Go 环境无 CGO；在 CI 的 CGO runner 执行 `go test -race` |

## 复现命令

```powershell
go test -count=1 ./...
go vet ./internal/mcpaccess ./internal/mcpserver ./internal/runcontrol ./internal/web ./internal/config ./internal/bootstrap
go mod verify

Set-Location .scratch/mcp-validation-2026-08-27
node .\mcp-blackbox.js http://127.0.0.1:18791 .\state .\artifacts
```

黑盒脚本从 State Root 读取初始凭据，只在进程内保存密码、Cookie、Authorization Code 和 Token；结果 JSON 与截图不保存这些值。

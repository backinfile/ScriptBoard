# 页面嵌入设置本地验收 · 2026-09-09

## 结果

通过。设置 → 页面嵌入支持禁止嵌入、指定地址和全部地址。保存后立即生效，重启后保留并优先于启动配置。首次保存前继承启动参数。README 已更新。

## 测试范围与结果

| 项目 | 结果 |
| --- | --- |
| 登录、概览、账户、站点名称、新设置页基础访问 | 通过 |
| 三种模式保存、回显、CSP 与 X-Frame-Options 即时更新 | 通过 |
| 指定 HTTP 与 HTTPS 来源；显式星号配置 | 通过 |
| 缺少 CSRF、跨来源写入、CSP 注入及无效模式 | 正确拒绝 |
| 观察员禁止访问；维护员可访问 | 通过 |
| 重启保留全部地址，覆盖指定来源启动配置 | 通过 |
| 外部 Edge 中真实 HTTP iframe 登录 | 通过 |
| 默认、指定、全部模式的 HTTP/HTTPS 登录、语言 Cookie 与嵌套页签 | Go 集成测试通过 |
| Schema 69 → 70、无设置时继承、设置及站点名称保留 | 通过 |
| 中英文表单、1440px 桌面与 390px 手机布局 | 通过，已查看截图 |

## 自动检查

- `go test ./internal/config ./internal/store/... ./internal/web ./internal/bootstrap ./cmd/scriptboard -count=1` 通过。
- `go vet ./internal/config ./internal/store/... ./internal/web ./internal/bootstrap ./cmd/scriptboard` 通过。
- `go build -o .scratch/embedding-local/scriptboard.exe ./cmd/scriptboard` 通过。
- 外部 Microsoft Edge（Playwright，headless）：26 项专项检查通过；追加重启、真实 iframe 登录、中英文检查通过。
- `git diff --check` 通过。未发布安装包或运行远程 CI。

## 保留部署

- 地址：http://127.0.0.1:6436/settings/embedding
- 用户名：`admin`
- 密码：`Embedding-Settings-Test-2026!`
- 目录：`C:/Users/17575/AppData/Local/Temp/scriptboard-embedding-settings-20260909`
- 保留管理员、测试观察员和维护员、审计及全部地址设置。
- 目录中包含 `browser-results.json`、`restart-results.txt`、桌面和手机截图、测试脚本、Go 测试日志与服务日志。

跨站 HTTPS Cookie 由 Go TLS 集成测试覆盖；本次真实浏览器 iframe 登录使用同站 HTTP。浏览器禁用第三方 Cookie 或 HTTPS 父页嵌入 HTTP 时，仍受浏览器限制。

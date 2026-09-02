# 上次发布后 Review 修复与简化本地部署测试报告

测试时间：2026-09-02（Asia/Shanghai）

测试分支：`codex/post-release-review-simplifications`

修复提交：`b33edfd959be99de3a7f2d8fcf33da47aa9b20cd`

## 结论

- Review 发现的批量 ZIP 截断成功、快捷访问首次点击语义、重复路径验证、MySQL Broker 查询 interface 透传和备份制品校验重复均已修复或收敛。
- 全仓 Go 测试、静态检查、完整外部 Chromium 门禁和真实本地部署黑盒验收全部通过。
- 最终部署监听 `127.0.0.1:18947`，进程、State Root、探针结果和测试数据均保留。

## 测试条目

1. 登录页、匿名受保护路由跳转、未知路由和管理员认证。
2. 已认证 Host Files、Databases 和静态资源基础访问。
3. 两个真实文件的批量 ZIP 下载、Content-Length 一致性、ZIP 可读性和条目名称。
4. ZIP destination writer 失败能返回错误，不再静默记录成功。
5. 快捷访问链接在验证期间保留真实 `href`，折叠/展开重新渲染只产生一次验证请求。
6. 验证完成前的普通点击能够继续导航，浏览器 console error 和 page error 均为 0。
7. Broker 在构造 seam 一次性要求完整 MySQL `Backend + QueryBackend`，查询方法直接提升，无运行时转发判断。
8. 本地与 Broker adapter 共用 regular-file、symlink、SHA-256 和 gzip SQL 制品检查。
9. 全仓 Go、Vet、浏览器 contracts、Chromium desktop gate 和差异格式检查。

## 自动化验证

| 项目 | 结果 |
| --- | --- |
| `go test ./...` | 通过，全部包无失败 |
| `go vet ./...` | 通过 |
| `npm test`（`integration/browser`） | 通过，全部 contracts 与 Chromium desktop gate 通过 |
| `TestWriteBatchArchiveReturnsDestinationFailure` | 通过，destination 写失败被返回 |
| `TestBrokerMySQLServicePromotesCombinedBackend` | 通过，四种查询能力由组合 interface 直接提升 |
| Broker artifact symlink / non-regular tests | 通过 |
| `git diff --check` | 通过 |

## 本地部署 HTTP 验收

| 编号 | 测试项 | 结果 |
| --- | --- | --- |
| H01 | `GET /login` | HTTP 200 |
| H02 | 匿名 `GET /resources/files` | HTTP 303，跳转 `/login` |
| H03 | 未知路由 | HTTP 404 |
| H04 | 使用新部署生成的管理员密码登录 | HTTP 200，进入 `/monitor` |
| H05 | 已认证 Host Files | HTTP 200 |
| H06 | 已认证 Databases | HTTP 200 |
| H07 | `app-v2.js` | HTTP 200，包含验证缓存实现 |
| H08 | 两文件批量下载 | HTTP 200；响应与文件长度均为 311 字节 |
| H09 | ZIP 完整性 | 通过，包含 `batch-a.txt`、`batch-b.txt` |

结构化结果保存在 `.scratch/post-release-review-simplifications-20260902/http-results.json`。

## 外部 Chromium 验收

部署态验证使用仓库外置 Playwright Chromium，没有使用应用内浏览器。

| 测试项 | 结果 |
| --- | --- |
| 管理员登录与文件页访问 | 通过 |
| 固定真实测试目录到快捷访问 | 通过 |
| 验证期间链接保留 `href` | 通过 |
| 折叠、展开后的验证请求数 | 1 |
| 首次点击完成导航 | 通过 |
| Console errors | 0 |
| Page errors | 0 |

结构化结果保存在 `.scratch/post-release-review-simplifications-20260902/browser-results.json`。

## 保留部署

| 项目 | 值 |
| --- | --- |
| 模式 | Windows 便携单进程部署 |
| URL | `http://127.0.0.1:18947` |
| PID | `30120` |
| 用户名 | `admin` |
| 初始密码 | State Root 私有文件 `secrets/initial-admin-password` |
| 部署目录 | `.scratch/post-release-review-simplifications-20260902` |
| State Root | `.scratch/post-release-review-simplifications-20260902/state` |
| 测试数据 | `D:\ScriptBoardLocalTests\post-release-review-simplifications-20260902` |
| stderr | 0 字节 |

Windows 便携模式不经过独立 Broker；实际 MySQL Broker interface seam 与制品检查由编译期 interface、定向 Broker 测试和全仓测试覆盖。部署进程保持运行。

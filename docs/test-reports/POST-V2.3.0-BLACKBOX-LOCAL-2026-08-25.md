# v2.3.0 之后修改的本地黑盒测试报告

## 结论

当前 `dev` 提交 `72e3be3` 已在保留部署上完成 HTTP 与外部 Playwright Chromium 逐项验证。基础访问、AI 退役、数据库统一工作台、MySQL/MariaDB SQL 安全、Redis 键空间、自定义页签、快捷执行排序、自定义面板页签、变量版本递增、Tooltip、概览布局、文本预览、页面标题与导入导出图标均通过。

两项未宣告完整通过：宿主没有 `mysqldump`/`mariadb-dump`，因此没有重新执行真实备份/恢复成功链路；SQL 结果类型未另建最大 BLOB/bit 极值夹具。它们在本报告中标为环境受限或部分覆盖。

## 测试环境

| 项目 | 值 |
| --- | --- |
| 对比基线 | `v2.3.0` |
| 测试提交 | `72e3be3` |
| ScriptBoard | `http://127.0.0.1:18788`，PID `27992` |
| State Root | `D:\Github\worktrees\ScriptBoard\database-unified\.scratch\database-unified-deployment\state` |
| 外部页签目标站 | HTTP `127.0.0.1:18880`、HTTPS `127.0.0.1:18881`，PID `43024` |
| 浏览器 | Playwright 1.61.1 / Chromium，外部 headless 进程 |
| 数据库容器 | 7 个：MySQL/MariaDB 明文/TLS，Redis 明文/鉴权/TLS |
| 其他容器 | k3d 3 个；合计 10 个容器持续运行 |
| 审计 | 1,093 条事件；哈希链与外部签名 checkpoint 有效 |

## 执行套件

| 套件 | 结果 | 覆盖摘要 |
| --- | --- | --- |
| `database-retained-matrix-playwright.cjs` | 通过 | 19 个混排连接、4 个 SQL 连接、每库 250 行、6 种 Redis 类型、局部导航、失败重试、响应式 |
| `database-partial-navigation-playwright.cjs` | 通过 | 连接区、MySQL/Redis 详情、SCAN、对象筛选、SQL、连接测试及移动端均只更新所属区域 |
| `redis-double-colon-playwright.cjs` | 通过 | 10 个 Redis 连接、`::` 分组、单冒号未分组、折叠、值预览、移动端 |
| `mysql-write-mode-dialog-playwright.cjs` | 通过 | 取消、错误密码、step-up 成功、写模式、实际写入、页面保持与移动端 |
| `mysql-query-settings-drawer-playwright.cjs` | 通过 | 打开/取消/范围校验/摘要/服务端限制/无 JS/移动端 |
| `dashboard-tab-and-mysql-height-playwright.cjs` | 通过 | 面板默认关闭、开启和持久化；MySQL 七页签高度均为 846px |
| `quick-run-group-ordering-playwright.cjs` | 通过 | 卡片尺寸、鼠标、键盘、分组置顶、移动端和持久化 |
| `transfer-icons-playwright.cjs` | 通过 | 自定义面板、网站、MySQL、Kubernetes、服务日志导入导出图标 |
| `release-blackbox.cjs` | 通过 | 基础访问、AI 退役、自定义页签三模式、Viewer 权限、变量、Tooltip、概览、预览和标题 |
| `http-negative.cjs` | 通过 | CSRF 与非法 MySQL/Redis 输入负例 |
| `sql-security-http.cjs` | 通过 | 7 类允许查询、13 类绕过拦截、超时恢复和数据完整性 |
| `integration/browser npm test` | 通过 | 完整 Chromium desktop gate 及自定义页签、Tooltip、弹窗、键盘、失败隔离等契约 |

完整浏览器门禁生成的跟踪截图差异在验证后恢复，没有把测试运行产生的快照改动留在工作区。

## 基础访问、身份与 AI 退役

| 条目 | 结果 |
| --- | --- |
| 登录页、用户名/密码字段、CSP | 通过 |
| 匿名访问受保护页面 303 至登录页 | 通过 |
| 管理员真实登录 | 通过 |
| 登录页中英文冗余文案移除 | 通过 |
| `/ai`、`/settings/ai`、对话、事件与旧静态资源 | 通过，均为 404 |
| 监控、账户、服务日志、数据库、快捷执行、更新页 | 通过，均为 200 且不渲染 AI 导航 |
| Viewer 访问自定义页签管理页 | 通过，403 |
| Viewer 枚举 Key 页签 | 通过，404 |
| Viewer 打开有可见权限的 isolated 页签 | 通过，200 |

## 数据库统一工作台与连接矩阵

- 19 个 MySQL/MariaDB/Redis 连接按同一连接栏混排、分页，四类 SQL Docker 连接和 Redis 连接均可用。
- MySQL/MariaDB 明文与 TLS 均完成对象列表、字段、索引、200 行预览、发送 SQL 和查询。
- Redis 保留明文、鉴权、TLS 与显式跳过验证连接表达；主矩阵读取 6 种值类型。
- MySQL、Redis 创建请求无 CSRF 均返回 403。
- MySQL 端口 `70000` 返回 400；Redis DB `-1` 返回 400，均未保存。
- SQL 请求无 CSRF 返回 403。
- 数据库连接、分页、内部页签、对象筛选、SQL、SCAN、连接测试与网络错误重试仅替换所属工作区。
- 1920、1440、430、390px 无页面级横向溢出；注入网络失败后旧内容保留且重试成功。

## SQL 安全逐项结果

允许并成功返回结果的 7 类语句：

1. `SELECT`
2. `SHOW`
3. `DESC`
4. `DESCRIBE`
5. `EXPLAIN`
6. 只读 `WITH ... SELECT`
7. 字符串内包含分号的 `SELECT`

被后端拒绝且未产生写入的 13 类候选：

1. `INSERT`
2. `UPDATE`
3. `DELETE`
4. `CREATE TABLE`
5. `ALTER TABLE`
6. `DROP TABLE`
7. `TRUNCATE TABLE`
8. `/* ... */ INSERT` 注释绕过
9. `-- ...` 后 `DELETE` 绕过
10. `# ...` 后 `UPDATE` 绕过
11. `WITH ... DELETE` 写 CTE
12. `SELECT ...; DROP ...` 多语句
13. `CALL`

1 秒 `SLEEP` 查询按时中止；随后 `SELECT COUNT(*)` 返回 250，连接恢复、表和数据均完整。查询设置抽屉的 25 行限制、截断、取消恢复、非法范围、摘要更新、无 JavaScript提交均通过。

写模式弹窗覆盖取消、Escape、错误密码、正确 step-up、Safe Updates 提示、实际更新和后端写路由。保留写入结果为 `scriptboard_qa.widgets.id=2 / status=write-dialog-retained`。

## Redis 键空间

| 条目 | 结果 |
| --- | --- |
| 空/通配 SCAN 与特殊字符 | 通过 |
| `namespace::key` 双冒号分组 | 通过 |
| 单冒号、无分隔符未分组 | 通过 |
| String/Hash/List/Set/ZSet/Stream | 通过 |
| TTL 与永久键 | 通过 |
| 分组折叠、值检查器、前后导航 | 通过 |
| 中文、空格和编码键 | 通过 |
| 移动端无横向溢出 | 通过 |

旧 `redis-values-playwright.cjs` 仍只查找已经退役的 `qa:*` 单冒号夹具，因此单独运行时未找到数据。等价且更完整的 `sb_partial_nav::*` 六类型用例已由主矩阵和双冒号探针通过，该旧探针不计作产品失败。

## 自定义页签

- 实际创建并保留 HTTP isolated、HTTPS target_state、HTTPS key 三类页签。
- `file://` 非法 URL 返回 422。
- 启停后“外部”导航即时更新；排序请求实际改变并持久化顺序。
- 创建/编辑抽屉覆盖完整视口，不受应用工作区变换裁剪。
- isolated iframe 不含 `allow-same-origin`。
- target_state iframe 包含 `allow-same-origin`，读取到目标站自己的 `sb_target_state=retained` Cookie。
- Key 通过正确 iframe、Origin 和 nonce 交付一次，值为目标页实际接收；ScriptBoard HTML 不含 Key。
- 伪造/重放 delivery 请求返回 403/404，未得到凭据。
- iframe 响应 CSP 只放行对应 `http://127.0.0.1:18880` 或 `https://127.0.0.1:18881` Origin。
- Viewer 不能管理或枚举 Key 页签，但能打开授权的 isolated 页签。
- 自定义页签外部 HTTP/HTTPS 目标站在报告生成后保持运行。

最终成功样本：`HTTP 隔离 mt8habav`、`HTTPS 状态 mt8habav`、`HTTPS Key mt8habav`。前几轮修正测试断言时创建的样本也按仓库规则保留。

## 其他 UI 与操作功能

| 条目 | 结果 |
| --- | --- |
| 快捷执行单组排序入口 | 通过 |
| 鼠标拖动与键盘方向键持久化 | 通过 |
| 卡片内容和尺寸稳定 | 通过 |
| 分组一次性置顶及相对顺序 | 通过 |
| 自定义面板默认不显示为页签 | 通过 |
| 开启、刷新持久化及关闭后管理 | 通过 |
| MySQL 七页签高度和间距 | 通过；846px、最大间距 26px |
| 版本变量 Patch→Minor→Major | 通过；`4.0.0` 最终为 `5.0.0`，revision `v4` |
| 非版本变量不显示递增入口 | 通过 |
| 变量递增无 CSRF | 通过，403 |
| 复制 Tooltip Top Layer、hover、复制反馈 | 通过 |
| 主机详情两张宽表上下等宽 | 通过，桌面/390px 均无页面溢出 |
| PowerShell 文本预览背景、正文、字号和行高 | 通过 |
| 十个一级页面统一标题 | 通过 |
| 外部日志五列结构 | 通过 |
| 业务导入/导出 Lucide 图标 | 通过 |
| 浏览器控制台和 page error | 通过，非预期错误 0 |

## 环境受限和剩余项

1. 本机不存在 `mysqldump` 或 `mariadb-dump`，真实备份、恢复、批量备份、导入成功链路未重新执行；请求编排和失败历史已有现有测试覆盖。
2. SQL 动态列、NULL、整数、文本、时间和 JSON schema 已覆盖；未另建最大 BLOB/bit 极值数据。
3. HTTPS 自定义页签使用本地测试 CA，Playwright 上下文显式忽略该测试证书的信任错误；产品没有替用户跳过目标站证书验证。

## 保留状态

- ScriptBoard PID `27992` 继续监听 `127.0.0.1:18788`。
- 自定义页签目标站 PID `43024` 继续监听 `18880/18881`。
- 7 个数据库容器、3 个 k3d 容器持续运行。
- 新建 Viewer、变量、自定义页签、自定义面板、快捷执行排序和数据库写入测试数据全部保留。
- 测试结果位于 `.scratch/release-72e3be3-blackbox/`，数据库截图和探针位于 `.scratch/database-unified-dev-deployment/`。

# 数据库 Docker 矩阵本地测试报告

测试日期：2026-08-25（Asia/Shanghai）

## 结论

预先列出的 49 项测试中，48 项通过，1 项因宿主缺少 `mysql`/`mysqldump` 客户端而环境受限。环境受限项的 HTTP 受理、失败状态落库与全部白盒路径均已验证，不属于连接功能失败。

本轮发现并修复了三个问题：Redis 连接新增/测试/删除缺少 CSRF 校验；MariaDB TLS 会话的状态探测误报为未加密；数据库抽屉关闭时引用了助手页面的局部变量并在浏览器控制台抛错。修复后已重新部署并完成全量回归。

## 保留的部署与数据

| 项目 | 当前状态 |
| --- | --- |
| ScriptBoard | `http://127.0.0.1:18788`，PID 30728，保持运行 |
| State Root | `D:\Github\worktrees\ScriptBoard\database-unified\.scratch\database-unified-deployment\state` |
| 管理员 | `admin`；密码仅保留在 State Root 私有文件中 |
| Docker 项目 | `scriptboard-database-qa`，7 个容器均保持运行 |
| 测试定义与证书 | `.scratch/database-docker-matrix/` |
| Playwright 截图 | `.scratch/database-docker-matrix/database-desktop.png`、`database-mobile.png` |
| 数据持久化 | 7 个 `sb_qa_*` 命名卷全部保留 |

| 服务 | 镜像 | 端口 | 模式 |
| --- | --- | --- | --- |
| `sb-qa-mysql-plain` | `mysql:8.4` | 23306 | 明文 |
| `sb-qa-mysql-tls` | `mysql:8.4` | 23307 | TLS，强制安全传输 |
| `sb-qa-mariadb-plain` | `mariadb:11.8` | 23308 | 明文 |
| `sb-qa-mariadb-tls` | `mariadb:11.8` | 23309 | TLS，强制安全传输 |
| `sb-qa-redis-auth` | `redis:8.2` | 26379 | 明文，密码鉴权 |
| `sb-qa-redis-noauth` | `redis:8.2` | 26380 | 明文，无鉴权 |
| `sb-qa-redis-tls` | `redis:8.2` | 26381 | TLS，密码鉴权 |

关系库均保留 `scriptboard_qa.widgets` 及两行种子数据；另保留 `sb_http_mysql_retained`、`sb_http_mariadb_retained`。Redis 保留 String、Hash、List、Set、ZSet 和 TTL 键。ScriptBoard 中保留成功连接、错误密码、错误 CA、不可达端口、备份计划及失败操作历史。修复前由 CSRF 负例创建的证据连接也按“保留测试数据”要求保留，修复后同一请求返回 403 且不再创建新连接。

## 黑盒测试（1–38）

| # | 测试条目 | 结果 | 证据摘要 |
| --- | --- | --- | --- |
| 1 | Docker 环境、网络、卷、端口 | 通过 | Compose 5.1.4；7 容器、7 命名卷；端口无冲突 |
| 2 | MySQL 明文实例 | 通过 | 8.4.11；禁用 TLS 后连接并读取 2 行种子数据 |
| 3 | MySQL TLS 与自定义 CA | 通过 | `verify_identity`；`TLS_AES_128_GCM_SHA256` |
| 4 | MariaDB 明文实例 | 通过 | 11.8.9；明文连接并读取 2 行种子数据 |
| 5 | MariaDB TLS 与自定义 CA | 通过 | `verify_identity`；`TLS_AES_128_GCM_SHA256` |
| 6 | Redis 明文密码鉴权 | 通过 | Redis 8.2.9；PING/INFO/SCAN 成功 |
| 7 | Redis 明文无鉴权 | 通过 | Redis 8.2.9；空密码连接成功 |
| 8 | Redis TLS 验证、跳过验证、错误 CA | 通过 | 验证与显式跳过均成功；独立错误 CA 返回 502 |
| 9 | 登录、导航、数据库页基础访问 | 通过 | 登录后进入 `/monitor`；数据库页 HTTP 200 |
| 10 | MySQL 与 Redis 融合连接栏 | 通过 | 同一列表混合排列两类连接，并按连接名称排序、分页 |
| 11 | 单一新增入口可选引擎 | 通过 | MySQL/MariaDB 与 Redis 选择器均存在 |
| 12 | MySQL/MariaDB 表单安全选项与提示 | 通过 | 明文、preferred、required、身份验证及 CA 路径齐全 |
| 13 | Redis 表单安全选项与风险提示 | 通过 | 明文、身份验证、显式跳过验证及中间人风险提示齐全 |
| 14 | 新建 MySQL 明文连接 | 通过 | HTTP 新建、测试与状态读取成功 |
| 15 | 新建 MySQL TLS 连接 | 通过 | HTTP 新建；测试返回 `TLS:true` |
| 16 | 新建 MariaDB 明文连接 | 通过 | HTTP 新建、测试与状态读取成功 |
| 17 | 新建 MariaDB TLS 连接 | 通过 | HTTP 新建；兼容修复后返回 `TLS:true` |
| 18 | 新建 Redis 明文鉴权连接 | 通过 | HTTP 新建与测试成功 |
| 19 | 新建 Redis 明文无鉴权连接 | 通过 | HTTP 新建与测试成功 |
| 20 | 新建 Redis TLS 身份验证连接 | 通过 | HTTP 新建与测试成功 |
| 21 | 新建 Redis TLS 跳过验证连接 | 通过 | 用户选择被保留，测试成功 |
| 22 | MySQL/MariaDB 连接测试、状态、版本 | 通过 | 四实例分别返回正确产品、版本、能力与 TLS 状态 |
| 23 | Redis 连接测试、状态、版本 | 通过 | 四种连接配置均返回 Redis 8.2.9 |
| 24 | MySQL/MariaDB 数据库列表 | 通过 | `scriptboard_qa` 与 HTTP 创建库可见 |
| 25 | 创建数据库及重复创建失败 | 通过 | MySQL/MariaDB 首次成功，重复请求均返回 400 |
| 26 | 字符集与排序规则显示 | 通过 | `utf8mb4` 及对应 MySQL/MariaDB collation 可见 |
| 27 | 备份、批量、计划、恢复、导入、删除、取消、历史 | 环境受限 | 计划保存成功；单库/批量请求被受理并留下 `failed` 历史。宿主无客户端，真实成功链路未执行；恢复/导入/取消/安全删除由白盒覆盖，删除负例 400 且数据保留 |
| 28 | Redis 概览 | 通过 | 四连接概览 HTTP 200，版本/键数/内存可读 |
| 29 | Redis SCAN、模式、类型、TTL、大小 | 通过 | `qa:*` 查询命中保留键，各类种子数据可见 |
| 30 | Redis 诊断页 | 通过 | 四连接诊断页 HTTP 200，安全模式可见 |
| 31 | 不可达、错误密码、错误 CA | 通过 | MySQL/Redis 错误密码 502；不可达 502；错误 CA 502 |
| 32 | 凭据保护 | 通过 | 页面不回显任一数据库密码或故障密码 |
| 33 | CSRF、权限、step-up | 通过 | MySQL/Redis 无令牌均 403；权限与 step-up 全仓测试通过 |
| 34 | 非法输入 | 通过 | 越界端口、负 Redis DB 均返回 400 |
| 35 | 中英文 | 通过 | Playwright 中文 `lang=zh-CN` 与英文页面通过 |
| 36 | 禁用 JavaScript 渐进增强 | 通过 | 禁用 JS 后登录、混合连接列表和 Redis 表单均可用 |
| 37 | 响应式、键盘、控制台 | 通过 | 1440×1000 与 390×844 无横向溢出；Esc 关闭抽屉；控制台错误为 0 |
| 38 | 保留部署与测试数据 | 通过 | 容器、卷、State Root、连接、数据库、键、计划、操作历史和截图均保留 |

## 白盒测试（39–49）

| # | 测试条目 | 结果 | 证据摘要 |
| --- | --- | --- | --- |
| 39 | `mysqlmanager` | 通过 | 连接、TLS、数据库、备份/恢复/计划/操作与兼容逻辑测试通过 |
| 40 | `redismanager` | 通过 | 保存、凭据、TLS、SCAN、状态与错误处理测试通过 |
| 41 | Web handlers | 通过 | MySQL/Redis 路由、表单、状态码、重定向与新增 CSRF 回归通过 |
| 42 | 模板、CSS、JavaScript 契约 | 通过 | 融合模板、局部刷新、抽屉、连接弹窗、响应式与键盘契约通过 |
| 43 | Privilege Broker 边界 | 通过 | `internal/privilegebroker` 及 Web broker 集成测试通过 |
| 44 | Schema 与 migrations | 通过 | SQLite store/migrations 及数据库资源迁移测试通过 |
| 45 | 安全 | 通过 | CSRF、权限、凭据隐藏、错误脱敏、确认流程测试通过 |
| 46 | 连接模式矩阵 | 通过 | MySQL/MariaDB/Redis 的明文、安全、身份验证及显式跳过验证均覆盖 |
| 47 | 目标包回归 | 通过 | `go test ./internal/web ./internal/redismanager ./internal/mysqlmanager -count=1` |
| 48 | 全仓回归 | 通过 | `go test ./... -count=1`，所有包通过 |
| 49 | 格式、差异、部署版本与运行状态 | 通过 | `gofmt`、`git diff --check`、重新构建部署、监听与容器核验通过 |

## 混合连接页签补充验收

- MySQL 与 Redis 不再按引擎拆成两个分组，统一进入一个按连接名称排序、可分页的连接页签列表。
- MySQL 使用 Lucide `database` 图标；Redis 使用 Lucide `memory-stick` 图标，并辅以短类型标签。
- Redis 详情使用与 MySQL 相同的 `.mysql-tabs` 内部页签，在概览、键空间查询和诊断之间切换。
- MySQL 与 Redis 详情统一为“详情头部、内部页签、活动内容面板”框架；Redis 页签补齐图标、滚动保持、连接列表页码保持和概览刷新入口。
- 真实保留数据下的 Playwright 桌面、移动端、键盘、中文、禁用 JavaScript 和控制台检查全部通过。

## 关键命令与限制

- 黑盒：PowerShell HTTP 会话、Docker 容器内客户端、Playwright Chromium。
- 白盒：目标包测试与两次全仓 `go test ./... -count=1`，最终一次在所有修复后执行。
- 未运行仓库的完整浏览器快照刷新套件，以免改写无关的跟踪截图；本轮使用独立的本地部署 Playwright 脚本与 `.scratch` 截图。
- `npm ci` 报告一个测试依赖树中的 moderate audit 提示；未执行会升级依赖并扩大修改范围的 `npm audit fix --force`。

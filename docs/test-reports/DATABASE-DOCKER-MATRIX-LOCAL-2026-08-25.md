# 数据库 Docker 矩阵本地测试报告

测试日期：2026-08-25（Asia/Shanghai）

## MySQL/MariaDB 对象浏览与 SQL 控制台预执行清单（部署前快照）

> 以下“待测”状态保留功能部署前固化的原始快照；部署后的逐项结论记录在本清单后方，避免事后改写测试范围。

### 黑盒测试（SQL-B01–SQL-B32）

| # | 测试条目 | 预期结果 | 状态 |
| --- | --- | --- | --- |
| SQL-B01 | MySQL 明文连接打开“对象/表”页签 | 页面 200，显示当前连接、当前数据库和对象树 | 待测 |
| SQL-B02 | MySQL TLS 连接打开“对象/表”页签 | TLS 连接下功能与明文一致 | 待测 |
| SQL-B03 | MariaDB 明文/TLS 连接打开对象页 | 两种模式均正确识别 MariaDB 并列表 | 待测 |
| SQL-B04 | 数据库选择与表/视图列表 | `scriptboard_qa.widgets` 可见，表与视图类型可区分 | 待测 |
| SQL-B05 | 系统库默认状态 | `mysql`/`information_schema`/`performance_schema`/`sys` 默认隐藏或折叠 | 待测 |
| SQL-B06 | 显式展开系统库 | 四个系统库可查看，选择状态可保持 | 待测 |
| SQL-B07 | `widgets` 字段结构 | 显示字段名、完整类型、可空、默认值、自增和主键 | 待测 |
| SQL-B08 | `widgets` 索引结构 | PRIMARY 索引的名称、唯一性、列和顺序正确 | 待测 |
| SQL-B09 | “查看前 200 行” | 跳转 SQL 结果并最多返回 200 行，不修改数据 | 待测 |
| SQL-B10 | “发送到 SQL 编辑器” | 编辑器填入安全引用的库名/表名，不自动执行 | 待测 |
| SQL-B11 | 只读 `SELECT` 单条执行 | 显示列名、2 行种子数据、返回行数和耗时 | 待测 |
| SQL-B12 | 只读 `SHOW`/`DESC`/`DESCRIBE`/`EXPLAIN` | 各语句均执行成功且结果列完整 | 待测 |
| SQL-B13 | 无副作用 `WITH ... SELECT` | CTE 成功执行并返回预期结果 | 待测 |
| SQL-B14 | 只读模式写入/DDL 拦截 | INSERT/UPDATE/DELETE/CREATE/ALTER/DROP/TRUNCATE 均由后端拒绝 | 待测 |
| SQL-B15 | 注释绕过 | `/*x*/INSERT`、`--x\nDELETE`、`#x\nUPDATE` 均被拒绝 | 待测 |
| SQL-B16 | 大小写/空白绕过 | 混合大小写、BOM、前导空白不改变分类结果 | 待测 |
| SQL-B17 | CTE 写入绕过 | `WITH ... DELETE/UPDATE/INSERT` 以及隐藏写 CTE 均被拒绝 | 待测 |
| SQL-B18 | 多语句绕过 | `SELECT 1; DROP ...`、注释后第二语句、多分隔符均被拒绝 | 待测 |
| SQL-B19 | 字符串/标识符内分号 | `SELECT ';'`和反引号标识符不被误判为多语句 | 待测 |
| SQL-B20 | 存储过程、`CALL`、`DELIMITER`、导入脚本 | 第一版全部拒绝，无任何部分执行 | 待测 |
| SQL-B21 | 结果上限 | 超过最大行数时截断，页面显示截断标识和实际返回行数 | 待测 |
| SQL-B22 | 超时 | 长查询在配置超时后中止，连接可继续使用 | 待测 |
| SQL-B23 | SQL 错误显示与脱敏 | 显示可操作的错误，不包含密码、DSN、CA 路径或完整服务端敏感信息 | 待测 |
| SQL-B24 | 无 CSRF 令牌执行 SQL/切换模式 | 请求 403，数据与模式不变 | 待测 |
| SQL-B25 | 维护者/管理员访问与无权用户 | 仅持有现有数据库权限的用户可访问，其他角色被拒绝 | 待测 |
| SQL-B26 | 只读到可写模式切换 | 未 step-up 进入二次验证；最近验证且有权限时才成功 | 待测 |
| SQL-B27 | 可写模式安全会话 | `@@sql_safe_updates=1`，服务端会话与页面模式一致 | 待测 |
| SQL-B28 | 无 WHERE UPDATE/DELETE 与危险 DDL | 额外确认缺失时拒绝；当前版本若选择全禁止则恒定拒绝 | 待测 |
| SQL-B29 | 有 WHERE 的可写语句 | 授权后仅更新 QA 数据，影响行数正确，数据保留 | 待测 |
| SQL-B30 | SQL 审计成功/拒绝/失败 | 记录操作者、连接、库、语句类型、时间、耗时、行数和结果，不记录敏感值 | 待测 |
| SQL-B31 | MySQL/MariaDB 结果类型兼容 | NULL、数字、布尔/位、时间、JSON、BLOB 均稳定渲染且有边界 | 待测 |
| SQL-B32 | 响应式、键盘、本地化和无 JS | 中英文、窄屏、键盘焦点正确；无 JS 至少可导航和执行安全单条查询 | 待测 |

### 白盒测试（SQL-W01–SQL-W28）

| # | 测试条目 | 关键断言 | 状态 |
| --- | --- | --- | --- |
| SQL-W01 | SQL 词法前缀分类 | SELECT/SHOW/DESC/DESCRIBE/EXPLAIN 分类正确 | 待测 |
| SQL-W02 | 注释/BOM/空白规范化 | 移除前导噪声后仍不可绕过 | 待测 |
| SQL-W03 | CTE 主语句分类 | 嵌套 CTE 正确定位最终 SELECT，写主语句拒绝 | 待测 |
| SQL-W04 | CTE 副作用检查 | CTE 体内非只读语法不可通过 | 待测 |
| SQL-W05 | 多语句扫描 | 忽略字符串/引用标识符/注释中的分号，拒绝真实第二语句 | 待测 |
| SQL-W06 | 禁止语句类型 | DML/DDL/CALL/DO/LOAD/LOCK/SET/USE 的只读策略有明确断言 | 待测 |
| SQL-W07 | 只读后端强制 | 伪造前端 mode 值仍由服务端模式判定 | 待测 |
| SQL-W08 | 只读事务 | 连接使用只读事务，提交/回滚/异常路径均关闭资源 | 待测 |
| SQL-W09 | 可写会话 Safe Updates | 执行前设置 `sql_safe_updates=1`，不泄漏到其他模式 | 待测 |
| SQL-W10 | 危险语句检查 | 无 WHERE UPDATE/DELETE 和 DROP/TRUNCATE/ALTER 需额外确认或被禁止 | 待测 |
| SQL-W11 | 模式切换权限 | PermissionManageDatabases 和最近 step-up 缺一不可 | 待测 |
| SQL-W12 | 模式作用域/过期 | 连接级或会话级作用域明确，超时、登出或连接切换后回到只读 | 待测 |
| SQL-W13 | 对象列表查询 | information_schema 参数化，表/视图类型与排序正确 | 待测 |
| SQL-W14 | 字段元数据查询 | 字段、类型、NULL、默认、extra、主键映射正确 | 待测 |
| SQL-W15 | 索引元数据查询 | 复合/唯一/前缀/降序索引可稳定聚合 | 待测 |
| SQL-W16 | 标识符安全 | 库名/表名包含反引号时正确引用，不可注入 | 待测 |
| SQL-W17 | 系统库过滤 | 默认过滤与显式 include-system 参数均覆盖 | 待测 |
| SQL-W18 | 结果集解码 | 动态列、NULL、`[]byte`、时间与数字无 panic/乱码 | 待测 |
| SQL-W19 | 返回行上限 | 服务端强制 clamp，通过额外取一行判定 truncated | 待测 |
| SQL-W20 | 超时与取消 | context deadline 传到 driver，rows/tx/db 均关闭 | 待测 |
| SQL-W21 | 参数边界 | 空 SQL、过长 SQL、非法库、超大 max rows/超时被拒绝或 clamp | 待测 |
| SQL-W22 | Web handler CSRF/状态码 | 缺 token=403，分类拒绝=400/422，超时和上游错误映射稳定 | 待测 |
| SQL-W23 | Web 权限与 step-up 路由声明 | 新增路由全部纳入 authorization 契约测试 | 待测 |
| SQL-W24 | SQL 审计完整性 | 成功/失败/拒绝/超时均写操作者、请求 ID、类型、耗时和行数 | 待测 |
| SQL-W25 | 审计脱敏 | 字面量中的密码/token/连接信息不进 target/result/日志 | 待测 |
| SQL-W26 | Privilege Broker 边界 | 托管模式不把密码或原始 DB 能力返回 Web 进程 | 待测 |
| SQL-W27 | MySQL/MariaDB 方言兼容 | 两引擎的 information_schema、只读事务、安全更新均在集成测试执行 | 待测 |
| SQL-W28 | 模板/本地化/响应式契约 | 中英文 key 齐全，窄屏不溢出，连接页签切换尺寸不变 | 待测 |

### 部署后逐项执行结果

| 条目 | 结果 | 实测证据 |
| --- | --- | --- |
| SQL-B01、B02、B03 | 通过 | 外部 Playwright 分别打开 MySQL 8.4/MariaDB 11.8 的明文与 TLS 连接，对象页和 SQL 页均可用 |
| SQL-B04、B05、B06 | 通过 | `scriptboard_qa.widgets` 可见；系统库默认隐藏，显式显示后按服务端实际系统库集合列出 |
| SQL-B07、B08、B09、B10 | 通过 | 字段、类型、NULL、默认值、PK、PRIMARY 索引、前 200 行和发送编辑器逐项通过 |
| SQL-B11、B12、B13 | 通过 | 四种连接均执行 SELECT、SHOW、DESC、DESCRIBE、EXPLAIN 和只读 CTE |
| SQL-B14、B15、B16、B17、B18、B19、B20 | 通过 | 写入/DDL、三种注释、混合大小写、写 CTE、多语句和 CALL/DELIMITER 绕过逐条执行；全部被后端拒绝，字符串内分号正常返回 |
| SQL-B21、B22、B23、B24 | 通过 | 1 行上限显示截断；1 秒 SLEEP 超时后 SELECT 1 恢复；错误脱敏；读写路由无 CSRF 均为 403 |
| SQL-B25、B26 | 通过 | 全仓授权契约通过；写路由要求数据库权限与 step-up，近期验证后才执行 |
| SQL-B27、B28、B29 | 通过 | 非键 WHERE 更新被 MySQL Safe Updates 拒绝；无 WHERE 更新无额外确认被拒绝；按主键更新成功并保留 `write-tested-retained` 数据 |
| SQL-B30 | 通过 | 审计页检索到 `execute_mysql_sql`、管理员和执行结果；只记录 SQL SHA-256，不含密码或 SQL 原文 |
| SQL-B31 | 部分通过 | MySQL/MariaDB 的 NULL、整数、文本、时间及动态列渲染通过；本轮未额外创建 JSON/BLOB/bit 边界数据 |
| SQL-B32 | 通过 | 中英文、1440×1000、390×844、键盘、禁用 JavaScript和控制台零错误检查通过 |
| SQL-W01、W02、W03、W04、W05 | 通过 | `query_test.go` 覆盖词法前缀、注释、大小写、CTE 主语句/副作用、真实与引用内分号 |
| SQL-W06、W07 | 通过 | 分类器拒绝不支持语句；Web 读写路由固定后端模式，不能用表单伪造 |
| SQL-W08、W09、W10 | 通过 | 只读路径执行 `SET TRANSACTION READ ONLY` 与只读 Tx；写路径启用 `sql_safe_updates=1`；危险语句必须显式确认 |
| SQL-W11、W12 | 通过 | 路由授权测试覆盖 PermissionManageDatabases/recent step-up；模式不持久化且每次页面/连接默认只读 |
| SQL-W13、W14、W15 | 通过 | 四实例 information_schema 实测返回对象、字段、默认值、主键和索引聚合 |
| SQL-W16 | 部分通过 | 标识符由反引号转义且库/对象长度和 NUL 校验已审查；未创建名称含反引号的实库夹具 |
| SQL-W17 | 通过 | 默认数据库列表与显式系统数据库列表均在四实例执行 |
| SQL-W18 | 部分通过 | 动态列、NULL、`[]byte`、整数和时间解码路径通过；未补充最大 BLOB 单元格夹具 |
| SQL-W19、W20、W21 | 通过 | 1000 行/4 MiB/64 KiB 单元格边界、context 超时以及参数默认值/clamp 的单元测试和实库测试通过 |
| SQL-W22、W23 | 通过 | 读写 handler CSRF 单测、实际 403、权限与 step-up 路由声明全仓测试通过；分类错误在同页安全显示 |
| SQL-W24 | 部分通过 | 成功、分类拒绝和超时均经过统一审计路径；实测审计检索通过，未单独故障注入审计存储失败 |
| SQL-W25、W26 | 通过 | 审计仅含语句类型/耗时/行数/hash；Privilege Broker 校验请求边界并清除客户端 Actor |
| SQL-W27、W28 | 通过 | MySQL/MariaDB 明文/TLS 四实例逐项执行；模板本地化、响应式、页签尺寸和 CSP/控制台检查通过 |

### Redis 键空间补充测试

| # | 测试条目 | 结果 |
| --- | --- | --- |
| REDIS-K01 | SCAN 工具栏中英文、空模式等同 `*`、`qa:*` 匹配 | 通过 |
| REDIS-K02 | 按双冒号（`::`）namespace 分组，单冒号及无分隔符键进入独立未分组分段 | 通过 |
| REDIS-K03 | namespace 原生 details 展开/折叠及无 JavaScript 可用性 | 通过 |
| REDIS-K04 | String 键值预览 | 通过，`qa:string=retained` |
| REDIS-K05 | Hash/List/Set/ZSet/Stream 类型化预览和 100 项截断 | 部分通过；保留数据与后端路径覆盖 Hash/List/Set/ZSet，浏览器逐键实测 String，本轮未创建 Stream 夹具 |
| REDIS-K06 | TTL、永久键、内存占用与选中态 | 通过 |
| REDIS-K07 | 键名/模式 URL 编码与特殊冒号 | 通过；修复了首次实测发现的二次编码 |
| REDIS-K08 | Broker 读取键边界、无效键名和错误脱敏 | 通过 |
| REDIS-K09 | 桌面/移动宽度、MySQL/Redis 页签尺寸稳定 | 通过 |
| REDIS-K10 | 外部 Playwright 控制台与页面错误 | 通过，0 条错误 |

## 结论

预先列出的 49 项测试中，48 项通过，1 项因宿主缺少 `mysql`/`mysqldump` 客户端而环境受限。环境受限项的 HTTP 受理、失败状态落库与全部白盒路径均已验证，不属于连接功能失败。

本轮在原有修复基础上新增了 MySQL/MariaDB 对象浏览、SQL 控制台和 Redis namespace 键值浏览。真实浏览器测试又发现并修复了 Redis 键链接二次编码、SQL 错误结果被异步导航丢弃、对象库选择的内联事件违反 CSP，以及亚秒 SQL 耗时被显示为 0 分钟四个问题。修复后已重新部署并完成全量回归。

## 保留的部署与数据

| 项目 | 当前状态 |
| --- | --- |
| ScriptBoard | `http://127.0.0.1:18788`，PID 3900，保持运行；二进制由当前 `dev` 工作区构建 |
| State Root | `D:\Github\worktrees\ScriptBoard\database-unified\.scratch\database-unified-deployment\state` |
| 管理员 | `admin`；密码仅保留在 State Root 私有文件中 |
| Docker 项目 | `scriptboard-database-qa`，7 个容器均保持运行 |
| 测试定义与证书 | `.scratch/database-docker-matrix/` |
| Playwright 截图 | `.scratch/database-docker-matrix/database-desktop.png`、`database-mobile.png`、`mysql-sql-console-desktop.png` |
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
- MySQL 与 Redis 切换前后的右侧详情宽高保持一致；桌面端共享固定详情舞台，移动端保留自然高度。
- 自定义面板“调整顺序”会先按可见顺序重新编号再移动，重复排序值和顺序空洞不再导致操作无效；外部 Playwright 已验证移动并刷新后顺序保持，测试面板与卡片保留。
- 真实保留数据下的 Playwright 桌面、移动端、键盘、中文、禁用 JavaScript 和控制台检查全部通过。

## 外部接口日志文件排版补充验收

本轮逐项测试清单：日志分组标题与表格左右边界、跨分组五列轨道、来源/文件/大小/更新时间/操作列语义宽度、数字与操作列对齐、长名称及长路径截断、折叠与重新展开、430px 窄屏卡片、横向溢出、控制台错误、模板契约与全仓回归。

- 保留两组外部接口日志测试数据及其真实日志文件，覆盖短标签、长标签、短路径和长路径。
- 1440px 桌面端两组标题与表格左右边界均为 `304–1374px`，五列起点均为 `304/561/924/1020/1223px`。
- 日志表格不再额外右移 28px；来源、文件、大小、更新时间和操作采用共享固定轨道，大小列右对齐、操作列靠右收束。
- 430px 窄屏恢复为标签—内容卡片排版，大小与操作改为左对齐；页面无横向溢出。
- 外部 Playwright 验证桌面、移动端、折叠交互和控制台错误均通过；截图保留为 `.scratch/database-docker-matrix/external-logs-desktop.png` 与 `external-logs-mobile.png`。

## 一级页面标题格式补充验收

本轮逐项测试清单：应用、容器、文件、变量、运行记录、审计事件、服务日志、快捷运行、计划任务和外部接口共十个一级页面；逐页核对主标题字号/字重/行高/字距、眉题、说明文字、操作区、桌面底部对齐、移动端左对齐、横向溢出与控制台错误。

- 十个页面统一使用 `primary-page-heading`，紧凑标题仅保留在设置和编辑器等二级页面。
- 1440px 下十页计算样式一致：主标题 `40.8px / 660 / 44.88px`，说明文字 `14.62px`，标题区底部间距 `18px`。
- 430px 下十页主标题均为 `30.6px`，标题区左对齐且无横向溢出；操作按钮按窄屏规则换行或铺满。
- 外部 Playwright 共访问十条真实路由，桌面与移动端均通过，控制台错误为 0；截图保留为 `.scratch/database-docker-matrix/page-heading-audit-desktop.png` 与 `page-heading-variables-mobile.png`。
- 新增白盒契约测试，防止一级页面重新引入独立的 22px 紧凑标题覆盖。

## 关键命令与限制

- 黑盒：PowerShell HTTP 会话、Docker 容器内客户端、Playwright Chromium。
- 白盒：目标包测试与两次全仓 `go test ./... -count=1`，最终一次在所有修复后执行。
- 未运行仓库的完整浏览器快照刷新套件，以免改写无关的跟踪截图；本轮使用独立的本地部署 Playwright 脚本与 `.scratch` 截图。
- `npm ci` 报告一个测试依赖树中的 moderate audit 提示；未执行会升级依赖并扩大修改范围的 `npm audit fix --force`。

## 合并至 dev 后重新部署

- 功能分支 `codex/database-unified` 已通过合并提交 `3244e17` 进入 `dev`；唯一冲突位于快捷运行模板，同时保留了 `dev` 的拖拽排序模式与统一一级标题契约。
- 合并态执行 `go test ./... -count=1` 全部通过，并从 `dev` 构建 `D:\Github\ScriptBoard\.scratch\database-unified-dev-deployment\scriptboard-dev.exe`。
- 新进程 PID 为 `37008`，继续监听 `127.0.0.1:18788`；沿用原 State Root，所有数据库容器、连接、SQL 审计、Redis 键及外部日志测试数据均保留。
- 外部 Playwright 基础矩阵、MySQL/MariaDB SQL 工作台、外部日志排版及十个一级页面标题回归全部通过，控制台错误为 0。

## 合并态全功能 Playwright 复验

本轮在合并后的 `dev` 部署上按以下清单逐项执行，测试使用外部 Playwright Chromium；允许创建且不清理测试数据。

| # | 测试范围 | 结果 | 实测摘要 |
| --- | --- | --- | --- |
| PW-01 | 基础访问、登录、导航、浏览器控制台 | 通过 | `/login` 返回 200，桌面与移动端完成登录和导航，控制台错误 0 |
| PW-02 | 数据库融合页、混排连接、稳定切换、新增引擎选择 | 通过 | MySQL/Redis 混合排序及分页可用；连接类型可选；两类详情切换宽高稳定 |
| PW-03 | MySQL/MariaDB 对象浏览与 SQL 控制台 | 通过 | 四个明文/TLS 连接完成对象、字段、索引、前 200 行、发送编辑器及查询结果验证 |
| PW-04 | SQL 后端安全与审计 | 通过 | 只读、注释/大小写/CTE/多语句绕过、截断、超时、CSRF、step-up、Safe Updates 和审计全部通过 |
| PW-05 | Redis 内部页签、SCAN、namespace 与键值 | 通过 | 遍历 6 个 Docker Redis 连接；String、Hash、List、Set、ZSet、TTL 六类保留键均可打开并显示值 |
| PW-06 | 外部接口日志文件排版 | 通过 | 两个保留日志分组的五列轨道、边界、折叠、桌面与移动端全部通过 |
| PW-07 | 十个一级页面标题格式 | 通过 | 应用、容器、文件、变量、运行、审计、服务日志、快捷运行、计划任务和外部接口桌面/移动端一致且无溢出 |
| PW-08 | 快捷运行调整顺序 | 通过 | 浏览器拖拽与键盘 ArrowDown 均成功，完成后刷新仍保持顺序 |
| PW-09 | 自定义面板调整顺序 | 通过 | 在保留面板中移动卡片，等待实际 DOM 更新后刷新，顺序持久化 |
| PW-10 | 响应式、键盘、本地化、禁用 JavaScript | 通过 | 1440×1000、390×844/430px、中英文、Escape、键盘操作及无 JavaScript 路径均通过 |

新增并保留了快捷运行分组 `PW retained mt86l5kr A`、`PW retained mt86l5kr B` 及三个运行项；自定义面板卡片的新顺序、Redis 键、SQL 写模式验证数据也保持在当前 State Root。快捷运行源文件保留在 `D:\Github\ScriptBoard-QA\playwright-all-modified-qa.cmd`。

测试脚本探索阶段发现四项测试夹具假设：受保护 `.scratch` 文件不能作为 Host Files 来源、默认 `dragTo` 落点不触发列表换位、面板排序需等待异步 DOM 更新、Redis 连接列表需要遍历分页。脚本分别改为仓库外 QA 文件、Playwright 页面内 DragEvent、基于名称变化等待和分页遍历后，完整复验全部通过；未发现产品回归，也未删除探索阶段创建的数据。

复验截图保留为 `.scratch/database-unified-dev-deployment/quick-run-reorder-dragged.png`、`custom-dashboard-reorder-retained.png`、`redis-values-retained.png`，并继续保留数据库、SQL 控制台、外部日志和页面标题截图。复验结束时 ScriptBoard PID 仍为 `37008`，`http://127.0.0.1:18788/login` 返回 200，7 个数据库容器全部保持运行。

## 快捷执行分组排序与一次性置顶复验

本轮先固定黑盒清单：分组级入口位置、单组作用域、常态/排序态卡片尺寸和内容一致、鼠标拖动持久化、方向键持久化、其他分组不变、一次性置顶、重复置顶幂等、移动端溢出、本地化和控制台错误。白盒清单覆盖完整清单并发校验、CSRF、权限、审计、相对顺序、未知分组和辅助操作目录契约。

- 页面级“调整顺序”入口已移除；每个分组标题的“调整顺序”位于 `…` 按钮前，未分组区同样可按组调整卡片顺序。
- 仅被选中的分组卡片可拖动；排序态继续使用原响应式卡片网格，卡片尺寸、名称、路径、最近记录、运行按钮及 `…` 菜单保持不变。
- 鼠标拖动与聚焦卡片后的方向键均已实际保存并刷新验证；保存请求提交完整分组/快捷执行清单，后端并发完整性保护继续生效。
- 普通分组 `…` 菜单新增“置顶”；它会立即将当前分组移动到首位，同时保持其他分组相对顺序，不创建固定置顶状态。重复操作保持幂等，无 CSRF 请求返回 403。
- 外部 Playwright 最终输出：`groupButtons/cardLayoutStable/drag/keyboard/moveTop/mobile=pass`，控制台错误 0。保留排序分组 `PW retained mt86bx8w A`，并将 `PW retained mt86l5kr A` 置顶；未删除任何测试数据。
- 截图保留为 `.scratch/database-unified-dev-deployment/quick-run-group-ordering-retained.png`。全仓 `go test ./... -count=1` 通过；重新部署 PID 为 `13576`，登录页返回 200。

- 截图保留为 `.scratch/database-unified-dev-deployment/database-full-width.png`；重新部署 PID 为 `27704`，登录页返回 200。

## 导入与导出图标统一复验

本轮先逐项审计所有明确标注“导入/Import”与“导出/Export”的操作，并区分普通上传、下载和文件选择动作。业务导入统一使用 Lucide `file-input`，业务导出统一使用 Lucide `file-output`；普通下载仍使用 `download`，文件选择仍使用对应文件类型图标。

- 白盒覆盖自定义面板、网站监控、MySQL、Kubernetes 和服务日志六个模板族；新增契约测试锁定 10 个导入动作和 5 个导出动作的图标映射。
- 目标 Web 测试与全仓 `go test ./... -count=1` 均通过，前端规范检测结果为空。
- 外部 Playwright 在保留数据上验证自定义面板、网站监控及确认页、MySQL 导入、Kubernetes 导入和服务日志 CSV 导出；五类页面全部通过，430px 无横向溢出，控制台错误为 0。
- 截图保留为 `.scratch/database-unified-dev-deployment/transfer-icons-desktop.png`；重新部署 PID 为 `12084`，登录页返回 200，原有数据库及测试数据均保留。

## Redis 双冒号键空间分段复验

本轮将 Redis 键空间的显式层级分隔符修正为双冒号（`::`）。单冒号属于普通键名字符，不再触发命名空间折叠；无分隔符键与单冒号键统一进入“未分组键”。

- 白盒回归先复现 `cache:item` 被错误拆到 `cache` 分组，修复后验证 `order::42`、`session::7` 正确分组，`cache:item` 与无分隔符键保持未分组；键值预览与 SCAN 查询继续通过。
- Docker `sb-qa-redis-noauth` 新增并保留 `qa::accounts::42`、`qa:single`、`qa-ungrouped` 三条边界测试数据。
- 外部 Playwright 遍历 10 个 Redis 连接，确认双冒号键进入 `qa` 折叠段、单冒号及普通键进入未分组段；折叠/展开、值预览、430px 响应式均通过，控制台错误为 0。
- 全仓 `go test ./... -count=1` 通过；截图保留为 `.scratch/database-unified-dev-deployment/redis-double-colon-keyspace.png`。重新部署 PID 为 `3900`，登录页返回 200。

# 数据库工作台本地验收

日期：2026-09-14。基于 dev / 8bdd6ed，在 codex/redis-key-separator 工作树完成。

## 变更

- Redis 键空间可选冒号或双冒号分段，默认双冒号；分页、预览和关闭预览保留模式，读取使用完整键名。
- 点击备份计划名称打开详情抽屉，展示 Cron、未来五次时间（含时区）、启停状态及按数据库分页的执行记录。
- Schema 72 将执行记录关联到计划；失败和重叠跳过记录独立保留，不随备份轮换删除。可确认归属的旧记录自动补关联。
- MySQL/Redis 连接悬停或键盘聚焦展示完整名称和连接地址。
- MySQL 下载走浏览器原生文件下载。原问题可重复复现为 fetch 请求且没有 download 事件；修复后下载成功，页面保持可操作。
- MySQL 连接默认打开本地备份。备份、计划和操作历史不连接远端；查看实时页面或显式加载计划数据库时才连接。

## 测试条目与结果

| 条目 | 结果 |
| --- | --- |
| 登录、概览、数据库基础访问 | 通过 |
| 完整连接名称和地址 Tooltip | 通过 |
| 离线连接打开备份，不触发远端查询 | 通过，HTTP 回归在修复前失败、修复后通过 |
| Cron 未来五次、停用提示、时区 | 通过 |
| 历史每页 12 条、计划隔离、失败/成功/重叠跳过 | 通过 |
| 历史在备份轮换后保留、旧数据升级 | 通过 |
| Redis 默认双冒号、切换冒号、原始键读取 | 通过 |
| Redis SCAN 下一页、预览关闭后保留模式 | 通过 |
| 390px 窄屏、Escape、无 JavaScript 详情导航 | 通过 |
| 44 MiB 本地下载、文件长度与 SHA-256 一致 | 通过；原生 download 事件、无 fetch 下载、页面可继续切换页签 |
| 浏览器脚本异常 | 0 |
| 相关包 go vet | 通过 |
| go test ./... | 全仓通过，日志 go-test.log |

外部浏览器：Microsoft Edge（Playwright）；未使用内部浏览器。Redis 使用本地 RESP 夹具，MySQL 使用离线地址和保留的本地记录，下载为 44 MiB 二进制传输夹具。本次不执行真实 MySQL 备份或恢复；连接安全模式由已有领域测试覆盖。

## 保留部署与复现

- 应用：http://127.0.0.1:19756
- 登录：admin / calibration-ledger-2026
- Redis RESP 夹具：127.0.0.1:19757
- 工作树：D:/Github/worktrees/ScriptBoard/redis-key-separator
- 部署、下载夹具、截图和日志：.scratch/database-enhancements/
- 当前二进制：fixture-final.exe；测试状态：deployment/state。
- 夹具脚本：integration/browser/database-enhancements-seed.cjs、database-enhancements-redis-fixture.cjs。
- 浏览器验收：integration/browser/database-enhancements-local.cjs、mysql-backup-download-local.cjs；需要 Playwright，BROWSER_EXECUTABLE 可指定外部 Chromium 浏览器。
- 浏览器结果：browser-results.json；截图：plan-desktop.png、plan-mobile.png、redis-mobile.png。
- 原工作区 AGENTS.md 的未提交修改和 docs/previews/registry-management.html 保持原样。

# 镜像仓库工作台本地测试报告

日期：2026-09-11。所有修改和验证在 `codex/registry-workspace` worktree 内完成。

## 修改内容

- 镜像仓库页采用全宽工作区，支持桌面与窄屏。
- 连接新增“只读 / 允许修改”。新连接默认只读，已有连接保留允许修改；后端拦截只读连接的删除预览、预览读取和执行。保存连接会使旧预览失效。
- 清理入口集中在列表工具栏“清理规则”；版本详情保留单版本删除预览，移除重复的清理标签、列表批量删除按钮与详情全版本删除按钮。
- 列表复用镜像卡片的数据：版本标签、压缩下载大小（多平台时显示范围）、版本时间及来源、同步状态。每页 20 个镜像，摘要查询沿用 20 秒超时。
- namespace 支持多级折叠与当前路径自动展开，例如 `jiangnan/main/nested/`；按带尾斜线的路径筛选后代，排除相似名称的兄弟目录。
- 连接页签采用左侧纵向列表，多级 namespace 位于同一导航轨道下方；访问模式与编辑入口位于右侧内容区顶部。
- 中英文 README 同步更新。

## 测试条目与结果

| 测试 | 结果 |
| --- | --- |
| 未登录访问跳转、登录、未知路由 404 | 通过 |
| 添加和切换连接、任务抽屉 | 通过 |
| 新连接默认只读；只读界面隐藏删除入口 | 通过 |
| 直接请求后端删除预览被拒绝 | 通过 |
| 保存及重新加载访问模式和 TLS 选择 | 通过 |
| HTTP、受信 HTTPS、显式跳过证书验证 | 通过 |
| 切换回只读后拒绝执行，重新允许修改后旧预览仍失效 | 通过 |
| 多级目录折叠、父目录展开、兄弟前缀隔离 | 通过 |
| 标签、压缩大小、时间及其来源、无版本状态 | 通过 |
| 20/5 条分页、无效页码、空搜索 | 通过 |
| 单版本预览显示共享 digest 的所有 tag | 通过 |
| 清理确认、远端 DELETE、兄弟目录保留、操作记录 | 通过 |
| 1600px 桌面占满右侧；390px 窄屏无整页横向溢出 | 通过 |
| 无 JavaScript 页面和目录导航 | 通过 |
| 浏览器脚本异常 | 0 |
| 布局机械检查 | 无发现 |

执行命令：

```text
go test ./internal/registryconnection ./internal/registrymonitor ./internal/web ./internal/privilegebroker -count=1
go test ./internal/web -run RegistryInventoryPagination -count=1
node integration/browser/registry-workspace-local.cjs
node integration/browser/registry-management-local.cjs
```

浏览器使用外部 Microsoft Edge（Playwright，headless）。本地部署为真实应用测试入口 `integration/browser/fixture`，远端仓库为可持久化的 Registry V2 HTTP 测试服务；未连接生产仓库。生产厂商特有扩展、垃圾回收和浏览器之外的客户端未在本次验证范围内。

## 保留的部署与产物

- 应用：<http://127.0.0.1:19746>
- 用户名：`admin`；测试密码：`calibration-ledger-2026`
- 测试仓库：<http://127.0.0.1:18946>
- worktree：`D:/Github/worktrees/ScriptBoard/registry-workspace`
- 状态、测试数据、日志、截图和 JSON 结果：worktree 下 `.scratch/registry-test/`
- 截图：`desktop.png`、`mobile.png`、`cleanup.png`、`no-js.png`。
- 历史流程结果：`.scratch/registry-test/legacy-flow/browser-results.json`。

部署与测试数据保持运行和保留。合并后若清理 worktree，需先迁移此部署。

连接栏调整补充验证：`go test ./internal/web -run TestRegistry -count=1` 通过；外部 Edge 验证编辑抽屉对应当前连接、桌面与窄屏布局、无 JavaScript 页面均通过。保留部署已更新至此次调整。

纵向连接布局补充验证：Registry Web 测试通过；外部 Edge 检查连接页签纵向坐标、切换后选中状态、当前连接编辑抽屉、桌面和窄屏截图、无 JavaScript 页面均通过。窄屏宽表仅在列表容器中横向滚动。测试部署保持运行。

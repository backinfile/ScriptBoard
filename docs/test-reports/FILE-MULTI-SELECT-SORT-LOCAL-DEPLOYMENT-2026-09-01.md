# 文件页多选、表头排序与双日期本地部署测试报告

测试时间：2026-09-01（Asia/Shanghai）  
测试分支：`codex/file-multi-select-sort`  
worktree：`D:\Github\worktrees\ScriptBoard\file-multi-select-sort`

## 结论

- 文件列表新增显式多选模式，支持本页全选、Shift 连选、清除选择、Esc 退出、复制路径、ZIP 打包下载、批量移动和批量移入回收站。
- 批量移动在写入前检查全部同名冲突并同步文件引用；批量回收先检查运行租约与引用，失败时回滚已经移动的条目。
- 桌面文件列表显示名称、大小、创建时间、修改时间表头，四列均可点击切换升降序；移动端保留字段标签与排序面板。
- Windows 使用原生创建时间；Linux 仅在文件系统提供 birth time 时展示，否则显示 `—`，缺失值在升降序中均置后。
- 全仓 Go 测试、相关包 `go vet`、Linux amd64 交叉编译、外部 Chromium 门禁和真实本地部署 HTTP 黑盒验收均通过。

## 测试条目

1. 登录页、匿名路由保护与管理员登录。
2. 文件页基础访问和四个命名表头。
3. 创建时间、修改时间各自的单元格渲染。
4. 点击创建时间与修改时间表头后的升降序状态、URL 和服务端排序。
5. 多选进入/退出、单项选择、本页全选、Shift 连选、选中计数、清除选择和复制路径。
6. 文件与目录混合选择后生成 ZIP，验证文件名、目录层级、内容和下载响应头。
7. 批量移动到目录、同名冲突整批预检、引用同步和文件系统提交结果。
8. 批量移入回收站、父子路径去重、引用确认、数据库记录和文件系统回滚语义。
9. 桌面表格对齐、无多余横向滚动条和操作菜单层级。
10. 820px 平板与 390px 移动端无横向溢出，移动记录仍显示两个日期；移动端多选工具栏单独截图验收。
11. JavaScript 关闭时原有文件浏览、预览和上传入口不回退。
12. 服务监听、标准错误和基础安全响应。

## 自动化验证

| 命令 / 门禁 | 结果 |
| --- | --- |
| `go test ./... -count=1` | 通过 |
| `go vet ./internal/hostfiles ./internal/web` | 通过 |
| Linux amd64 `go test -c ./internal/hostfiles` | 通过 |
| `node integration/browser/run.cjs` | 通过；真实执行 ZIP 下载、批量移动、批量回收，并覆盖排序、多选、响应式与 console/page error 门禁 |
| Impeccable 机械检测 | 已运行；本次新增样式改用现有字号与圆角令牌，仓库既有全局告警不在本功能内扩改 |
| `git diff --check` | 通过；仅仓库既有 Windows 换行提示 |

## 本地部署

| 项目 | 值 |
| --- | --- |
| URL | `http://127.0.0.1:18901` |
| PID | `15740` |
| 监听边界 | 仅 `127.0.0.1:18901` |
| 部署目录 | `.scratch/local-deploy-file-multi-select-20260901` |
| State Root | `.scratch/local-deploy-file-multi-select-20260901/state` |
| 登录用户 | `admin` |
| 初始密码 | 保留在 State Root 的 `secrets/initial-admin-password` |
| 保留测试数据 | `D:\ScriptBoard-QA\file-multi-select-20260901` |
| stderr | 0 字节 |

最终构建重启后，真实部署 HTTP 黑盒验证登录、文件页访问、创建时间排序、ZIP 签名、批量移动提交、批量回收提交与零标准错误，全部通过。新增批量测试数据与回收站记录均按约定保留。部署、State Root、管理员凭据与测试数据均保留；报告生成后未停止进程。

## 视觉证据

- `integration/browser/snapshots/files.png`：普通桌面文件表头与双日期列。
- `integration/browser/snapshots/files-selection.png`：多选状态、选中行、计数与批量复制路径操作。
- `integration/browser/snapshots/files-mobile.png`：390px 移动端双日期记录布局。
- `integration/browser/snapshots/files-mobile-selection.png`：390px 移动端批量下载、移动、回收站与复制路径工具栏。

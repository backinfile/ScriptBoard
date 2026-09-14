# 命名空间导航本地测试

日期：2026-09-14。基于 dev / e5997da，在 codex/registry-namespace-navigation 中完成。

## 调整

- 仓库连接保留独立纵向列；命名空间位于右侧镜像工作区、镜像列表旁。
- 每个目录节点只显示一个名称链接。首次点击未选中节点仅筛选此前缀的镜像，再次点击当前节点才切换其子目录展开状态；子节点状态独立保留。
- 中英文 README 和已有浏览器测试同步更新。

## 验证结果

- `go test ./internal/web -run TestRegistry -count=1`：通过。
- 外部 Microsoft Edge：基础登录与访问、三个区域独立布局、首次点击筛选、再次点击展开、父子展开状态隔离、刷新保持、键盘操作、收起父节点直接筛选、单个名称链接、多级目录与兄弟前缀隔离、当前连接编辑、390px 窄屏无整页横向溢出、无 JavaScript 导航：全部通过，脚本异常 0。
- 人工查看桌面与窄屏截图：通过。
- 浏览器脚本：`integration/browser/registry-namespace-local.cjs`。

## 保留部署

应用：http://127.0.0.1:19746
账号：admin / calibration-ledger-2026
Registry V2 测试服务：http://127.0.0.1:18946
本次使用真实应用测试入口和本地 Registry V2 HTTP 模拟服务，未连接生产仓库。

当前二进制和截图、日志、results.json 位于此 worktree 的 `.scratch/namespace-test/`。
连接和测试状态沿用 `D:/Github/worktrees/ScriptBoard/registry-workspace/.scratch/registry-test/deployment`，测试数据全部保留。

## 数据库与镜像仓库共用框架

两页共用 `resource-workspace.css`，统一页面标题、连接轨道、连接项、内容内边距、页签、滚动区及响应式断点；数据库未选中连接的占位区也使用相同框架。命名空间保留独立列和单行节点链接。

- `go test ./internal/web -run 'Registry|Database|MySQL|Redis' -count=1`：通过。
- 外部 Edge 在 1600px、1000px、390px 下逐项比对两页标题、连接栏、侧栏标题、连接项、地址字号及内容区的坐标/宽度/内边距：一致，均无整页横向溢出。
- 1600px 时，两页标题高度 154.1875px，连接栏宽 280px，侧栏标题起点 x=304，内容区起点 x=528、内边距 28px 56px 72px。
- 命名空间筛选、鼠标和键盘展开、编辑抽屉、无 JavaScript 导航回归：通过。
- 使用保留的本地样例连接“框架对齐测试”检查数据库选中连接与连接失败展示；未启动 MySQL 服务，本次不验证数据库可用性。
- 逐页逐视口截图与测量结果保存在 `.scratch/namespace-test/`：`*-aligned-*.png`、`framework-results.json`。

## 节点点击状态验证

外部 Edge 已验证：未选中父节点首次点击不展开，当前节点再次点击只展开/折叠自身；切换筛选保持其他节点状态，折叠父节点保留子节点的 open 状态，刷新按连接恢复状态。浏览器脚本异常为 0。无 JavaScript 时保留原生树与链接导航。

当前本地部署二进制：`.scratch/namespace-test/fixture-click.exe`；原有连接与测试数据继续保留。

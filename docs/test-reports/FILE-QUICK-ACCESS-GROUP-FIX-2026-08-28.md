# 文件快捷访问分组保存与抽屉定位修复测试报告

测试时间：2026-08-28（Asia/Shanghai）  
分支：`codex/fix-file-group-drawer`  
基线：`1307157`

## 结论

- 文件快捷访问条目选择非空共享分组后可正常保存，不再占住唯一 SQLite 连接。
- 编辑快捷访问条目的抽屉已提升到页面顶层，固定覆盖当前视口并贴齐右侧，不再受文件浏览区入场动画的 `transform` 定位上下文影响。
- 修复版真实本地部署的外部 Chromium 验证为 6 项通过、0 项失败，浏览器 console error 与 page error 均为 0。
- 全仓 Go 测试、Go vet、命令构建、前端语法检查、完整 Chromium Gate 与 `git diff --check` 均通过。

## 保留部署

| 项目 | 值 |
| --- | --- |
| URL | `http://127.0.0.1:18871` |
| 进程 | `scriptboard-fix-final.exe`，PID `48464` |
| 监听边界 | 仅 `127.0.0.1:18871` |
| 部署目录 | `.scratch/local-deploy-20260828` |
| State Root | `.scratch/local-deploy-20260828/state` |
| 登录用户 | `admin` |
| 初始密码 | 仅保留在 State Root 的 `secrets/initial-admin-password`，未写入报告 |
| 结构化结果 | `.scratch/local-deploy-20260828/e2e-results.json` |
| 抽屉截图 | `.scratch/local-deploy-20260828/file-group-drawer-open.png` |
| 页面截图 | `.scratch/local-deploy-20260828/file-group-fix.png` |

## 回归检查

1. 从文件页创建共享分组。
2. 固定真实宿主文件到快捷访问。
3. 打开编辑抽屉并确认其父元素为 `body`。
4. 确认抽屉遮罩覆盖视口且抽屉顶、右、底边与视口一致。
5. 选择非空 `Operations` 分组并保存，响应为 200。
6. 保存后再次访问已认证监控页，响应为 200。
7. 浏览器 console error 与 page error 为 0。
8. 后端回归测试通过单连接 SQLite 的真实应用入口完成分组创建、固定、分组保存和 JSON 回显。

## 自动化命令

```powershell
go test ./internal/web -run '^(TestFileQuickAccessCanMovePinIntoSharedGroup|TestFilesPageOffersCollapsedInstanceQuickAccess|TestFileQuickAccessSupportsFilesLabelsAndOrdering)$' -count=1 -timeout=60s
go test -p 1 ./... -count=1
go vet ./...
go build ./cmd/...
node --check internal/web/ui/assets/app.js
node --check integration/browser/run.cjs
npm test  # integration/browser
node .scratch/local-deploy-20260828/e2e-file-group-fix.cjs
git diff --check
```

全部通过。

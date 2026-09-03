# 文件夹拖放上传本地部署测试报告

测试时间：2026-09-03（Asia/Shanghai）

测试分支：`codex/folder-drop-upload`

## 结论

- 主机文件页面现在可以递归读取拖入的文件夹，保留相对目录结构，并通过原有全批次提交语义上传全部普通文件。
- 相对路径由 Host Files 模块逐段校验；绝对路径、父目录跳转、空路径段和平台危险名称均被拒绝。
- 本地与特权 Broker adapter 均覆盖嵌套路径；批次回滚会移除本批次创建的文件与空目录。
- 全仓 Go、Vet、浏览器 contracts、Chromium desktop gate 和真实本地部署 Chromium 验收通过。

## 测试条目

1. 登录页、匿名文件页保护、未知路由、静态资源和管理员登录。
2. 普通单层批次上传保持兼容。
3. 拖入含根文件和嵌套子目录的文件夹，保留完整相对路径。
4. 仅含一个文件的文件夹仍走批次接口并保留文件夹。
5. 嵌套文件冲突预检显示完整相对路径，Rename 只修改末级文件名。
6. 101 个文件在客户端拒绝；服务端仍保留 100 文件上限。
7. `../outside.txt`、绝对路径、双斜杠、反斜杠和清理后路径被拒绝。
8. Host Files 批次回滚清理由本批次创建的目录。
9. Broker manifest 往返后嵌套文件正确落盘。
10. 浏览器 console error 和 page error 均为 0。
11. 便携安装目录作为受保护路径返回 403，拖放功能未绕过保护策略。

## 自动化验证

| 项目 | 结果 |
| --- | --- |
| `go test ./...` | 通过，全部包无失败 |
| `go vet ./...` | 通过 |
| `npm test`（`integration/browser`） | 通过，全部 contracts 与 Chromium desktop gate 通过 |
| `node --check internal/web/ui/assets/app.js` | 通过 |
| Host Files / Broker / Web 定向测试 | 通过 |
| `git diff --check` | 通过 |

浏览器门禁按仓库锁文件安装依赖；`npm audit` 报告一个既有 moderate 级别上游依赖问题，本分支未修改依赖或锁文件。

## 本地部署 HTTP 验收

| 编号 | 测试项 | 结果 |
| --- | --- | --- |
| H01 | `GET /login` | HTTP 200 |
| H02 | 匿名 `GET /resources/files` | HTTP 303，跳转 `/login` |
| H03 | 未知路由 | HTTP 404 |
| H04 | `GET /assets/app-v2.js` | HTTP 200，包含文件夹递归收集实现 |
| H05 | 部署标准错误 | 0 字节 |

结构化结果保存在 `.scratch/folder-drop-upload-20260903/http-results.json`。

## 外部 Chromium 验收

测试使用仓库外置 Playwright Chromium，没有使用应用内浏览器。

| 测试项 | 结果 |
| --- | --- |
| 管理员登录与 Host Files 页面 | 通过 |
| 嵌套文件夹拖放 | 通过，保留 `<folder>/src/main.js` |
| 嵌套文件页面访问 | 通过 |
| 单文件文件夹拖放 | 通过 |
| 嵌套冲突 Rename | 通过，生成 `<folder>/README (2).md` |
| 101 文件客户端上限 | 通过 |
| Console errors | 0 |
| Page errors | 0 |

结构化结果和截图分别保存在 `.scratch/folder-drop-upload-20260903/browser-results.json` 与 `.scratch/folder-drop-upload-20260903/folder-drop-files.png`。测试文件保留在 `D:\ScriptBoardLocalTests\folder-drop-upload-20260903`。

## 保留部署

| 项目 | 值 |
| --- | --- |
| 模式 | Windows 便携单进程部署 |
| URL | `http://127.0.0.1:18983` |
| PID | `30520` |
| 用户名 | `admin` |
| 初始密码 | State Root 私有文件 `secrets/initial-admin-password` |
| 部署目录 | `.scratch/folder-drop-upload-20260903` |
| State Root | `.scratch/folder-drop-upload-20260903/state` |
| stderr | 0 字节 |

部署进程、State Root、浏览器结果、截图和测试数据均保留。

# 文件删除原地刷新本地部署测试报告

测试时间：2026-09-03（Asia/Shanghai）

测试分支：`codex/folder-drop-upload`

## 结论

- 单个文件和批量文件移入回收站后，均停留在原文件目录，仅刷新文件列表区域。
- 删除响应返回原文件页并保留目录、排序、搜索和分页状态；前端异步处理该响应，不再跳转到回收站。
- 文件仍会正常进入回收站，可在回收站页面查询和恢复。

## 自动化验证

| 项目 | 结果 |
| --- | --- |
| 单文件删除 Web 测试 | 通过，响应定位到原目录 |
| 批量删除 Web 测试 | 通过，响应定位到原目录 |
| 文件页模板契约 | 通过，删除表单仅刷新 `[data-deferred-region]` |
| `go test ./...` | 通过，全部包无失败 |
| `go vet ./...` | 通过 |
| `npm test`（`integration/browser`） | 通过，包含删除后原地刷新与回收站记录验证 |

## 外部 Chromium 验收

测试使用仓库外置 Playwright Chromium，没有使用应用内浏览器。

| 测试项 | 结果 |
| --- | --- |
| 管理员登录与文件目录访问 | 通过 |
| 单文件删除 | 通过，条目原地消失，文件页 URL 与排序状态保持不变 |
| 批量删除 | 通过，两个条目原地消失，文件页 URL 与排序状态保持不变 |
| 局部刷新 | 通过，文件列表更新时页面主容器保持不变 |
| 回收站记录 | 通过，三个测试文件均可查到 |
| Console / Page errors | 0 |

结构化结果和截图分别保存在 `.scratch/folder-drop-upload-20260903/delete-refresh-result.json` 与 `.scratch/folder-drop-upload-20260903/delete-refresh-acceptance.png`。测试数据保留在 `D:\ScriptBoardLocalTests\folder-drop-upload-20260903`。

## 保留部署

| 项目 | 值 |
| --- | --- |
| URL | `http://127.0.0.1:18983` |
| PID | `34232` |
| 用户名 | `admin` |
| 初始密码 | State Root 私有文件 `secrets/initial-admin-password` |
| 部署目录 | `.scratch/folder-drop-upload-20260903` |
| State Root | `.scratch/folder-drop-upload-20260903/state` |
| `GET /login` | HTTP 200 |
| stderr | 0 字节 |

部署进程、State Root、测试结果、截图和测试数据均保留。

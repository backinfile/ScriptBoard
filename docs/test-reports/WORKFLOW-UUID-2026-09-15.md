# 工作流 HTTP 新建回归测试

日期：2026-09-15

## 修复

HTTP 页面未提供 `crypto.randomUUID` 时，新建工作流报错。节点 ID 改为在该接口不可用时使用 `crypto.getRandomValues` 生成，与现有工作台保持一致。

## 验证结果

| 测试项 | 结果 |
| --- | --- |
| 修复前外部 Chromium 普通 HTTP 新建 | 复现 `crypto.randomUUID is not a function` |
| 修复后 HTTP 新建、保存、刷新恢复节点 | 通过 |
| HTTPS 原生 randomUUID 新建、保存、刷新恢复节点 | 通过 |
| 真实本地部署登录与工作流页面访问 | 通过 |
| 本地部署禁用 randomUUID 后新建、真实保存、刷新恢复 | 通过 |
| 工作流状态接口与脚本资源访问 | HTTP 200 |
| 本地浏览器页面错误 | 无 |
| `go test ./internal/web -run Workflow -count=1` | 通过 |

浏览器回归命令：`node integration/browser/workflow-uuid-contract.cjs`，已加入浏览器测试入口。Go 测试将 TEMP/TMP 指向仓库内测试目录，以适配当前文件访问权限。

## 保留的测试部署

- 地址：http://127.0.0.1:18935
- 用户：`admin`
- 密码：`calibration-ledger-2026`
- 工作流：`913d47052df945c871479bfc58eceb28`
- 部署、数据、日志及真实接口验证脚本：`.scratch/workflow-uuid-test/`

本次验证使用重新构建的本地测试 fixture 和外部 Chromium，未发布远端版本。

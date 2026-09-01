# Registry 多标签与元数据切换本地部署测试报告

测试时间：2026-09-01（Asia/Shanghai）
测试分支：`codex/registry-tag-history`
worktree：`D:\Github\worktrees\ScriptBoard\registry-tag-history`

## 结论

- 每个 Registry 镜像显示按语义版本排序的最新 5 个标签；标签总数超过 5 个时，另显示最早 1 个且不重复。
- 每个展示标签保存自己的上传时间或 OCI 构建时间，以及对应的压缩下载大小；点击标签后卡片原位切换这些元数据。
- 默认选中首个标签，被省略的版本使用 `…` 表达，尾部保留最旧版本；桌面与移动布局均无横向溢出。
- 刷新失败时完整保留最近成功的标签集合与逐标签元数据；升级前仅有单标签的快照继续兼容。
- 全仓 Go 测试、相关包 `go vet`、浏览器契约和真实本地部署黑盒验收均通过。

## 自动化验证

| 命令 | 结果 |
| --- | --- |
| `go test ./...` | 通过 |
| `go test ./internal/registrymonitor ./internal/customdashboard ./internal/web -count=1` | 通过 |
| `go vet ./internal/registrymonitor ./internal/customdashboard ./internal/web` | 通过 |
| `node registry-tag-switch-contract.cjs` | 通过 |
| `node custom-dashboard-drawer-contract.cjs` | 通过 |
| `git diff --check` | 通过，仅有仓库既有的 Windows 换行提示 |

Windows 当前 Go 环境未启用 CGO，因此 `go test -race` 按工具链限制未运行；逐标签查询使用每个镜像最多 6 个有界并发槽，相关状态写入独立结果下标，并由单元测试和全量测试覆盖。

## 本地部署

| 项目 | 值 |
| --- | --- |
| ScriptBoard URL | `http://127.0.0.1:18893` |
| ScriptBoard PID | `59864` |
| Registry V2 URL | `http://127.0.0.1:18894` |
| Registry PID | `60512` |
| State Root | `.scratch/registry-tag-history-local-20260901/state` |
| 登录用户 | `admin` |
| 初始密码 | 仅保留在 State Root 的 `secrets/initial-admin-password` |
| 测试面板 ID | `kXehKdIKIUL3MJrutqpRwiAT` |
| 二进制 SHA-256 | `F57609AD9115BDE5F3C9E73E63CD9000676E5E20D73D16FC8DB2119AD172CA49` |
| ScriptBoard stderr | 0 字节 |
| Registry stderr | 0 字节 |

## HTTP 黑盒验收

| 编号 | 测试 | 结果 |
| --- | --- | --- |
| B01 | 登录页返回 200 并包含登录表单 | 通过 |
| B02 | 匿名访问监控页重定向到登录页 | 通过 |
| B03 | 未知路由返回 404 | 通过 |
| B04 | 使用新部署生成的管理员密码登录 | 通过 |
| B05 | 创建“Registry 标签历史验收”面板 | 通过 |
| B06 | 创建指向 HTTP Registry V2 的镜像卡片 | 通过 |
| B07 | 7 个仓库标签显示为 `v3.0.0 … v1.0.0`，省略第 6 新标签，不显示额外分组文字 | 通过 |
| B08 | ScriptBoard 只监听指定回环地址 | 通过 |

统计：**8 项通过，0 项失败**。

## 外部 Chromium 验收

| 编号 | 测试 | 结果 |
| --- | --- | --- |
| C01 | 卡片渲染 6 个可切换标签按钮 | 通过 |
| C02 | 默认显示首个标签 `v3.0.0` 的上传时间与 `15.0 KiB` | 通过 |
| C03 | 点击尾部标签 `v1.0.0` 后更新选中状态 | 通过 |
| C04 | 上传时间切换到 `2020-01-02 16:00` | 通过 |
| C05 | 压缩下载大小同步切换到 `4.0 KiB` | 通过 |
| C06 | 390 px 移动视口无卡片横向溢出，console error 与 page error 均为 0 | 通过 |

统计：**6 项通过，0 项失败**。

桌面截图、移动截图、结构化浏览器结果、测试 Registry、面板数据、State Root 和最终部署均保留在 `.scratch/registry-tag-history-local-20260901/`；报告生成后未停止进程或清理测试数据。

# 网站监控筛选栏单行对齐本地测试报告

测试时间：2026-09-02（Asia/Shanghai）  
测试分支：`codex/monitor-filters-single-row`

## 结论

- 网站监控筛选栏已在桌面端改为单行布局，标签、下拉框、筛选按钮、数量和排序入口使用同一中心线。
- 两个下拉触发控件、按钮和统计入口统一为 42px，桌面端和 520px 窄屏均无横向溢出。
- 两个下拉框最小宽度增至 160px，大字体设置下“全部状态”和“全部位置”仍可完整显示。
- 外部 Playwright Chromium、HTTP 基础访问、专项 Go 测试、全仓 Go 测试、`go vet` 和补丁格式检查全部通过。
- 本地测试数据、截图和部署均保留。

## 保留部署

| 项目 | 值 |
| --- | --- |
| URL | `http://127.0.0.1:18937` |
| PID | `53136` |
| 登录用户 | `admin` |
| 初始密码 | State Root 私有文件 `secrets/initial-admin-password` |
| 部署目录 | `.scratch/website-filter-single-row-20260902` |
| State Root | `.scratch/website-filter-single-row-20260902/state` |
| stderr | 0 字节 |

## 测试数据

- 本机服务：`本机筛选栏测试`
- 远程服务：`远程筛选栏测试`

## 验收结果

| 编号 | 测试项 | 结果 |
| --- | --- | --- |
| B01 | 登录页返回 200 | 通过 |
| B02 | 匿名访问 `/monitor/websites` 返回 303 并跳转 `/login` | 通过 |
| B03 | 未知路径返回 404 | 通过 |
| U01 | 桌面端筛选栏所有直接子控件中心线及高度一致 | 通过，高度均为 42px |
| U02 | 状态与服务位置下拉触发控件高度一致 | 通过，均为 42px |
| U03 | 状态选项保持“全部状态、故障、复核中、正常、已暂停” | 通过 |
| U04 | 服务位置选项保持“全部位置、本机服务、远程服务” | 通过 |
| U05 | 桌面端筛选栏无横向溢出 | 通过 |
| U06 | 520px 窄屏页面无横向溢出 | 通过 |
| U07 | 提交故障状态筛选后 URL 与筛选栏状态正常 | 通过 |
| U08 | 浏览器无 `pageerror` 和控制台错误 | 通过 |
| U09 | 28px 根字体下两个中文默认选项保留至少 8px 文字余量 | 通过，可用文字区 109px，文字宽度约 94.08px |
| U10 | 关闭状态触发控件为文字行高、内边距和边框保留完整垂直空间 | 通过，需要约 40.13px，实际 42px |
| T01 | 专项 Go 回归测试 | 通过 |
| T02 | `go test ./... -count=1` | 通过 |
| T03 | `go vet ./...` | 通过 |
| T04 | `git diff --check` | 通过 |

## 截图

- `.scratch/website-filter-single-row-20260902/website-filters-desktop.png`
- `.scratch/website-filter-single-row-20260902/website-filters-mobile.png`

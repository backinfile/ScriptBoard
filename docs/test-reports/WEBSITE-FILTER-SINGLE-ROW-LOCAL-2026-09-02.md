# 网站监控筛选栏单行对齐本地测试报告

测试时间：2026-09-02（Asia/Shanghai）  
测试分支：`codex/monitor-filters-single-row`

## 结论

- 网站监控筛选栏已在桌面端改为单行布局，标签、下拉框、筛选按钮、数量和排序入口使用同一中心线。
- 两个下拉框统一为 34px 紧凑控件高度，桌面端和 520px 窄屏均无横向溢出。
- 外部 Playwright Chromium、HTTP 基础访问、专项 Go 测试、全仓 Go 测试、`go vet` 和补丁格式检查全部通过。
- 本地测试数据、截图和部署均保留。

## 保留部署

| 项目 | 值 |
| --- | --- |
| URL | `http://127.0.0.1:18937` |
| PID | `35632` |
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
| U01 | 桌面端筛选栏所有直接子控件中心线及高度一致 | 通过，中心线均为 548.0625px，高度均为 34px |
| U02 | 状态与服务位置下拉框高度一致 | 通过，均为 34px |
| U03 | 状态选项保持“全部状态、故障、复核中、正常、已暂停” | 通过 |
| U04 | 服务位置选项保持“全部位置、本机服务、远程服务” | 通过 |
| U05 | 桌面端筛选栏无横向溢出 | 通过 |
| U06 | 520px 窄屏页面无横向溢出 | 通过 |
| U07 | 提交故障状态筛选后 URL 与筛选栏状态正常 | 通过 |
| U08 | 浏览器无 `pageerror` 和控制台错误 | 通过 |
| T01 | 专项 Go 回归测试 | 通过 |
| T02 | `go test ./... -count=1` | 通过 |
| T03 | `go vet ./...` | 通过 |
| T04 | `git diff --check` | 通过 |

## 截图

- `.scratch/website-filter-single-row-20260902/website-filters-desktop.png`
- `.scratch/website-filter-single-row-20260902/website-filters-mobile.png`

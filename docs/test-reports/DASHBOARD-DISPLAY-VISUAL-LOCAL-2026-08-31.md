# 自定义面板展示页深色视觉改造本地验收报告

测试时间：2026-08-31（Asia/Shanghai）  
测试分支：`dev`  
被测提交：`2280776`

## 结论

- 当前 `dev` 已重新构建并部署到 `http://127.0.0.1:18869`，复用 2026-08-28 充足数据 State Root。
- 外部 Playwright Chromium 与 HTTP 共完成 12 项检查，12 项通过、0 项失败。
- 新增"服务状态"自定义面板（ID `s81pHxBip81kjdcDXXLV0swv`，公开 slug `service-status`），含数值、百分比、额度、网站状态、镜像版本 5 种类型卡片。
- 监控页与公开页呈现深色氛围背景 + 渐变描边卡片 + 渐变发光圆环；管理配置页保持原有浅色样式，未被波及。
- 最终服务 PID 为 `18220`，仅监听 `127.0.0.1:18869`；`stderr.log` 为 0 字节。
- 测试面板、既有测试数据、截图与最终部署均保留。

## 保留部署

| 项目 | 值 |
| --- | --- |
| URL | `http://127.0.0.1:18869` |
| 登录用户 | `admin` |
| 初始密码 | 仅保留在 State Root 的 `secrets/initial-admin-password`，未写入报告 |
| 部署目录 | `.scratch/local-deploy-dashvisual-20260831` |
| State Root | `.scratch/local-deploy-richdata-final-20260828/state`（复用 2026-08-28 充足数据） |
| 服务日志 | `stderr.log` 为 0 字节 |

## 面板测试数据

| 卡片 | 类型 | 数据源 | 取值表达式 | 状态 |
| --- | --- | --- | --- | --- |
| DNS 解析 TTL | 数值 | `https://223.5.5.5/resolve?name=registry.npmmirror.com&type=A` | `Answer[0].TTL` | 正常（282 秒） |
| 记录存活占比 | 百分比 | 同上 | `Answer[1].TTL * 100 / 120` | 正常（26.67%） |
| TTL 刷新额度 | 额度 | 同上 | `Answer[0].TTL` / `Answer[1].TTL` | 正常（已用 282 / 剩余 32 秒） |
| 网站状态 | 网站状态 | 勾选既有 8 个网站监控 | — | 正常（7 Up / 1 Down） |
| 镜像版本 | 镜像版本 | `http://127.0.0.1:5999`（不可达 Registry） | — | 按预期显示"异常/暂未获取版本" |

数据源说明：面板默认出站客户端按设计拒绝 loopback/私网地址，且本机代理的 fake-IP DNS（198.18.0.0/15）在 `outboundpolicy.isAlwaysBlocked` 名单中，域名型公共 API 均无法直连；故改用证书含 IP SAN 的阿里公共 DoH 接口（IP 字面量、443 端口）作为数值来源。

## 验收条目

1. 登录页基础访问返回 200。通过。
2. 匿名访问 `/config/dashboards` 返回 303 并跳转 `/login`。通过。
3. 未知路径返回 404。通过。
4. 监控页 `/monitor/dashboard/s81pHxBip81kjdcDXXLV0swv` body 背景为 `rgb(7, 9, 15)`，且含 `custom-dashboard-public custom-dashboard-monitor`。通过。
5. 监控页卡片 `border-radius` 为 `20px`。通过。
6. 监控页圆环进度 circle 的 `stroke` 为 `url("#sb-dash-ring")`，页面含 `sb-dash-ring` 渐变定义。通过。
7. 匿名隐身上下文打开公开页 `/public/dashboard/service-status` 不跳转登录，body 背景为 `rgb(7, 9, 15)`。通过。
8. 公开页卡片 `border-radius` 为 `20px`，圆环 `stroke` 为渐变引用。通过。
9. 监控页与公开页均渲染 5 张卡片。通过。
10. 配置页 `/config/dashboards`（登录态）body 背景为 `rgb(242, 243, 247)`，非深色，class 为 `custom-dashboard-admin`，管理页样式未被波及。通过。
11. 目视检查截图：百分比/额度圆环在卡片内居中，"%" 与大数字基线对齐，卡片顶部有类型色渐变线，网站卡 24 小时色块圆角正常，无错位破版。通过。
12. 浏览器全程无 pageerror 与控制台错误。通过。

## 截图清单

| 截图 | 内容 |
| --- | --- |
| `.scratch/local-deploy-dashvisual-20260831/dashvisual-monitor.png` | 监控页深色样式（5 张卡片全量渲染） |
| `.scratch/local-deploy-dashvisual-20260831/dashvisual-public.png` | 匿名公开页深色样式 |
| `.scratch/local-deploy-dashvisual-20260831/dashvisual-config.png` | 管理配置页浅色样式回归对照 |

## 过程记录与遗留问题

- 旧 QA 实例（PID 48284，旧构建）已停止，新构建 exe 复用同一 State Root 重新部署。
- 初始用本地 `127.0.0.1:18999` JSON 服务作为数据源时卡片报"数据暂时不可用"：面板默认客户端按安全策略拒绝 loopback/私网地址（`TestDefaultDashboardClientBlocksLoopbackSources`），属预期行为，非缺陷。
- 切换为域名型公共 API 后仍失败：本机代理 fake-IP DNS 使所有域名解析到 198.18.0.0/15，该段在出站策略中恒定拒绝。最终改用阿里 DoH IP 字面量接口解决。
- 遗留：镜像版本卡按设计指向不可达地址以保持"异常"展示；如需恢复为正常状态，改填可达 Registry 即可。
- 辅助脚本（`seed-dashboard.cjs`、`update-cards.cjs`、`test-card.cjs`、`dashvisual-acceptance.cjs`）与截图均保留在部署目录。

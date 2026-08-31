# 最近提交本地与 Botyun 黑盒/白盒测试报告

测试时间：2026-08-31（Asia/Shanghai）  
本地分支：`dev`  
被测提交：`34db14a`  
远端升级前版本：`dev-404519f`  
远端升级后版本：`dev-34db14a`  
SSH 目标：`botyun`  
公网地址：`https://server-test.karen.fan`

## 结论

- `404519f..34db14a` 的文件快捷访问分组保存和 PJAX 抽屉修复，在 Windows 本地部署与 Botyun Linux 受管部署均通过真实浏览器验证；分组保存返回 HTTP 200，随后认证页面继续返回 200，没有再次锁住 SQLite。
- 自定义仪表盘监控页和公开页的深色背景、20px 卡片圆角和渐变卡片背景在两套环境均生效，管理配置页仍为浅色。
- 本轮不是全绿：`34db14a` 新增的零尺寸 SVG 使用内联 `style="position:absolute"`，被应用 CSP 拒绝；本地和 Botyun 都产生稳定的浏览器 console error。
- 完整 Chromium Gate 还存在一项契约不一致：新卡片用双层渐变 `background`，计算后的 `backgroundColor` 为透明；旧契约仍精确要求 `rgb(16, 19, 32)`，因此门禁失败。
- 全仓 Go 测试、定向回归、Go vet、JavaScript 语法检查和 Linux 四组件交叉构建通过。
- Botyun 已在完整备份后升级并继续运行；Web、Broker、Runner Socket 均 active/enabled，service identity 下 Doctor 全项通过，部署后 systemd error 日志为 0。

## 最近提交范围

| 提交 | 内容 | 测试域 |
| --- | --- | --- |
| `278dcce` | 文件快捷访问修改分组时在同一 SQLite 事务中校验共享分组，避免单连接自等待 | 分组保存、响应时限、后续认证访问、事务回归 |
| `2280776` | PJAX 清理时归还提升到 `body` 的快捷访问抽屉 | 侧栏 PJAX、编辑按钮、抽屉 DOM 生命周期、视口几何 |
| `34db14a` | 自定义仪表盘展示页深色氛围视觉和渐变卡片 | 监控页、公开页、管理页样式隔离、CSP、浏览器契约 |
| `1307157` | Broker Host Files 修复验证报告 | 报告与既有远端证据一致性 |
| `6ea5ee5` | 文件分组修复重部署报告 | 报告与修复提交、部署证据一致性 |

## 黑盒测试条目

| 编号 | 条目 | 本地 Windows | Botyun Linux |
| --- | --- | --- | --- |
| B01 | 匿名 `/login` 返回 200 | 通过 | 通过 |
| B02 | 匿名访问受保护配置页返回 303 `/login` | 通过 | 通过 |
| B03 | 未知路径返回 404 | 通过 | 通过 |
| B04 | 管理员真实凭据登录并进入 `/monitor` | 通过 | 通过 |
| B05 | 仪表盘管理页保持 `custom-dashboard-admin` 浅色作用域 | 通过 | 通过 |
| B06 | 仪表盘监控页使用深色背景、渐变卡片、20px 圆角 | 通过，5 张卡片 | 通过，1 张 QA 卡片 |
| B07 | 匿名公开仪表盘返回 200，并使用深色展示样式 | 通过 | 通过 |
| B08 | 从监控页经侧栏 PJAX 进入文件页，快捷访问条目编辑按钮可用 | 通过 | 通过 |
| B09 | 编辑抽屉父节点为 `body`，左/上为 0，右/下贴合视口 | 通过 | 通过 |
| B10 | 快捷访问条目移动到另一共享分组返回 200 | 通过 | 通过 |
| B11 | 分组保存后 `/monitor` 仍返回 200，未发生 SQLite 锁死 | 通过 | 通过 |
| B12 | 测试后恢复条目原分组 | 通过 | 通过 |
| B13 | 浏览器 page error 与 console error 为 0 | **失败：CSP console error** | **失败：CSP console error** |
| B14 | 本地/公网入口、HTTPS 证书和最终服务存活 | 本地 200 | HTTPS 200，证书验证 0 |

聚焦黑盒统计：本地 **5 通过、1 失败**；Botyun **5 通过、1 失败**。失败均为同一个 CSP 问题，业务功能断言均通过。

## 白盒测试条目

| 编号 | 条目 | 结果 |
| --- | --- | --- |
| W01 | `TestFileQuickAccessCanMovePinIntoSharedGroup`：真实单连接 SQLite 应用入口内创建分组、固定文件并保存分组 | 通过 |
| W02 | `TestFilesPageOffersCollapsedInstanceQuickAccess`：抽屉提升、归还和编辑按钮契约 | 通过 |
| W03 | `TestCustomDashboardCanBeCreatedPublishedAndDeleted`：面板生命周期、公开页、监控页和卡片 | 通过 |
| W04 | 自定义面板出站边界：拒绝 loopback，重定向前不转发凭据 | 通过 |
| W05 | `go test -p 1 ./... -count=1` | 通过 |
| W06 | `go vet ./...` | 通过 |
| W07 | `node --check` 检查 `app.js` 与 Chromium Gate 脚本 | 通过 |
| W08 | Linux amd64、`CGO_ENABLED=0` 构建 Web/Broker/Runner/Updater | 通过 |
| W09 | 完整 `integration/browser` Chromium Gate | **失败：旧契约要求实体 `backgroundColor`，新渐变背景计算为透明** |
| W10 | Botyun 上传前后与安装后四组件 SHA-256 一致 | 通过 |
| W11 | Botyun schema、WAL、SQLite integrity、执行器、密钥与服务 Doctor | 通过；schema 62 |
| W12 | Botyun Web/Broker/Runner Socket active/enabled，部署后 error journal 为 0 | 通过 |
| W13 | 两份文档提交与对应修复提交、历史部署结果一致 | 通过 |
| W14 | `34db14a` 自带仪表盘报告与实际提交/浏览器结果一致 | **失败：报告标注被测提交为 `2280776`，且只收集 pageerror，未收集 CSP console error** |

## 已确认问题

### 1. 仪表盘 SVG 内联样式违反 CSP

`internal/web/ui/templates/custom-dashboard.html` 新增：

```html
<svg width="0" height="0" style="position:absolute" ...>
```

应用 CSP 未允许内联样式，Chromium 在管理页、监控页和公开页稳定报告 `Applying inline style violates ... Content Security Policy`。视觉主体仍能显示，但“浏览器错误为 0”的门禁不成立。

### 2. 仪表盘浏览器契约未随渐变背景更新

`34db14a` 将卡片改为双层渐变背景。现有 `custom-dashboard-drawer-contract.cjs` 仍断言：

```text
backgroundColor === rgb(16, 19, 32)
```

实际 `backgroundColor` 为 `rgba(0, 0, 0, 0)`，颜色由 `backgroundImage` 中的第一层 `rgba(16, 19, 32, 0.92)` 提供。应明确选择增加实体背景色回退，或更新契约为同时验证渐变层和可见底色。

## Botyun 部署与白盒状态

| 项目 | 结果 |
| --- | --- |
| Current | `/opt/scriptboard/current -> /opt/scriptboard/versions/0.0.1` |
| Version | `dev-34db14a` |
| State Root | `/var/lib/scriptboard/state-v20` |
| Schema | 62 |
| 本地监听 | `127.0.0.1:8787` |
| 公网入口 | `https://server-test.karen.fan` |
| State 备份 | `/root/.scriptboard-backups/state-v20-predeploy-dev-34db14a-20260831T023616Z` |
| 二进制备份 | `/opt/scriptboard/deploy-backups/0.0.1-pre-34db14a-20260831T023616Z` |
| 上传 staging | `/root/scriptboard-deploy-34db14a` |

四组件 SHA-256：

| 文件 | SHA-256 |
| --- | --- |
| `scriptboard` | `10f5557e8c5752cb61952d14ed0c407f5da0326451c79df7b9a0192c21fc83f1` |
| `scriptboard-broker` | `264233d0179c2e9366b7328ff214845520a548c9be418ad45d544153369e4896` |
| `scriptboard-runner` | `38d858698c6efb0d7da228f9970200ac38da9feda92c0b020d4e69d82c7df6fa` |
| `scriptboard-updater` | `7a6eb367d413a8f7295e4ee4a3f0874c6b162bbcd0aa8eb084d6b409b46363e3` |

## 保留测试数据与证据

- 本地地址：`http://127.0.0.1:18869`，原丰富数据 State Root 继续运行。
- Botyun QA 面板：`QA Recent Visual 34db14a`，公开 slug `qa-recent-visual-34db14a`，含 `DNS TTL` 数值卡片。
- Botyun 文件快捷访问新增并保留 `/opt/qa-hostfiles-2b15adc/qa-script.sh`；分组测试后恢复为未分组。
- 聚焦探针：`.scratch/deploy-botyun-34db14a/recent-blackbox.cjs`。
- 本地结果：`.scratch/deploy-botyun-34db14a/local-recent-blackbox-results.json`。
- Botyun 结果：`.scratch/deploy-botyun-34db14a/botyun-recent-blackbox-results.json`。
- 截图：`.scratch/deploy-botyun-34db14a/botyun-dashboard-monitor.png`、`botyun-files-after-group-save.png`。

本地部署与 Botyun 当前部署、备份、staging 和测试数据均保留。

# v2.4.7 之后 HEAD 的 Botyun 远程重新部署测试报告

测试时间：2026-08-28（Asia/Shanghai）  
SSH 目标：`botyun`  
公网地址：`https://server-test.karen.fan`  
最终被测提交：`a7adcf7e7aac238c2c69bcc94ee92ba059b70c4d`

## 结论

- Botyun 先从 `dev-e3d47ac`（schema 54）升级到 `dev-2b15adc`，随后因测试期间 `dev` 新增产品修复提交，再升级到最终 `dev-a7adcf7`（schema 62）。Web、Broker、Runner Socket 均为 `active`、`enabled`，部署继续运行。
- 旧 `scriptboard-ai.socket` 已停用并移除，Web unit 已改为当前 Web + Broker + Runner 三组件依赖。
- Doctor 全项 `[OK]`：SQLite integrity、WAL、schema 62、外部密钥/checkpoint、执行器、端口和 systemd 状态均正常。
- HTTPS + 外部 Playwright Chromium 黑盒测试为 **26 项通过、2 项失败**。两项失败来自同一个真实 Linux/Broker 重命名缺陷：普通重命名将“不存在的新目标”错误视为 HTTP 400；失败期间源文件未改变，没有部分写入。
- Linux POSIX 权限真实写入、同名上传冲突 Rename、共享分组、变量、明文 HTTP 本机网站监控、快捷执行二次确认和实际 Shell Run 均通过并保留数据。
- 最终恢复启动后 systemd error 级别日志为 0；公网 HTTPS 证书验证通过。
- 当前 HEAD 的 `go test ./...`、`go vet ./...` 与仓库外部 Chromium 浏览器门禁 `npm test` 全部通过。

## 部署信息

| 项目 | 结果 |
| --- | --- |
| 安装槽位 | `/opt/scriptboard/versions/0.0.1` |
| Current | `/opt/scriptboard/current -> versions/0.0.1` |
| State Root | `/var/lib/scriptboard/state-v20` |
| 配置 | `/etc/scriptboard/config-hostfs.yaml` |
| 登录用户 | `admin`；密码仍仅位于远端 State Root 私有文件，未写入报告或结果 |
| 版本 | `dev-a7adcf7` |
| Commit | `a7adcf7e7aac238c2c69bcc94ee92ba059b70c4d` |
| Schema | 62，由原 schema 54 启动迁移 |
| 本地监听 | `127.0.0.1:8787` |
| 公网入口 | Nginx HTTPS `server-test.karen.fan` |
| 服务 | `scriptboard.service`、`scriptboard-broker.service`、`scriptboard-runner.socket` 均 active/enabled |
| 退役服务 | `scriptboard-ai.socket` inactive/not-found，unit 已移除 |

部署产物上传前后 SHA-256 一致：

| 文件 | SHA-256 |
| --- | --- |
| `scriptboard` | `4f96d0ac0d50da90e87c8ebbd99258484555bee584181167cb4018e94f1973d2` |
| `scriptboard-broker` | `7c477680940aed010cfdae0f0ca9edbf9e51b3db2501e314eee34105c64a1af7` |
| `scriptboard-runner` | `ed361c6b1de76938687378b486a0485ea03daa6f5c95a690ed9d625947b1c083` |
| `scriptboard-updater` | `622d2d79e0b3b92861cd8b367a3dff57a5dc996ec0a795a1db240ee9d844913e` |

## 可恢复备份

部署前先停止组件，随后复制一致性备份：

- State Root：`/root/.scriptboard-backups/state-v20-predeploy-dev-2b15adc-20260828T055539Z`（约 20 MB）
- 二进制、Updater 与 systemd units：`/opt/scriptboard/deploy-backups/0.0.1-pre-2b15adc-20260828T055539Z`（约 121 MB）
- 二次升级前 State Root：`/root/.scriptboard-backups/state-v20-predeploy-dev-a7adcf7-20260828T061412Z`
- 二次升级前二进制、Updater 与 systemd units：`/opt/scriptboard/deploy-backups/0.0.1-pre-a7adcf7-20260828T061412Z`

两轮备份及远端上传 staging `/root/scriptboard-deploy-2b15adc`、`/root/scriptboard-deploy-a7adcf7` 均保留。

## HTTP 与浏览器黑盒结果

| 范围 | 测试条目 | 结果 |
| --- | --- | --- |
| 公网入口 | HTTPS 登录页 200；HTTP GET 不提供明文应用页面（403），HTTP HEAD 由 Nginx 301 至 HTTPS | 通过 |
| 鉴权 | 匿名快捷执行页 303 `/login`；真实管理员凭据登录 | 通过 |
| 安全响应 | CSP、`nosniff`、Frame 防护、HSTS | 通过 |
| MCP/OAuth | 未认证 MCP 401 challenge；两组元数据绑定规范 HTTPS 地址；PKCE S256 | 通过 |
| DCR | 创建无 secret 的公开客户端并保留；拒绝 `client_secret_basic` | 通过 |
| 共享分组 | 创建并保留 `QA Botyun Shared Group a7adcf7`；快捷执行、计划、变量、文件快捷访问、网站监控五页共享显示 | 通过 |
| 变量 | 创建归组变量 `QA_BOTYUN_VALUE=retained-botyun-head-value` | 通过 |
| 连接兼容 | 创建归组 local-scope 网站监控，目标为明文 `http://127.0.0.1:8787/login` | 通过 |
| Linux 权限 | 对 `qa-script.sh` 真实写入 owner execute，回读模式 `0744` | 通过 |
| 文件重命名 | `conflict-source.txt` → `retained-renamed-source.txt` | **失败：HTTP 400** |
| 上传冲突 | 同名上传弹窗选择 Rename，保留 `retained-upload.txt` 与 `retained-upload (2).txt` | 通过 |
| 快捷执行 | 创建开启二次确认的 `QA Botyun Confirmed Run a7adcf7` | 通过 |
| 运行确认 | 取消不启动；确认后生成真实 Run，Shell 最终状态 `succeeded` | 通过 |
| 操作菜单 | 长菜单滚动到底且位于 Top Layer | 通过 |
| 折叠偏好 | 共享分组折叠刷新后恢复 | 通过 |
| 合并校验 | 非法小写变量名被客户端约束阻止 | 通过 |
| 移动端 | 390 × 844 的 Overview、Quick Runs、Website Monitoring 无横向溢出 | 通过 |
| 浏览器异常 | page error 为 0；重命名失败产生一条 HTTP 400 console resource error | **失败** |

结构化结果：`.scratch/deploy-botyun-a7adcf7/botyun-blackbox-results.json`  
外部浏览器脚本：`.scratch/deploy-botyun-a7adcf7/botyun-blackbox.cjs`  
截图：`.scratch/deploy-botyun-a7adcf7/botyun-quick-runs.png`

## 白盒回归

| 测试 | 结果 |
| --- | --- |
| `go test ./...` | 通过；包含权限解析与精确范围、Broker 校验、共享分组 schema 60→61 冲突 fixture、MCP Ledger 冲突释放等回归 |
| `go vet ./...` | 通过 |
| `npm test`（`integration/browser`） | 通过；全部契约测试及外部 Chromium desktop gate 通过 |

Windows ACL 的 `files` / `folders` / `children` 细分无法在 Linux 远端执行真实 ACL 写入；该部分由 Windows 本地部署验收和跨平台白盒测试覆盖。远端则完成 Linux POSIX 权限真实写入与 Broker 回读。

## 已确认缺陷

在 Linux Broker 模式下打开 `/resources/files/rename`，将：

```text
/opt/qa-hostfiles-2b15adc/conflict-source.txt
```

重命名为同目录尚不存在的 `retained-renamed-source.txt`，Web 向 `/resources/files/move` 提交后返回：

```text
无法检查移动目标：stat .../retained-renamed-source.txt: file does not exist:
privileged Broker host_files_not_found: Host Files path does not exist
```

预期行为是把目标 `not found` 解释为“无冲突”，继续执行重命名；实际行为把它当成请求错误并返回 HTTP 400。源文件仍存在，内容和权限没有改变。Windows 本地便携链路此前通过，说明缺口位于 Web 对 Broker `host_files_not_found` 的跨边界解释或对应 Linux/Broker 回归覆盖。

## 保留测试数据

主要远端测试目录：`/opt/qa-hostfiles-2b15adc`

```text
qa-script.sh                 mode 0744
conflict-source.txt          重命名失败后保留
retained-upload.txt
retained-upload (2).txt
```

应用数据库中继续保留共享分组、变量、网站监控、DCR 客户端、快捷执行、实际 Run 与审计事件。最初放在 `/var/tmp/scriptboard-qa-2b15adc` 和 `/tmp/qa-hostfiles-2b15adc` 的探针数据也未删除；这些目录因 world-writable ancestor 安全边界不用于最终 Host Files 验收。

## 补充说明

- 二次升级首次启动暴露了一个部署兼容问题：遗留的空目录 `state-v20/inbox/host-files-broker` 为 `root:root`，新 Web 进程无法清理废弃 inbox。确认目录为空且已纳入二次状态备份后，仅用 `rmdir` 删除两个空目录并启动成功；全部业务及测试数据未删除。
- `scriptboard audit verify` 作为独立 CLI 在该提交级开发构建占用正式 `0.0.1` 安装槽位时，无法同时跨越 Web 与外部 root-owned checkpoint/credential 边界；root 调用拒绝 credential owner，`scriptboard-web` 调用拒绝外部 checkpoint。Doctor 经受管依赖检查两类材料均为 `[OK]`。这与先前提交级开发部署不被正式安装元数据认可的限制一致，不记为本轮 HTTP/浏览器失败。
- 当前部署不是带 Tag、签名清单和 provenance 的正式 Release，因此不能替代 release workflow 的完整门禁。
- 本轮只部署和测试，未修改重命名产品代码；缺陷证据与失败状态如实保留。

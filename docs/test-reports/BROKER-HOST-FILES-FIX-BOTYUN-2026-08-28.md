# Broker Host Files 修复与 Botyun 部署测试报告

测试时间：2026-08-28（Asia/Shanghai）  
分支：`codex/fix-broker-files`  
产品修复：`89b3ebf`  
远端测试版本：`404519ffca1eb4e22901e2ffc26ffdeec581e500`（`dev-404519f`）  
地址：`https://server-test.karen.fan`

## 结论

- Linux Broker 普通文件重命名已修复：不存在的新目标不再被误判为 HTTP 400，真实重命名返回 303 并保留目标文件。
- 受管 Web 不再删除 Broker-owned `inbox/host-files-broker`；退役的 `inbox/uploads` 改由 root Broker 清理。
- Botyun 连续两轮停止 Web、重启 Broker、再启动 Web 均成功，交换目录保持 `root:root`，没有再次出现权限启动失败。
- 外部 Playwright Chromium + HTTP 黑盒最终 **28/28 通过**，console error 和 page error 均为 0。
- 全仓 `go test ./...`、`go vet ./...`、浏览器契约和 Chromium desktop gate 全部通过。
- 当前部署继续运行；全部既有和本轮新增测试数据均保留。

## 修复内容

1. 在 Web Host Files 边界集中使用 `errors.Is(err, os.ErrNotExist)`，正确识别 Broker 传输层包装后的缺失路径。
2. 移动、重命名、回收站恢复、文件冲突、外部接口上传/日志和 HTTP 状态映射统一使用该判断。
3. Web 仅在便携模式清理自己拥有的退役 `inbox/uploads`，不再删除整个 `inbox`。
4. 受管模式由 Broker 清理退役上传目录并创建、保留 `inbox/host-files-broker`。
5. 新增 Broker-backed HTTP 重命名回归、Web 保留交换目录回归和 Broker 精确清理路径回归。
6. 当前 `dev` 新增第三个文件页标题操作后，同步 Chromium 门禁数量断言，保留等宽和 44px 触控尺寸检查。

## 红灯与转绿证据

修复前定向测试稳定失败：

- `TestManagedMoveTreatsBrokerNotFoundAsAvailableDestination`：HTTP 400，错误为 `host_files_not_found`。
- `TestManagedWebPreservesBrokerHostFilesExchangeRoot`：Web 启动删除 `host-files-broker` sentinel。

修复后：

| 测试 | 结果 |
| --- | --- |
| `TestManagedMoveTreatsBrokerNotFoundAsAvailableDestination` | 通过 |
| `TestManagedWebPreservesBrokerHostFilesExchangeRoot` | 通过 |
| `TestPrepareBrokerHostFilesStagingRootRemovesOnlyRetiredUploads` | 通过 |
| `go test ./...` | 通过 |
| `go vet ./...` | 通过 |
| `npm test`（复用主工作区 Playwright runtime） | 通过 |
| `git diff --check` | 通过 |

## Botyun 部署

| 项目 | 结果 |
| --- | --- |
| 版本 | `dev-404519f` |
| Commit | `404519ffca1eb4e22901e2ffc26ffdeec581e500` |
| Schema | 62 |
| Web / Broker / Runner Socket | active、enabled |
| Doctor | 全项 `[OK]` |
| HTTPS | 200，证书验证结果 0 |
| systemd error 日志 | 最终启动后 0 条 |
| 部署前 State 备份 | `/root/.scriptboard-backups/state-v20-predeploy-dev-404519f-20260828T071750Z` |
| 部署前二进制备份 | `/opt/scriptboard/deploy-backups/0.0.1-pre-404519f-20260828T071750Z` |
| 上传 staging | `/root/scriptboard-deploy-404519f`，保留 |

部署产物 SHA-256：

| 文件 | SHA-256 |
| --- | --- |
| `scriptboard` | `62e4d3fd731c8fe6131e5b69c0065c3496061d232a8e2ee78b47c83a78826ba0` |
| `scriptboard-broker` | `87e419d2ff46e97a73cc0ee75b6d36cd33d51f85ad46e12ba485ad9b6fd5eadf` |
| `scriptboard-runner` | `22ebfef1f50884c5397688bc3521f768e2e82e0e2debc441d6ad955f08e01804` |
| `scriptboard-updater` | `4462233f8df02bbf3745cafba486c57a91982b5720db3a8d83d740bbab78222b` |

## 远端黑盒结果

最终结构化结果：`.scratch/deploy-botyun-404519f/botyun-blackbox-results.json`

覆盖 HTTPS/HTTP、匿名跳转、登录、安全响应头、MCP/OAuth/DCR、共享分组五页面复用、变量、明文 HTTP 本机网站监控、Linux 权限、文件重命名、同名上传 Rename、快捷执行确认与真实 Shell Run、长菜单、折叠偏好、客户端校验及移动端溢出。

原测试脚本曾把成功的 303 重定向按 `response.ok()` 判失败；最终复验改为精确断言 303，并创建全新源文件执行真实重命名，避免复用已成功的数据。

## 保留测试数据

目录：`/opt/qa-hostfiles-2b15adc`

```text
qa-script.sh                      mode 0744
retained-renamed-source.txt       首次修复复验生成
retained-renamed-404519f.txt      最终全新重命名复验生成
retained-upload.txt
retained-upload (2).txt
```

数据库继续保留 `QA Botyun Shared Group 404519f`、网站监控、OAuth 客户端、快捷执行、实际 Runs、既有分组与变量。

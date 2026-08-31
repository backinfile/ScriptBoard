# 主机概览历史曲线自适应分桶本地部署测试报告

测试时间：2026-08-31（Asia/Shanghai）  
测试分支：`dev`  
被测基线：`1842587d93804aecaccc324730a370375c63dc2e`

## 结论

- 主机概览的持久化历史此前无论选择 1h、6h 还是 24h，都逐分钟返回完整指标对象；24h 因此会返回约 1,440 点，并重复携带曲线未使用的文件系统、磁盘和网卡明细。
- 现在按范围使用固定上限的时间分辨率：1h 为 1 分钟、6h 为 2 分钟、24h 为 5 分钟；15m 继续使用内存中的 5 秒实时样本。
- 合并后的平均值按原始 `sample_count` 加权，峰值取桶内最大值，不以简单抽点丢弃尖峰。
- 历史接口只返回概览实际绘制的 CPU、内存、存储、磁盘读写和网络收发 7 个汇总指标。
- 单元回归、全仓 Go 测试、真实 HTTP 和外部 Chromium 验证全部通过；部署保持运行。

## 参考实现调查

- Grafana 使用 `Max data points` 限制每条序列的点数，并根据时间范围和面板宽度计算查询 interval。
- Prometheus 范围查询使用等间隔 `step` 控制查询分辨率。
- Zabbix 对较长时间范围使用保存 average / maximum / minimum 的 trends，而不是持续读取全部原始 history。

本次实现采用相同原则，但保持 ScriptBoard 现有 24 小时保留边界和 SQLite 分钟历史，不扩展为长期监控系统。

## 回归信号

测试夹具写入完整 24 小时、每分钟一条且含设备明细的历史数据，并直接调用真实 `Monitor.Overview` 路径。

| 状态 | 24h 点数 | 序列 JSON | 结果 |
| --- | ---: | ---: | --- |
| 修复前 | 1,440 | 574,273 bytes | 回归测试失败 |
| 修复后 | 288 | 40,609 bytes | 回归测试通过 |

回归同时验证：1h 为 60 点、6h 为 180 点、24h 为 288 点；5 分钟桶的加权平均和峰值正确；未使用的设备明细与进程汇总指标不会泄漏到曲线载荷。

## 本地部署

| 项目 | 值 |
| --- | --- |
| URL | `http://127.0.0.1:18869` |
| 进程 | `scriptboard-host-history-fix2.exe`，PID `50020` |
| 监听边界 | 仅 `127.0.0.1:18869` |
| State Root | `.scratch/local-deploy-richdata-final-20260828/state` |
| 登录用户 | `admin` |
| 登录密码 | 通过 `scriptboard admin reset` 获取，保留在 State Root 私有密码文件中 |
| 二进制 SHA-256 | `92BB5F8655ED6F07C773B428AAFEE0843D686631D53C704DC5F0D48EEE1A1EFF` |
| 浏览器结果 | `.scratch/local-release-v253-20260831/host-history-browser-results.json` |
| 浏览器截图 | `.scratch/local-release-v253-20260831/host-history-24h.png` |
| 标准错误日志 | 0 字节 |

## 测试条目

| 编号 | 测试条目 | 结果 |
| --- | --- | --- |
| T01 | 匿名访问 `/login` 返回 200 | 通过 |
| T02 | 匿名访问 `/monitor` 返回 303 | 通过 |
| T03 | 使用重置后的一次性管理员密码真实登录 | 通过 |
| T04 | `/monitor/data?range=1h` 返回 200，真实历史 57 点 | 通过 |
| T05 | `/monitor/data?range=6h` 返回 200，真实历史 179 点 | 通过 |
| T06 | `/monitor/data?range=24h` 返回 200，真实历史 287 点、199,723 bytes、约 71.30ms | 通过 |
| T07 | 24h 数据仅包含 7 个概览曲线指标 | 通过 |
| T08 | `/monitor?node=local&range=24h` 返回 200 并正确选中 24h | 通过 |
| T09 | 外部 Chromium 完成 10 条 SVG 曲线绘制，单条最多 288 点 | 通过 |
| T10 | 外部 Chromium 数据请求约 75.8ms，从导航到曲线完成约 177ms | 通过 |
| T11 | 浏览器 console error 与 page error | 0 / 0 |
| T12 | `go test ./internal/hoststatus -run TestOverviewHistoryUsesRangeSpecificBoundedSummarySeries -count=1 -v` | 通过 |
| T13 | `go test ./internal/hoststatus ./internal/web -count=1` | 通过 |
| T14 | `go test -p 1 ./... -count=1` | 通过 |
| T15 | `git diff --check` | 通过（仅已有行尾转换提示） |

真实历史点数少于理论上限，是部署期间停止与重启造成的正常采集缺口；接口没有伪造或补齐缺失样本。

## 保留状态

修复后的本地部署、丰富测试数据、结构化浏览器结果和截图均保留，便于继续复核。

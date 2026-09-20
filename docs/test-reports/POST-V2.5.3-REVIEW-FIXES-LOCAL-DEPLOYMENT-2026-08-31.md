# v2.5.3 后代码审查修复本地部署测试报告

测试时间：2026-08-31（Asia/Shanghai）

测试分支：`codex/review-v253-fixes`

## 结论

- 多架构 Registry 索引只有在所有有效平台清单均成功返回完整 descriptor size 时才公开压缩下载大小；任一平台失败时不会把部分结果展示成完整值。
- 宿主历史聚合把每个概览指标的有效样本数保存在原有分钟 JSON 中，跨分钟分桶时按逐指标计数加权；旧数据继续回退到分钟总样本数。
- 逐指标计数属于持久化实现细节，不进入 `MetricValues` 或 `/monitor/data` 响应接口。
- 定向测试、关联包测试、全仓测试、静态检查和真实本地部署 HTTP 验收全部通过。

## 自动化验证

| 项目 | 结果 |
| --- | --- |
| Registry 部分平台失败 tracer bullet | 修复前失败，修复后通过 |
| 宿主历史缺失指标加权 tracer bullet | 修复前返回 `50`，修复后返回约 `9.09` |
| `go test ./internal/registrymonitor -count=1` | 通过 |
| `go test ./internal/hoststatus -count=1` | 通过 |
| Registry、连接、自定义面板、Broker、Web 关联包测试 | 通过 |
| `go test ./...` | 通过 |
| `go vet ./...` | 通过 |
| `git diff --check` | 通过 |

## 本地部署

| 项目 | 值 |
| --- | --- |
| URL | `http://127.0.0.1:18881` |
| 进程 | `scriptboard-review-fixes.exe`，PID `58528` |
| 监听边界 | 仅 `127.0.0.1:18881` |
| State Root | `.scratch/local-deploy-review-v253-fixes-20260831/state` |
| 登录用户 | `admin` |
| 登录密码 | 保留在 State Root 私有文件 `secrets/initial-admin-password` |
| 二进制 SHA-256 | `E11D4B4396E8BA2E4E45DCCD8C2F65C31AA104DF5C4A28486A8E1A037510E7B2` |
| stderr | 0 字节 |

## HTTP 黑盒验收

| 编号 | 测试条目 | 结果 |
| --- | --- | --- |
| B01 | 匿名访问 `/login` | 200，通过 |
| B02 | 匿名访问 `/monitor` | 303 跳转 `/login`，通过 |
| B03 | 未知路由 | 404，通过 |
| B04 | 使用生成的管理员密码真实登录 | 最终落到 `/monitor`，通过 |
| B05 | 认证访问 `/monitor` | 200，且不是登录页 |
| B06 | 认证访问 `/monitor/data?range=1h` | 200，2 个真实历史点 |
| B07 | 认证访问 `/monitor/data?range=6h` | 200，1 个聚合历史点 |
| B08 | 认证访问 `/monitor/data?range=24h` | 200，2 个真实历史点 |
| B09 | 三个历史响应均不包含内部 `counts` 字段 | 通过 |
| B10 | 监听地址与进程核对 | 仅预期进程监听回环端口，通过 |

## 保留状态

部署、State Root、测试数据、二进制和日志均保留在 worktree 的 `.scratch/local-deploy-review-v253-fixes-20260831`，服务保持运行以便继续复核。

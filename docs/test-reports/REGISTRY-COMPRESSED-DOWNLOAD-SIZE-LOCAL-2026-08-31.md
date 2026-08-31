# Registry 镜像压缩下载大小本地部署测试报告

测试时间：2026-08-31（Asia/Shanghai）  
测试分支：`codex/registry-image-download-size`

## 结论

- Registry 卡片已额外显示“下载大小（压缩）”。
- 单平台镜像显示一个压缩下载量；多平台镜像显示各可运行平台的最小—最大范围。
- Docker BuildKit attestation manifest 不计入可运行平台大小。
- Registry 刷新失败时，版本、时间和下载大小作为同一份最近成功快照保留。
- 全仓 Go 测试及本地部署 HTTP 黑盒验收全部通过。

## 测试条目

1. 单平台 manifest 按 `config.size + sum(layers[].size)` 计算压缩下载量。
2. 多平台 index 读取所有可运行子 manifest，并生成大小范围。
3. attestation manifest 不参与大小范围计算。
4. descriptor 缺少完整 size 时不展示猜测值。
5. Registry 局部刷新失败时保留最近成功的压缩下载量。
6. 管理与监控页面渲染“下载大小（压缩）”及人类可读单位。
7. 登录页、未知路由、管理员登录、面板与 Registry 卡片创建基础访问。
8. 最终部署只监听指定回环地址。

## 自动化验证

| 命令 | 结果 |
| --- | --- |
| `go test ./internal/registrymonitor ./internal/customdashboard ./internal/web` | 通过 |
| `go test ./...` | 通过 |
| `git diff --check` | 通过 |

## 本地部署

| 项目 | 值 |
| --- | --- |
| ScriptBoard URL | `http://127.0.0.1:18873` |
| ScriptBoard PID | `62488` |
| State Root | `.scratch/local-deploy-registry-size-20260831/state-verified` |
| 登录用户 | `admin` |
| 初始密码 | 登录时读取一次性密码；登录后由应用消费，未写入报告 |
| Registry V2 测试地址 | `http://127.0.0.1:18874` |
| Registry PID | `48504` |
| 测试镜像 | `team/api:v2.5.4` |
| manifest 压缩字节数 | `2,622,464`（config 1,024 + layers 2,097,152 + 524,288） |
| ScriptBoard stderr | 0 字节 |
| Registry stderr | 0 字节 |

## HTTP 黑盒验收

| 编号 | 测试 | 结果 |
| --- | --- | --- |
| B01 | 登录页返回 200 并包含登录表单 | 通过 |
| B02 | 未知路径返回 404 | 通过 |
| B03 | 使用一次性管理员密码登录并落到 `/monitor` | 通过 |
| B04 | 创建“Registry 下载大小验收”面板 | 通过；ID `NmX71lPwBFxgJuJN8b_tO-m6` |
| B05 | 创建 HTTP Registry V2 卡片 | 通过 |
| B06 | 监控页显示 `team/api` 和“下载大小（压缩）”人类可读值，且无异常状态 | 通过 |
| B07 | 应用只由预期进程监听 `127.0.0.1:18873` | 通过 |

统计：**7 项通过，0 项失败**。

## 环境说明

首次尝试使用 Docker Hub 作为外部 Registry，但本机代理把域名解析到测试网段 `198.18.0.0/15`，被出站策略按设计拒绝。最终改用独立的本机 Registry V2 模拟服务返回标准 OCI manifest 与 config blob；该服务、最终 ScriptBoard 部署、面板和测试数据均保留。

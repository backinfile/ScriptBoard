# 工作流使用说明

## 创建并执行

进入“工作流 → 编排”，从顶部更多菜单新建工作流。入口定义需要由执行者填写的参数；出口输入字段组成运行结果。双击画布添加节点，拖拽参数端口连接数据，使用方形端口连接执行顺序。画布空白拖动平移，滚轮缩放。选中连线后 Delete/Backspace 删除，拖动选中连线的端点重新连接；Escape 取消，Ctrl/Cmd+Z 撤销。

在右侧“参数”填写本地值或配置，在“说明”查看节点行为。字段支持 string、integer、number、boolean、json；字符串可设置枚举。连接上游后不再使用本地值。固定值节点输出名称保留为 value，类型需与配置的 JSON 值一致。取值路径支持 `entry.version`、`entry.release.tag` 和 `variables.NAME`，不读取密码变量。

点击“保存”后，到“执行”创建 Entry，选择工作流、填写参数和互斥标签。相同工作区或目标环境使用相同标签，如 `workspace:server`、`cluster:qa`。排序字段决定组内卡片顺序。Entry 锁定后仍可运行，编辑前先解锁。保存新版本后需编辑 Entry 并保存以更新版本绑定。

点击“**一键整理**”按依赖从左到右排列整个流程，入口在前、出口在后，独立节点单独摆放。整理保留端口、连线和配置，可一次撤销/重做；“适应画布”显示完整流程。坐标需保存后持久化。计算期间若修改或切换流程，本次结果自动作废。

Entry 抽屉独立配置“运行前二次确认”；Entry 更多菜单的“打开工作流”切换至当前页的编排页签。顶部只保留一次保存操作，校验失败不覆盖已有版本。

## 自定义节点

“自定义节点”展示已保存卡片；点击右下角“…”打开配置抽屉，删除操作位于抽屉底部。脚本正文、输入输出定义和版本保存到 ScriptBoard 服务端。修改脚本产生新版本，已有编排实例继续使用原版本。

Python 使用：
```python
def main(inputs):
    print("开始构建")
    return {"version": inputs["version"]}
```

JavaScript 使用：
```javascript
export async function main(inputs) {
  return { version: inputs.version };
}
```

Shell/PowerShell 从 `SCRIPTBOARD_INPUT_FILE` 读取 JSON，并向 `SCRIPTBOARD_OUTPUT_FILE` 写入结果 JSON 对象。输出字段须符合节点定义；普通 stdout/stderr 是日志，不解析为结果。

## 主机与节点配置

执行主机为 ScriptBoard 所在主机；进程在浏览器关闭后继续运行。所有执行节点需要 Python 3；可在 ScriptBoard 的执行器链中为 .py 指定 Python 路径。其他工具需可从执行服务的 PATH 访问，或在节点环境变量 JSON 中指定 PATH/JAVA_HOME 等。环境配置会随定义保存，敏感凭据应使用主机配置文件。

| 节点 | 主要参数与结果 |
| --- | --- |
| Git | repository、branch、directory；输出 sourceDir、sourceCommit |
| 本地脚本 | script 绝对路径、arguments JSON 数组、directory；按配置输出 JSON 字段 |
| Maven | directory、pom、goals 数组、profiles、skipTests；输出 target 下的 JAR artifactPath |
| Go | directory、package、binary、goos、goarch、cgo、ldflags；输出 binaryPath |
| 制作镜像 | context、dockerfile、image、platform、buildArgs 对象、cache；输出 imageRef、imageId |
| 推送/拉取镜像 | image、可选 dockerContext/dockerConfig；推送返回 digest |
| k3d | cluster、manifest、namespace、importImage；可指定 deployment/image/container 并等待 rollout |
| k3s | kubeconfig、context、server、namespace、manifest；可指定 deployment/image/container |
| 条件 | condition 分别触发 true / false 出口，另一侧跳过 |

Maven/Go 的 version 输出来自显式配置；需要业务计算的实际版本时，由业务脚本返回 resolvedVersion 并通过连线传递。部署 endpoint 为显式配置值。平台不从候选版本号猜测实际发布版本。

工作目录、构建产物与工具缓存保留。脚本正文以服务端版本为准，执行时可临时物化；临时文件不是自定义节点的持久存储位置。

Git 支持协议本身允许的 HTTP/HTTPS/SSH。k3s 可使用 HTTP 或 HTTPS server；skipTLSVerify 明确控制跳过证书验证。跳过验证会增加中间人攻击风险。Docker 使用选定 daemon 的 Registry 连接策略和选定 config 目录的凭据，不自动修改证书、协议或系统配置。

## 运行、日志和恢复

运行历史记录参数快照、步骤状态、进程 ID 和结构化结果。日志抽屉定期更新最近 1000 条事件，完整日志可下载。进程超时与内存限制沿用现有执行器机制。

默认失败不重试；节点可配置 1～10 次总尝试、固定或指数退避间隔。重试不会回滚已完成的外部操作。取消请求停止执行进程树；已完成的部署、上传等外部操作不会撤销。服务重启后未确认结束的运行标记为 needs_attention，并保留互斥资源；核查实际进程和环境后，由维护者点击释放资源。

## HTTP API

API 使用 ScriptBoard 现有登录会话。先访问 /workflow 取得页面 data-csrf，写请求发送 `X-CSRF-Token` 和 `Content-Type: application/json`。观察者可读取，操作者可运行/取消，维护者可编辑/保存/核查。

| 请求 | 用途 |
| --- | --- |
| GET /workflow/state | 定义、Entry 及最近 200 次运行；按角色隐藏脚本正文 |
| GET /workflow/runs/{id} | 按运行 ID 查询快照、状态、步骤和结果 |
| GET /workflow/logs/{processId} | 最近 1000 条进程日志，含 sequence/source/text |
| GET /history/runs/{processId}/events?after={sequence} | 复用进程日志 SSE，断线后按序号继续 |
| GET /history/runs/{processId}/download | 下载完整日志 |
| POST /workflow/start | `{"id":"ENTRY_ID"}`，异步返回运行 ID |
| POST /workflow/cancel | `{"id":"RUN_ID"}` |
| POST /workflow/resolve | 核查后释放 needs_attention 运行资源 |
| POST /workflow/save | 校验并原子保存 Definition 与可运行版本，编辑时携带 id/revision |
| POST /workflow/draft | 兼容旧客户端：仅保存编辑定义 |
| POST /workflow/publish | 兼容旧客户端：`{"id":"WORKFLOW_ID","revision":1}` |
| POST /workflow/custom | 保存脚本定义，编辑时携带 id/revision |
| POST /workflow/entry | 保存 Entry、工作流版本、参数、分组、排序、资源标签与 confirm 二次确认开关 |
| POST /workflow/entry-lock | `{"id":"ENTRY_ID","revision":1,"locked":true}` |
| POST /workflow/delete | `{"kind":"entry","id":"ENTRY_ID","revision":1}`；kind 可为 entry/custom/draft |

运行只接受已保存 Entry ID。版本冲突或锁冲突返回 409，字段/图校验失败返回 400，缺失记录返回 404。调用者应保留返回的运行 ID 查询结果，不对写操作自动重试。

## 流程控制与计算

- 算术：加、减、乘、除、取余、幂、最小值、最大值、绝对值、向下/向上取整、四舍五入（半数远离零）。输入 a、b，输出 result；一元运算仅使用 a，除零与非有限结果进入失败出口。
- 比较和布尔逻辑：严格保留值类型；支持相等、大小、文本包含/前缀/正则，以及 AND、OR、NOT、XOR。比较结果可以接条件或按值分支。
- 按值分支：选择 boolean、integer、number、string 或 json，可读取对象字段路径，按首条或全部规则匹配。支持范围、存在/空值判断；未命中走“其他情况”，类型错误走失败。integer 限定为 JavaScript 安全整数；枚举值按字符串等值规则处理。
- 普通节点提供成功、失败、完成三个控制出口；完成表示成功或失败，不包括取消和监督不确定。失败的 `_error` JSON 输出包含节点 ID、名称、消息及尝试次数。
- 分支汇合默认接控制线：任一模式适用于互斥分支，全部模式要求每条入线均触发。它会等待前驱状态确定，未命中分支不会卡住运行。数据输入仍要求来源实际产出，不会用默认值替代跳过分支的数据。
- 等待节点支持 0～86400 秒，可取消。“结束流程”立即跳过剩余节点并设置整次运行状态。处理失败分支后整体仍为失败，只有明确结束为成功才恢复。
- 重试默认关闭（总尝试 1 次），可配置最多 10 次，初始间隔 0～60000 毫秒；指数退避最高 60 秒。已确认失败才重试，取消和监督不确定不会重试。有副作用的任务须自行保证可重复执行。每次尝试保留独立日志。

示例：固定值 → 算术 `7 × 2` → 按值分支 `result > 10` → 对应处理 → 任一汇合 → 出口。所有命中路径按拓扑顺序执行；当前不执行并发或循环。

## 等待节点

“等待固定时间”填写 seconds（支持小数，0～86400 秒），等待期间可取消。

“等待节点完成”在参数面板点击“选择等待节点”，勾选同一流程中的一个或多个节点。可选择执行完成（成功或失败）或执行成功，保存后在画布显示等待控制线，也可直接拖线连接其他节点的成功/完成出口。全部模式要求每条等待条件满足；任一模式要求至少一条满足。未执行的分支算跳过，不算完成。等待关系不能形成环路。

当前流程按拓扑顺序执行，等待节点在全部前驱状态确定后判断继续条件；任一模式用于接受部分有效分支，不会抢跑仍在执行的任务。等待失败节点完成不会自动把整次运行恢复为成功。

## 导入、导出与 AI 编写

编排页的更多菜单提供“导出工作流”和“导入工作流”。导出当前页面配置为 `.workflow.json`，包括引用的自定义脚本版本。导入支持选择文件或粘贴 JSON，先查看摘要与内容，再确认创建新流程；不会覆盖同名流程或自动执行。

“说明”页签可下载 `scriptboard-workflow-ai-guide.md` 和示例 JSON。将指南与需求交给 AI，让其输出完整 JSON 文件，然后导入即可。下载指南包含字段约束、全部内置节点模板、连线/分支/等待规则和可直接导入的示例，模板与当前页面版本一致。

配置包为 `scriptboard.workflow` v1，最多 4 MiB、500 个节点和 100 个自定义版本。自定义脚本保存到 ScriptBoard，引用会映射到新 ID；Entry、运行历史和主机文件不打包。配置值及脚本正文原样导出。导入后检查目标主机路径、工具链和变量，再创建 Entry。

API：`POST /workflow/export` 接收 Definition，返回配置包；`POST /workflow/import` 接收配置包，事务创建新流程及引用脚本；两者限维护者及管理员，使用既有 CSRF 请求头。`GET /workflow/guide` 提供指南基础正文，页面下载时追加当前节点模板与示例。

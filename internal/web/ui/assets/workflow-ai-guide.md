# ScriptBoard 工作流配置编写指南（供 AI 使用）

请根据用户需求生成一个 UTF-8 JSON 文件，文件扩展名为 `.workflow.json`。输出完整 JSON，不带 Markdown 代码围栏、注释或额外解释。用户可在 ScriptBoard 的工作流页面直接导入。

## 文件结构

顶层仅允许：

- `format`：固定为 `scriptboard.workflow`。
- `version`：固定为数字 `1`。
- `workflow`：流程定义。
- `customNodes`：自定义节点版本定义数组；没有自定义节点时填 `[]`。

workflow 包含 `name`、`parameters`、`graph`，可省略 `id`、`revision`。导入始终创建新 ID，不覆盖任何现有工作流。graph 包含 `nodes`、`links`、`dependencies` 三个数组。最多 500 个节点、100 个自定义定义，整个请求不超过 4 MiB。

导入只保存，不执行。用户需另建 Entry，填写执行参数、互斥标签和二次确认开关后运行。配置包不包含 Entry、运行记录、主机文件、凭据文件或环境变量库。不要在文件中写入密码/token；使用用户明确提供的主机凭据引用。路径和工具链必须符合目标执行主机，不能假设本地脚本会随导出迁移。

## 参数 Field

```json
{"name":"version","type":"string","required":true,"source":"local_or_link","default":"dev"}
```

- name：匹配 `[A-Za-z_][A-Za-z0-9_]*`，同一侧不可重复。
- type：`string`、`integer`、`number`、`boolean`、`json`。
- required：布尔值。
- source：输入允许 `local_or_link` 或 `link_only`，可省略（本地或连线）。link_only 不可配置 default。
- default：实际 JSON 值，不能把数字或布尔值写成字符串。json 类型可以接任意 JSON 值。
- enum：可选字符串数组，仅用于 string。
- integer：整数，绝对值不超过 9007199254740991。

连接输入时只读取上游结果，不使用本地值或 default 兜底。必填的普通输入须有连线、values 或 default；不做隐式字符串转换。`_error` 是内置失败输出，不可写入 outputs 定义。

## 节点 Node

```json
{"id":"build","kind":"arithmetic","name":"计算","x":400,"y":100,"inputs":[{"name":"a","type":"number","required":true,"default":7},{"name":"b","type":"number","required":true,"default":2}],"outputs":[{"name":"result","type":"number","required":true}],"values":{},"config":{"operation":"multiply"}}
```

Node 允许：id、kind、name、x、y、inputs、outputs、values、config；自定义节点另有 customId、customRevision。不要写 group、description 或 script 到 Node；说明属于 customNodes。id 在同一图内唯一。x/y 为有限数字，推荐从左到右每列间隔 400，页面还可一键整理。

values 是以输入名称为键的 JSON 对象；只能引用已声明输入。config 是节点专有设置，不能把 inputs 的值混放进去（通用执行配置除外）。必须按本文末尾的内置节点模板声明输入输出名称及类型。

## 入口与出口

整个流程必须恰有一个 `start` 和一个 `end`。

- start：inputs 为 []，outputs 与 workflow.parameters 的名称、类型、required、顺序一致。Entry 提供参数值，缺省时使用参数 default。
- end：inputs 定义流程返回值，outputs 为 []。通过 links 将结果连到 end 的输入。
- 无入口参数时 parameters 和 start.outputs 均为 []。

## 数据与控制连线

```json
{"from":{"node":"build","field":"result"},"to":{"node":"end","field":"result"}}
```

上述对象放在 graph.links。一个输入仅有一个数据来源；数据连线自动形成执行依赖。类型必须一致，integer 可接 number，任意类型可接 json。json 输出不能直接接 string/number，需通过自定义节点输出明确类型。

```json
{"from":"build","to":"cleanup","outlet":"always"}
```

上述对象放在 graph.dependencies。outlet 可省略（按 success）；普通节点有 success、failure、always。always 表示已确认成功或失败，不包括跳过、取消和监督不确定。推荐只对控制顺序或分支添加依赖，不要给失败处理节点同时添加同一来源的 success 和 failure。

图必须无环，不能自连接。普通节点要求所有控制入线都激活；未激活路径显示 skipped。没有依赖的节点可能独立执行，不会因摆放位置靠后而等待。

## 流程控制与计算

- arithmetic：输入 a、b 为 number，result 为 number。operation 支持 add/subtract/multiply/divide/modulo/power/min/max/abs/floor/ceil/round；后四个只用 a。除零、溢出和非有限结果失败；round 半数远离零。
- compare：a、b 为 json，result 为 boolean。operation 为 eq/ne/gt/gte/lt/lte/contains/starts_with/matches；大小比较需要数字，文本操作需要字符串，不转换类型。
- logic：a、b 为 boolean，result 为 boolean；operation 为 and/or/not/xor，not 只用 a。
- condition：condition 输入为 boolean，matched 输出为 boolean。使用 true、false 控制出口选择分支。
- switch：value 输入为 json，matched 输出为 json 数组，value 输出为可选 json。config.valueType 为判断类型，matchMode 为 first 或 all，path 可选对象路径（如 result.code）；rules 是规则数组。每条规则含稳定且唯一的 id、name、operator，可有 value/upper。operator 支持 eq/ne/gt/gte/lt/lte/between/contains/starts_with/matches/exists/missing/is_null/not_null。value 与 valueType 一致；between 使用数字闭区间 value..upper，正则使用 Go RE2 语法。exists/missing/is_null/not_null 不填比较值，null 判断用 json 类型。规则 id 是控制出口，禁止 success/failure/always/default；未命中触发 default，类型错误走 failure。
- merge：无固定数据端口，config.joinMode 为 any（默认）或 all，连接上游控制出口汇合分支。
- wait：输入 seconds 为 number，0～86400，可含小数；可取消。
- wait_for：至少一条控制入线，config.joinMode 为 all（默认）或 any。等待完成用 always，等待成功用 success，不能循环等待。
- terminate：config.status 为 succeeded 或 failed，message 为说明。立即结束流程，剩余节点跳过；显式 succeeded 可恢复之前失败。
- literal：无输入，唯一输出 value，config.value 是实际 JSON 值，必须符合输出类型。
- lookup：无输入，唯一输出 value；config.path 为 entry.x 或 variables.NAME。变量为非密码变量；导入不创建变量。

失败节点自动产出 `_error`（json：nodeId/nodeName/message/attempts）。失败处理节点可用 links 接收 `_error`。连接 failure 或 always 允许处理后继续，但不会隐式将整次运行变为成功。

节点 config 可设 retryAttempts（总次数 1～10，默认 1）、retryDelayMs（0～60000）、retryBackoff（fixed/exponential，上限 60 秒）。仅确认失败才重试，取消和监督不确定不重试。具有部署/推送等副作用的动作必须可重复执行。

当前同一流程按拓扑顺序执行，不支持循环和节点并发。merge/wait_for 在所有前驱状态确定后判断 any/all；any 不会抢跑仍在执行的任务。

## 脚本与构建部署

内置脚本 kind=script 的 script 输入是目标主机已有文件路径，arguments 为 JSON 数组。节点 config 可设 directory（工作目录）、timeout（秒，默认 1800）、memory（如 512M）、environment（字符串键值对象）。本地文件不打包；希望随包迁移的代码请使用 custom。

Git、Maven、Go、镜像、Kubernetes 节点使用本文末尾的模板。所有外部执行节点需要目标主机的 Python 和对应工具链。HTTP/HTTPS/SSH 仓库协议保持用户提供的值，skipTLSVerify 默认 false；显式跳过证书校验会有中间人攻击风险。Docker 凭据/连接策略由主机 daemon/context/config 管理，不能编造凭据。

## 可移植自定义节点

customNodes 中每个定义包含 id（文件内逻辑标识）、revision（正整数）、name、description、language、code、inputs、outputs。language 为 python/javascript/shell/powershell。digest 可省略，导入会重新计算。

Node 用 kind=custom、customId、customRevision 引用该定义，inputs/outputs 必须与引用定义完全一致。每个引用版本必须随包提供。相同 id/revision 在包内只能定义一次；导入时新建 ScriptBoard 节点并重映射引用，不需要知道目标数据库 ID。

Python 实现 `def main(inputs):` 并返回字典；JavaScript 实现 `export async function main(inputs)` 并返回对象。Shell/PowerShell 从 SCRIPTBOARD_INPUT_FILE 读取 JSON、向 SCRIPTBOARD_OUTPUT_FILE 写入结果 JSON。stdout/stderr 是日志，不是输出参数。

## 生成前检查

确认所有节点 ID 与端口引用存在，入口与参数一致，出口有数据来源或默认值，必填项完整，类型匹配，控制分支有明确去向，等待无环，自定义引用完整。返回完整 JSON 文件；如缺少目标主机路径或业务参数，应先向用户询问，不生成假定的凭据或路径。

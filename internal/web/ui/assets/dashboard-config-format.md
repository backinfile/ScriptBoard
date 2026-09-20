<!-- 维护提示：本文件存在两份副本（docs/DASHBOARD-CONFIG-FORMAT.md 与 internal/web/ui/assets/dashboard-config-format.md，后者随二进制内嵌供面板页下载），修改时请同步两处（有测试守护）。 -->
# 自定义面板配置导入导出格式

本文档描述自定义面板「节点配置」JSON 文件的完整格式，供人工编写与 AI 生成使用。文件由面板设置页导出、导入，用于在实例之间迁移卡片（含操作按钮与流程卡片）。

- 导出：面板设置页勾选卡片后导出，文件名形如 `scriptboard-dashboard-nodes-20060102-150405.json`。
- 导入：面板设置页「导入节点」上传该文件，可勾选文件中的部分卡片导入。
- 限制：文件 ≤ 2 MB；卡片数量 1–100；顶层与卡片记录层拒绝未知字段；`config` 内部字段按类型校验（操作按钮拒绝未定义字段）。
- 导出时敏感值（如请求头中的凭据）会被脱敏，迁移后需重新配置。

## 顶层结构

```json
{
  "format": "scriptboard.custom-dashboard-nodes",
  "version": 1,
  "exported_at": "2026-09-15T06:00:00Z",
  "nodes": []
}
```

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `format` | string | 固定为 `scriptboard.custom-dashboard-nodes`，不匹配拒绝导入 |
| `version` | number | 固定为 `1` |
| `exported_at` | string | RFC 3339 时间戳，仅记录 |
| `nodes` | array | 卡片列表，1–100 条 |

## 卡片字段（nodes[]）

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `name` | string | 是 | 卡片名称，1–80 字符 |
| `type` | string | 是 | `number`、`percentage`、`quota`、`key_value`、`website`、`registry`、`flow` |
| `source_url` | string | 按类型 | 数据源地址（website / registry / flow 类型不用） |
| `headers` | object | 否 | 请求头键值对，随 `source_url` 请求发送 |
| `value_path` | string | 按类型 | 主值取值路径 |
| `secondary_path` | string | 按类型 | 次值取值路径（quota 类型必填） |
| `formula` | string | 否 | 展示公式 |
| `config` | object | 否 | 类型私有配置，见下文 actions 与 flow |
| `refresh_seconds` | number | 否 | 刷新间隔秒数；≤0 取 60，下限 15，上限 86400 |
| `website_monitors` | string[] | website | 网站监控名称列表，导入时按名称重映射为本机监控 ID |

## 操作按钮（config.actions）

任何类型的卡片都可以在 `config.actions` 中携带操作按钮，每张卡片最多 8 个。

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `id` | string | 操作 ID；留空由服务端生成 `act_` 前缀 ID |
| `label` | string | 按钮名称，1–40 字符 |
| `kind` | string | `quick_run`（触发快捷执行项）或 `browser_http`（浏览器直发 HTTP 请求） |
| `quickRunId` | string | `quick_run` 必填；本机快捷执行项 ID |
| `quickRunName` | string | 仅导出文件携带；导入时按名称重映射为 `quickRunId` 后删除，不落库 |
| `confirm` | bool | 触发前是否二次确认；`danger` 样式强制为 true |
| `confirmText` | string | 确认文案，开启确认时必填，1–200 字符 |
| `style` | string | `default`、`primary`、`danger` |
| `publicAllowed` | bool | 是否允许在公开面板（公开可操作档）中触发 |
| `method` | string | `browser_http` 必填，`GET` 或 `POST` |
| `url` | string | `browser_http` 必填；绝对 HTTP/HTTPS 地址，不允许用户信息片段与 `#` 锚点 |
| `visitorCredential` | string | `browser_http` 可选；声明访客浏览器提供的请求头名（≤100 字符、无控制字符）。禁止配置任何静态凭据值 |

导入重映射规则：本机 `quickRunId` 仍然有效则保留；否则按 `quickRunName` 匹配本机同名快捷执行项；仍未匹配的 `quick_run` 操作被丢弃（其余操作保留）。`browser_http` 不依赖快捷执行项，不受影响。

## 流程卡片（config.flow）

`type: "flow"` 的卡片以 `config.flow` 描述一个有向无环流程，节点按 `needs` 拓扑执行。

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `nodes[].id` | string | 节点 ID：`^[a-z0-9][a-z0-9_-]{0,39}$`，小写字母或数字开头，仅含小写字母、数字、`-`、`_`，最长 40 字符，不可重复 |
| `nodes[].name` | string | 节点显示名，1–60 字符 |
| `nodes[].needs` | string[] | 依赖的节点 ID；不可引用自身或不存在的节点；整体不可成环 |
| `nodes[].run` | string | 快捷执行项名称；导入时按名称重映射为 `runId` |
| `nodes[].runId` | string | 本机快捷执行项 ID；本机有效则保留，否则按 `run` 名称解析 |
| `nodes[].language` | string | `script` 必填；本平台支持的脚本语言（Windows：`powershell`、`batch`、`python`、`nodejs`；其他平台：`shell`、`python`、`powershell`、`nodejs`） |
| `nodes[].script` | string | 内联脚本源码（≤1 MB，UTF-8 且无 NUL 字符） |
| `nodes[].uses` | string | 内置节点名称，见下方内置节点表 |
| `nodes[].with` | map | 执行参数：键 `^[A-Za-z][A-Za-z0-9_]{0,31}$`，最多 20 键，值 ≤2000 字符 |
| `nodes[].confirm` | bool | 执行该节点前是否二次确认 |
| `nodes[].timeout` | number | 超时秒数，0–86400；0 表示不限制 |
| `post[].name` | string | 收尾步骤名称，1–60 字符 |
| `post[].run` / `runId` / `language` / `script` / `uses` / `with` | | 同节点规则，post 步骤同样支持三种执行器 |
| `post[].when` | string | `on_success`（全部节点成功）、`on_failure`（任一节点失败）、`always`；留空默认 `always` |

每个节点与 post 步骤必须且只能指定 `run`、`script`、`uses` 之一（三种执行器互斥）：

- `run`：快捷执行。`with` 按键名覆写快捷执行项的参数默认值；键是否在目标执行项的参数定义中，保存时不校验，运行时按参数定义兜底校验。参数取值对脚本有两种暴露方式：环境变量 `SCRIPTBOARD_PARAM_<大写名>`，以及快捷执行项启动参数中的变量 `{{PARAM_<大写名>}}`（保存快捷执行项时校验，未定义的参数引用会被拒绝）。
- `script` + `language`：一次性内联脚本。`with` 注入为 `SCRIPTBOARD_PARAM_<大写键名>` 环境变量（如 `target` → `SCRIPTBOARD_PARAM_TARGET`），脚本在该卡片的工作区目录中执行；工作区绝对路径同时以 `SCRIPTBOARD_FLOW_WORKSPACE` 环境变量提供。
- `uses`：内置便利节点，`with` 为下表声明的输入。

内置节点（全部跨平台；相对路径解析到该卡片的工作区目录，仅支持工作区内的相对路径；操作工作区外的主机文件请发布并引用快捷执行脚本）：

| 名称 | with 输入（* 为必填） | 行为 |
| --- | --- | --- |
| `git-sync` | `repo`*、`dest`*、`branch`、`depth` | 克隆或拉取 git 仓库到目标目录 |
| `copy-files` | `from`*、`to`* | 复制文件或目录 |
| `write-file` | `path`*、`content`*、`append` | 写入文本文件，`append` 为真时追加 |
| `make-dir` | `path`* | 创建目录（含父级） |
| `remove-files` | `path`* | 删除文件或目录 |
| `http-request` | `url`*、`method`、`body`、`header_<名>`、`insecure_skip_verify` | 支持 HTTP 和 HTTPS；默认验证证书。显式设为 `true` 可跳过证书验证，存在中间人攻击风险 |
| `sleep` | `seconds`* | 等待指定秒数（0–300） |

节点可配置 `post`，在该节点结束后、下游节点开始前执行；条件基于该节点结果，跳过的节点不执行 post。全局 `post` 在图结束后执行；收尾失败展示在记录中，不覆盖主流程结果。

限额：节点 ≤ 20 个，每组 post 步骤 ≤ 4 个。导入时任一 `run` 步骤的快捷执行项无法按名称匹配，整个文件拒绝导入（DAG 不允许缺节点）；`script`/`uses` 步骤不依赖快捷执行项，导入免映射。

权限与安全：

- 含 `script` 或 `uses` 步骤的卡片，保存与导入都需要执行管理权限。
- 导出时 `http-request` 的 `header_*` 值会被脱敏，迁移后需重新配置。

卡片编辑抽屉中也接受 YAML 草案（保存时解析为上述规范化 JSON，不会保存 YAML）：

```yaml
version: 1
flow:
  nodes:
    - id: build
      name: 构建
      run: 快捷执行项名称
      with: {target: release}
    - id: sync
      name: 同步仓库
      uses: git-sync
      with: {repo: https://example.com/repo.git, dest: repo, depth: "1"}
    - id: deploy
      name: 部署
      needs: [build, sync]
      language: shell
      script: |
        echo deploying
  post:
    - name: 清理
      uses: remove-files
      with: {path: tmp}
      when: always
```

## 端到端示例

纯数据面板（一个数值卡片）：

```json
{
  "format": "scriptboard.custom-dashboard-nodes",
  "version": 1,
  "exported_at": "2026-09-15T06:00:00Z",
  "nodes": [
    {
      "name": "在线人数",
      "type": "number",
      "source_url": "https://api.example.com/stats",
      "value_path": "online",
      "refresh_seconds": 60
    }
  ]
}
```

带操作按钮的卡片（一个快捷执行操作 + 一个浏览器直发操作）：

```json
{
  "format": "scriptboard.custom-dashboard-nodes",
  "version": 1,
  "exported_at": "2026-09-15T06:00:00Z",
  "nodes": [
    {
      "name": "发布状态",
      "type": "key_value",
      "source_url": "https://api.example.com/release",
      "refresh_seconds": 300,
      "config": {
        "actions": [
          {
            "id": "act_deploy",
            "label": "重新部署",
            "kind": "quick_run",
            "quickRunId": "",
            "quickRunName": "部署生产环境",
            "confirm": true,
            "confirmText": "确认触发生产部署？",
            "style": "danger",
            "publicAllowed": false
          },
          {
            "id": "act_approve",
            "label": "审批通过",
            "kind": "browser_http",
            "method": "POST",
            "url": "https://ci.example.com/approve",
            "visitorCredential": "Authorization",
            "confirm": false,
            "style": "primary",
            "publicAllowed": true
          }
        ]
      }
    }
  ]
}
```

流程卡片（构建 → 部署 → 验证，失败后通知）：

```json
{
  "format": "scriptboard.custom-dashboard-nodes",
  "version": 1,
  "exported_at": "2026-09-15T06:00:00Z",
  "nodes": [
    {
      "name": "发布流程",
      "type": "flow",
      "refresh_seconds": 0,
      "config": {
        "flow": {
          "nodes": [
            { "id": "build", "name": "构建", "run": "构建产物" },
            { "id": "deploy", "name": "部署", "needs": ["build"], "run": "部署生产环境", "confirm": true, "timeout": 600 },
            { "id": "verify", "name": "验证", "needs": ["deploy"], "run": "冒烟验证" }
          ],
          "post": [
            { "name": "失败通知", "run": "发送告警", "when": "on_failure" }
          ]
        }
      }
    }
  ]
}
```

说明：`refresh_seconds` 对 flow 卡片仅作展示刷新用；`run`/`quickRunName` 填目标实例上的快捷执行项名称，跨实例迁移时不要填来源实例的 `runId`/`quickRunId`（留空即可）。

## 常见导入错误

| 界面错误码 | 含义 | 排查 |
| --- | --- | --- |
| `file_required` | 未选择文件 | 选择由 ScriptBoard 导出的 JSON 文件 |
| `too_large` | 文件超过 2 MB | 拆分卡片分批导出导入 |
| `invalid` | 文件无法识别 | 检查 `format`/`version`、未知字段、卡片数量（1–100）、名称长度（≤80）、`config` 是否为合法 JSON |
| `selection_required` | 未勾选任何卡片 | 在导入确认页至少勾选一项 |
| `failed` | 卡片配置无效 | 常见原因：流程节点/post 的 `run` 名称在本机无匹配快捷执行项；`quick_run` 操作未绑定快捷执行项；`browser_http` 地址不是绝对 HTTP/HTTPS；`danger` 样式未开启确认；`when` 取值非法；节点 ID 非法或依赖成环 |

流程配置的结构类错误（节点数量、ID、needs、成环、when 枚举）会在保存/导入时逐条列出，按提示逐条修正即可。

内置节点与脚本都生成运行记录，由 Runner 执行。内置 HTTP 可访问 Runner 所在网络（包括内网），仅执行管理者可保存这类配置。公开快捷执行按钮每 4 秒查询状态，密钥撤销或可见性变更立即停止授权。

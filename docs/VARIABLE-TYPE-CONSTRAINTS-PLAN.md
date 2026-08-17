# 变量类型约束计划

## 1. 类型范围

变量只支持五种类型，不提供任何可配置的长度、正则、枚举或数值范围约束：

| 类型 | 接受的值 | 存储方式 |
|---|---|---|
| `text` | 任意 UTF-8 文本 | 原样存储 |
| `bool` | `true` 或 `false` | 小写规范值 |
| `integer` | 带可选负号的十进制整数 | 十进制字符串 |
| `float` | 普通十进制浮点数 | 十进制字符串 |
| `version` | `x.y.z`，三段均为非负十进制整数 | 原样存储 |

示例：

- `text`: `production`
- `bool`: `true`
- `integer`: `-12`
- `float`: `3.14`
- `version`: `1.7.0`

所有类型仅保留现有的通用 4 KiB 值大小硬上限。`integer`、`float` 和 `version` 不提供最小值、最大值等业务范围配置。

`is_password` 继续独立存在，只控制变量页面是否默认遮罩。它不是第六种值类型，不改变明文存储、参数展开或 Run 历史行为。

## 2. 校验语义

- `text`：UTF-8 有效即可，可为空。
- `bool`：只接受 `true`、`false`，不接受 `1`、`0`、`yes`、`no`。
- `integer`：匹配 `-?(0|[1-9][0-9]*)`，不接受小数、指数、前导 `+` 或多余前导零。
- `float`：接受带可选负号的十进制整数或小数；不接受指数、`NaN`、`Inf`、前导 `+`。
- `version`：严格匹配 `x.y.z`；每段使用无前导零的非负十进制整数，数字 `0` 除外。不接受 `v1.2.3`、预发布后缀或构建元数据。

这些规则只回答“值的格式是否属于该类型”，不表达业务范围。

## 3. 模块与接口

新增 `internal/variables` 领域模块，把五种类型的格式校验集中在一个 seam。网页变量管理和 External Interface 复用同一实现，调用方不自行维护正则或解析规则。

接口保持为一个入口：

```go
type Kind string

const (
    KindText    Kind = "text"
    KindBool    Kind = "bool"
    KindInteger Kind = "integer"
    KindFloat   Kind = "float"
    KindVersion Kind = "version"
)

func Parse(kind Kind, raw any) (string, error)
```

`Parse` 负责：

- 检查类型是否受支持。
- 将网页字符串或外部 JSON 标量转成字符串。
- 校验格式并返回可存储的值。
- 返回可映射到具体表单字段的稳定错误。

变量消费者仍只读取 `map[string]string`。Quick Run、Schedule、手动 Run 和网站监控不需要知道变量类型。

## 4. 数据模型与迁移

`variables` 表只增加一个字段：

```sql
value_type TEXT NOT NULL DEFAULT 'text'
    CHECK (value_type IN ('text', 'bool', 'integer', 'float', 'version'))
```

不增加 `constraints_json`，也不增加最小值、最大值或其他约束列。

迁移规则：

1. 所有现有变量自动成为 `text`。
2. 现有 `value` 与 `is_password` 原样保留。
3. 变量引用和 Run 参数协议不变。
4. 提升 schema version，并覆盖旧数据库升级测试。

## 5. 页面行为

### 新建和编辑

- 表单增加类型选择：文本、布尔、整数、浮点数、版本号。
- `bool` 使用 `true / false` 选择框。
- 其余类型使用单值输入框；`version` 提示格式为 `x.y.z`。
- 保存时始终由服务端调用 `variables.Parse`。
- 修改类型时，提交值必须符合新类型；否则拒绝保存。

### 列表

- 列表显示变量类型。
- password 标记继续控制值的默认遮罩。
- 复制名称、复制值及显示/隐藏行为保持不变。

## 6. External Interface

变量自身类型在所有写入路径上生效。External Interface 更新变量时，写入值也必须通过 `variables.Parse`。

本次“无范围约束”仅指变量定义本身不保存范围。External Interface 现有的入站能力限制暂时保留，因为它们用于约束外部调用权限；后续只把基础类型解析委托给 `internal/variables`，不在本次改动中放宽已经发布的外部能力。

## 7. 实施顺序

1. 为 `internal/variables.Parse` 编写五种类型的表驱动测试。
2. 实现类型模块。
3. 增加 `value_type` 字段和数据库迁移测试。
4. 接入变量的新建、编辑和列表页面。
5. 让 External Interface 写入额外执行变量自身类型校验。
6. 补充网页集成测试和各变量消费者的回归测试。
7. 更新 `CONTEXT.md`、数据模型、ADR-0017、README 和验收文档。

## 8. 验收重点

- 旧变量升级后均为 `text`，值、遮罩和引用不变。
- 五种类型在网页保存和外部写入时遵循同一格式规则。
- 不存在范围、长度、正则、枚举等可配置约束。
- 修改类型不会留下不符合新类型的当前值。
- 所有运行入口继续得到字符串参数，参数边界和历史记录不变。

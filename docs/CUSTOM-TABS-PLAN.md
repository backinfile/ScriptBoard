# 自定义页签实施计划

状态：Implemented
调研日期：2026-08-25

## 1. 目标

在“配置”大类中新增“自定义页签”管理页。管理员或维护员可以新增、编辑、排序、启用、停用和删除一个指向本地或受信网络页面的 HTTP/HTTPS 引用。

启用后的引用出现在主导航新增的“外部”大类中。用户点击后进入 ScriptBoard 自己的稳定路由，由页面主体中的全尺寸 `iframe` 打开目标地址，主导航、登录会话和 ScriptBoard 页面框架保持不变。

每个自定义页签支持三种凭据行为：

1. **隔离打开**：不向目标传递 ScriptBoard 身份或静态 Key，也不授予 iframe 使用目标 origin 持久状态的能力。
2. **保留目标站登录状态**：允许目标站使用它自己的 Cookie、Local Storage 等浏览器状态；不复制、转换或暴露 ScriptBoard 会话。
3. **注入 Key**：保存一个目标站专用 Key，通过绑定目标 origin 的 `postMessage` 握手按需交给 iframe；Key 不进入 URL、HTML、审计、日志或 Referer。

“保留登录状态”只表示保留被嵌入目标自身的浏览器登录状态，不表示把 ScriptBoard 的会话 Cookie、CSRF Token 或原始 session token 注入第三方页面。

## 2. 产品范围

### 2.1 本次包含

- 配置入口：`GET /config/custom-tabs`。
- 新建、编辑、排序、启用、停用、删除自定义页签。
- 字段：名称、绝对 HTTP/HTTPS URL、启用状态、凭据模式、可见角色、可选 Key 名称与 Key。
- 启用项按配置顺序动态加入“外部”导航大类。
- 访问入口：`GET /defined/tabs/{id}`。
- iframe 全尺寸展示、加载提示、配置说明及“在新窗口打开”备用入口。
- HTTP 和 HTTPS 地址都可显式保存；HTTP 页面明确提示明文传输与混合内容限制，不静默升级为 HTTPS。
- 目标站登录状态保留开关。
- 精确 origin 的 Key 注入协议。
- 中英文界面、权限检查、CSRF、关键变更审计、敏感值密封存储。
- 数据库升级、领域测试、Web 集成测试、浏览器契约测试及本地重新部署验收。

### 2.2 本次不包含

- ScriptBoard 反向代理、HTML 重写或服务端抓取目标页面。
- 把 ScriptBoard 原始会话、CSRF Token、用户密码或长期身份 Token 传给目标站。
- 在 URL query、fragment、路径或 Referer 中放置静态 Key。
- 任意请求头注入。普通跨源 iframe 无法由父页面指定 `Authorization` 等请求头；如目标必须依赖请求头，应由目标实现 Key 握手后的自身 API 请求。
- 绕过目标站的 `X-Frame-Options` 或 CSP `frame-ancestors`。
- 自动放宽所有页面的 CSP、全局允许任意 frame origin。
- iframe 内页面的 DOM 读取、样式覆盖、脚本注入或跨源自动登录。
- 导入导出 Key；配置导出如后续增加，只能导出非敏感元数据。

## 3. 调研结论

### 3.1 sub2api 的可复用结构

sub2api 使用 `custom_menu_items` 生成动态侧栏项，统一导航到 `/custom/{id}`，再由 `CustomPageView` 使用 iframe 打开配置 URL。它还在构建 URL 时追加 `user_id`、`token`、主题、语言和嵌入模式等 query 参数。

可复用的产品结构是：

- 配置记录拥有稳定 ID。
- 动态导航只引用应用自身稳定路由，不直接把外部 URL 放进侧栏。
- iframe 页面保留应用外壳，并提供新窗口打开入口。
- 导航只展示当前用户可见且已启用的记录。

参考：

- [sub2api CustomPageView](https://github.com/Wei-Shaw/sub2api/blob/main/frontend/src/views/user/CustomPageView.vue)
- [sub2api embedded URL builder](https://github.com/Wei-Shaw/sub2api/blob/main/frontend/src/utils/embedded-url.ts)
- [sub2api AppSidebar](https://github.com/Wei-Shaw/sub2api/blob/main/frontend/src/components/layout/AppSidebar.vue)
- [sub2api 自定义页面参数说明](https://github.com/Wei-Shaw/sub2api/blob/main/docs/ADMIN_PAYMENT_INTEGRATION_API.md)

不直接照搬的部分：

- 长期 token 不进入 URL。敏感 query 会进入浏览器历史、服务端及中间代理日志；OWASP 明确要求密码、token 等敏感数据不要放入 query string。
- iframe 必须声明最小化 `sandbox`，按凭据模式逐项开放能力。
- ScriptBoard 的 CSP 只在当前自定义页签响应上增加该记录的精确 origin，不做全局通配。
- 静态 Key 必须使用现有主机密钥密封，Web 页面永不回显已保存值。

参考：[OWASP ASVS：query string 不应包含敏感数据](https://owasp-aasvs4.readthedocs.io/en/latest/8.3.1.html)。

### 3.2 浏览器约束

- 不带 `allow-same-origin` 的 sandboxed iframe 会获得不透明 origin，目标页面的 Cookie、Local Storage 等能力会受限；保留目标登录状态时需要显式加入该 token。
- 现代浏览器可能分区或阻止跨站 iframe 的第三方 Cookie。即使配置“保留登录状态”，目标站仍可能需要 `SameSite=None; Secure`、CHIPS 或在用户交互后调用 Storage Access API，ScriptBoard 不能替目标站绕过浏览器策略。
- HTTPS ScriptBoard 页面嵌入普通 HTTP 页面属于可阻止的混合内容。系统仍接受 HTTP 配置，但必须显示原因和新窗口打开备用入口，不得静默改写协议。
- 目标响应可以通过 `X-Frame-Options` 或 CSP `frame-ancestors` 拒绝被嵌入；这只能由目标站调整。
- ScriptBoard 当前 CSP 的 `default-src 'self'` 会阻止外部 iframe，因此页签响应必须生成精确 `frame-src`。

参考：

- [MDN iframe sandbox](https://developer.mozilla.org/en-US/docs/Web/HTML/Reference/Elements/iframe)
- [MDN Storage Access API](https://developer.mozilla.org/en-US/docs/Web/API/Storage_Access_API)
- [MDN third-party cookies](https://developer.mozilla.org/en-US/docs/Web/Privacy/Guides/Third-party_cookies)
- [MDN CSP frame-src](https://developer.mozilla.org/en-US/docs/Web/HTTP/Reference/Headers/Content-Security-Policy/frame-src)
- [MDN X-Frame-Options](https://developer.mozilla.org/en-US/docs/Web/HTTP/Reference/Headers/X-Frame-Options)
- [MDN mixed content](https://developer.mozilla.org/en-US/docs/Web/Security/Defenses/Mixed_content)

## 4. ScriptBoard 现状与落点

当前仓库已有两种可以复用的机制：

- `internal/web/web_shell.go` 会根据数据库中的自定义面板动态追加导航项。
- `internal/web/ui/templates/settings-navigation.html` 和配置页面已形成稳定的服务端模板、PJAX 与权限模式。

本功能与“自定义面板”保持独立：自定义面板由 ScriptBoard 服务端抓取 JSON 并生成数据卡片；自定义页签由用户浏览器直接加载完整页面。两者的网络边界、凭据语义和风险不同，不共用 `internal/customdashboard` 表或 Manager。

建议新增以下 seam：

```text
internal/customtab/                 # 数据校验、排序、CRUD、Key 密封/解封
internal/web/web_custom_tabs.go     # 管理页与 iframe 页 handlers
internal/web/ui/templates/custom-tabs.html
internal/web/ui/templates/custom-tab-frame.html
```

`App` 只依赖 `customtab.Manager` 的领域接口；Shell、handler 和模板不能直接拼 SQL 或自行解封 Key。

## 5. 领域模型与数据

### 5.1 术语

在 `CONTEXT.md` 增加：

- **自定义页签（Custom Tab）**：实例级、可排序且可启停的受信页面引用；启用后在“外部”导航中拥有稳定入口。
- **目标站登录状态（Target Login State）**：目标 origin 自己在浏览器中的 Cookie 或 Web Storage 状态，不包含 ScriptBoard 会话。
- **页签 Key（Tab Key）**：由管理员配置、面向一个目标 origin、只在显式握手时交给 iframe 的静态凭据。

### 5.2 表结构

新增 `custom_tabs`：

```sql
CREATE TABLE IF NOT EXISTS custom_tabs (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    target_url TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 0 CHECK (enabled IN (0, 1)),
    credential_mode TEXT NOT NULL DEFAULT 'isolated'
        CHECK (credential_mode IN ('isolated', 'target_state', 'key')),
    visibility_roles TEXT NOT NULL
        DEFAULT 'administrator,maintainer,operator,viewer',
    key_name TEXT NOT NULL DEFAULT '',
    key_ciphertext BLOB NOT NULL DEFAULT X'',
    sort_order INTEGER NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
CREATE INDEX custom_tabs_order_idx
    ON custom_tabs(enabled, sort_order, created_at);
```

约束：

- ID 使用现有密码学随机 token 方案，不从名称生成。
- 名称去除首尾空白，限制为 1–80 个 Unicode 字符。
- `target_url` 限制为 2 KiB，必须是带主机的绝对 `http://` 或 `https://` URL；拒绝 userinfo、fragment、控制字符及非 HTTP(S) scheme。
- URL 可包含普通业务 query，但保存 Key 时不允许 Key 被模板替换进 URL。
- 私网、回环、`.local`、主机名和自定义端口都允许，因为请求由当前浏览器直接发出；Manager 和 Web handler 不对目标发起 DNS、探测或预览请求。
- Key 名称限制为 1–64 个安全字符；Key 限制为有效 UTF-8、1–4 KiB。
- 可见角色必须是四种固定角色中的非空集合；未知角色拒绝保存。
- `isolated` 和 `target_state` 模式必须把 `key_name`、`key_ciphertext` 清空。
- 更新时 Key 输入为空表示保留已有 Key；“移除 Key”必须是独立且明确的操作。
- 删除记录必须同时删除密文；审计只记录页签 ID、动作与结果。

### 5.3 密钥存储

`customtab.Manager` 接收现有 `secretstore.Store`，用与页签 ID 和目标 origin 绑定的 purpose/AAD 密封 Key。SQLite 只保存密文，不能保存明文、摘要或末四位。

Key 读取规则：

- 列表、编辑页和一般 GET 不解封。
- 只有通过授权的页签 Key 交付端点才解封。
- 解封失败即拒绝交付并记录失败审计，不降级为空 Key。
- 已保存 Key 在表单中只显示“已配置”，不放入 `<input value>`、`data-*` 或 JS 初始状态。
- State Backup 沿用现有数据库密文与外部主机密钥恢复边界；文档需要说明恢复到另一主机时的密钥依赖。

### 5.4 Schema 迁移

- 在 `internal/store/migrations` 注册新 schema。
- 将 `internal/buildinfo.DatabaseSchemaVersion` 从 54 提升到下一个版本。
- 新表纯新增，不对旧数据做猜测或回填。
- 增加从当前最小兼容版本和 schema 54 升级的测试，验证旧表数据保持不变、`PRAGMA user_version` 正确推进。

## 6. 权限与审计

权限规则：

| 行为 | 权限 |
|---|---|
| 查看启用的普通页签 | `PermissionObserve` |
| 查看 Key 模式页签 | `PermissionManageOperations` |
| 管理页签、排序、启停 | `PermissionManageOperations` |
| 新增、替换、移除 Key | `PermissionManageOperations` + 近期身份验证 |

Key 模式默认只向管理员和维护员显示，因为浏览器收到 Key 后，具备页面查看权限的用户最终可以通过开发者工具观察它；不能把客户端交付描述为对该用户保密。

审计动作建议：

- `create_custom_tab`
- `update_custom_tab`
- `enable_custom_tab` / `disable_custom_tab`
- `move_custom_tab`
- `rotate_custom_tab_key` / `remove_custom_tab_key`
- `delete_custom_tab`
- `deliver_custom_tab_key`，记录成功或失败，但不记录 Key、URL query 或消息正文

所有写入使用 CSRF 校验；Key 变更使用 `requireStepUp`。普通 GET 不产生高频审计，Key 每次成功交付可按“每会话、每页签首次”去重，避免刷新制造无界事件。

## 7. 管理页面

### 7.1 导航与路由

在“配置”大类增加：

```text
自定义页签 -> /config/custom-tabs
```

建议路由：

```text
GET  /config/custom-tabs
POST /config/custom-tabs
GET  /config/custom-tabs/{id}/edit
POST /config/custom-tabs/{id}
POST /config/custom-tabs/{id}/toggle
POST /config/custom-tabs/{id}/move
POST /config/custom-tabs/{id}/key/remove
POST /config/custom-tabs/{id}/delete

GET  /defined/tabs/{id}
POST /defined/tabs/{id}/key-challenge
POST /defined/tabs/{id}/key-delivery
```

两个 Key 接口只接受同源、已认证、带 CSRF 的 POST，响应 `Cache-Control: no-store`。challenge 响应只包含页签 ID、目标精确 origin、不可预测 nonce 和短过期时间，不包含 Key；浏览器先把 challenge 发给目标 iframe，只有在 iframe 从正确 origin 回显相同 nonce 后，才调用 delivery 接口消费 challenge 并转交 Key。

### 7.2 表单

表单包含：

- 名称。
- URL。
- 启用开关。
- 凭据模式：隔离打开 / 保留目标登录状态 / 注入 Key。
- 可见权限：系统管理员、维护员、执行员、观察员的非空多选；默认全部可见。
- Key 模式下的 Key 名称和 Key；编辑时空值保持原 Key。
- HTTP 明文风险提示。
- 登录态兼容性提示：第三方 Cookie 或 Web Storage 仍受浏览器策略与目标站 Cookie 属性限制。
- 嵌入兼容性提示：目标站必须允许被 ScriptBoard origin frame。

创建后默认停用。管理员先通过页面中的只读预览入口验证目标，再显式打开开关，避免错误配置立即出现在所有用户导航中。预览仍由浏览器 iframe 加载，不由服务端探测。

### 7.3 列表行为

- 列表按 `sort_order, created_at` 排序。
- 显示名称、规范化后的 origin、HTTP/HTTPS、启用状态、凭据模式和 Key 是否已配置。
- 不显示完整业务 query，避免页面和截图扩散可能存在的非 Key 参数。
- 支持上移、下移；边界动作不改变顺序。
- 删除需要明确确认；Key 模式删除前提示密文会同时删除。

## 8. “外部”动态导航

在 `shellNavigation` 的静态组之后、历史组之前插入动态“外部”组。仅当当前用户至少有一个可见且已启用的自定义页签时显示该组。

每项只包含：

```go
shellNavigationItem{
    Href:    "/defined/tabs/" + tab.ID,
    Label:   tab.Name,
    Icon:    "panel-top",
    Current: request.URL.Path == href,
}
```

要求：

- 侧栏中不出现目标 URL、Key 模式或其他敏感元数据。
- 停用或删除后，旧稳定路由返回 404，不渲染目标 URL。
- Key 模式对无 `PermissionManageOperations` 用户表现为不存在，避免通过状态码枚举。
- PJAX 切换继续只替换主内容；离开页签时移除 iframe 和 message listener。

## 9. iframe 页面与浏览器安全

### 9.1 页面响应

`custom-tab-frame.html` 只渲染当前记录，页面响应：

- `Cache-Control: no-store`
- 保留全局 `X-Frame-Options: DENY` 和 `frame-ancestors 'none'`，它们保护 ScriptBoard 自身不被别人嵌入。
- 覆盖当前响应 CSP，在既有规则上增加精确的 `frame-src <scheme>://<host>:<port>`；不使用 `*`、`http:` 或 `https:` 通配。
- iframe 使用 `referrerpolicy="no-referrer"`。
- 提供 `target="_blank" rel="noopener noreferrer"` 的新窗口入口。
- 不把 Key、CSRF Token 或 challenge secret 写入 iframe URL。

不能把包含 path/query 的完整 URL直接作为 CSP source；只使用经过 `net/url` 解析和规范化的 origin，模板仍使用经过 HTML 属性转义的完整目标 URL。

### 9.2 sandbox 策略

最小能力如下：

| 模式 | sandbox tokens |
|---|---|
| `isolated` | `allow-scripts allow-forms` |
| `target_state` | 上述 + `allow-same-origin allow-storage-access-by-user-activation` |
| `key` | 与 `target_state` 相同，另启用 Key 消息握手 |

默认不开放：

- `allow-top-navigation`
- `allow-top-navigation-by-user-activation`
- `allow-popups`
- `allow-popups-to-escape-sandbox`
- `allow-downloads`
- 摄像头、麦克风、地理位置、剪贴板等 Permissions Policy 能力

如真实目标需要下载或弹窗，应在后续需求中做独立、可见、逐能力开关，不能一次性给所有页签放开。

### 9.3 Key 消息协议

目标页需要主动实现协议，建议版本化。完整顺序是：父页面通过同源 POST 取得不含 Key 的 challenge，向 iframe 发送初始化消息；iframe 回显 challenge 表示已经就绪；父页面验证后再通过同源 POST 消费 challenge、取得 Key，并向 iframe 发送凭据消息。

```text
parent -> iframe
{
  type: "scriptboard.custom-tab.init",
  version: 1,
  tabId: "...",
  nonce: "..."
}

iframe -> parent
{
  type: "scriptboard.custom-tab.ready",
  version: 1,
  tabId: "...",
  nonce: "..."
}

parent -> iframe
{
  type: "scriptboard.custom-tab.credential",
  version: 1,
  tabId: "...",
  nonce: "...",
  keyName: "...",
  key: "..."
}
```

父页面必须同时验证：

- `event.source === iframe.contentWindow`
- `event.origin === configuredOrigin`
- `tabId`、`version`、nonce 完全一致
- challenge 未过期且本会话未消费
- 当前登录用户仍有页签查看权限

发送时 `window.postMessage(message, configuredOrigin)`，禁止 `"*"`。父页面不把 Key 存入 Local Storage、Session Storage、DOM、全局变量或日志；交付完成后清空持有值并撤销 challenge。刷新需要新 challenge。

### 9.4 登录态现实边界

“保留目标站登录状态”打开后：

- 目标站仍负责自己的登录页面、Cookie 属性、token 刷新和退出。
- ScriptBoard 只通过 sandbox 能力允许目标使用其 origin；不会读取或设置跨源 Cookie。
- 浏览器阻止第三方状态时，iframe 应显示目标自身登录页或错误；ScriptBoard 页面给出“在新窗口登录后重试”和兼容性说明。
- 如果目标实现 Storage Access API，sandbox 已提供所需 token，但授权提示和结果由浏览器决定。

## 10. HTTP、HTTPS 与本地地址

URL 校验同时接受 HTTP 和 HTTPS，不默认拒绝回环、私网或本地域名，也不静默升级协议。

行为矩阵：

| ScriptBoard | 目标 | 预期 |
|---|---|---|
| HTTP | HTTP | 允许 iframe；Key 和目标登录数据均为明文传输风险 |
| HTTP | HTTPS | 允许 iframe |
| HTTPS | HTTPS | 允许 iframe |
| HTTPS | HTTP | 浏览器通常阻止为混合内容；保留配置并显示明确说明与新窗口入口 |

“本地”以当前浏览器的网络视角解释。`http://127.0.0.1:3000` 指访问浏览器所在机器，不一定是 ScriptBoard 服务所在主机。管理页需要明确写出这一点。

## 11. 实施顺序

1. 更新 `CONTEXT.md`、`docs/DATA-MODEL.md`，新增自定义页签术语和信任边界；增加一份 ADR，记录浏览器直连、动态 CSP、sandbox 与 Key 消息协议的决策。
2. 为 `internal/customtab` 先写 URL、字段、模式、权限可见性、排序和 Key 生命周期测试。
3. 实现 `customtab.Manager`，接入 `secretstore.Store`，确保所有秘密错误经过 redaction。
4. 新增 `custom_tabs` schema、提升 schema version，补充升级与新库初始化测试。
5. 注册管理路由与 handlers，完成 CRUD、启停、排序、step-up Key 变更和审计。
6. 增加管理模板、中英文文案和现有视觉系统样式；图标只使用 Lucide。
7. 扩展 `web_shell.go`，按当前会话权限加载启用项并生成“外部”动态组。
8. 实现 iframe 页面、逐响应 CSP、sandbox 模式和新窗口备用入口。
9. 实现 nonce challenge 与 exact-origin `postMessage` Key 交付，增加前端 listener 清理和 PJAX 回归保护。
10. 完成 Go、Web、浏览器契约测试和安全回归测试。
11. 按仓库本地部署流程重新部署，保留测试数据和最终部署，输出测试报告。
12. 验收通过后更新面向用户的 `README`、`README_EN.md` 与 `docs/ACCEPTANCE.md`。

## 12. 测试计划

### 12.1 领域与数据库

- 接受合法 HTTP/HTTPS、回环、私网、主机名、自定义端口、path/query。
- 拒绝相对 URL、userinfo、fragment、非 HTTP(S)、空 host、控制字符和超限输入。
- 创建默认停用；启停、编辑、排序、删除符合事务语义。
- 密文不含明文 Key；错误 purpose、错误页签 ID 或目标 origin 无法解封。
- 空 Key 更新保留旧值，明确移除才删除密文。
- 旧 schema 升级后原数据不变，新表存在且 schema version 正确。

### 12.2 Web 与权限

- 未登录请求跳转登录；viewer/operator 不能访问管理路由。
- 管理员和维护员可以 CRUD；缺少 CSRF 的写请求被拒绝。
- Key 新增、替换、移除缺少近期身份验证时进入 step-up。
- 普通启用项按配置的固定角色集合可见；Key 项在此基础上只对 `PermissionManageOperations` 用户存在。
- 停用/删除项从“外部”消失，稳定路由返回 404。
- 页面、表单、错误、审计 CSV 和响应 JSON 均不出现 Key 明文。
- iframe 页 CSP 只允许当前目标 origin；其他 ScriptBoard 页面维持原 CSP。
- HTTP 不被改写，页面显示明文风险；HTTPS 页面嵌 HTTP 显示混合内容说明。

### 12.3 浏览器契约

准备三个保留的本地测试站点：

- HTTP 站点：显示自身 Cookie/Storage 状态并支持登录。
- HTTPS 站点：使用测试 CA，支持同样登录状态验证。
- Key 站点：实现 ready/credential 协议，并记录是否收到正确 `keyName` 与 Key。

逐项验证：

1. 基础访问：登录、主导航、配置页、外部页、退出均可访问。
2. 新增停用页签后不显示；打开开关后显示；关闭后消失。
3. 点击动态项后 ScriptBoard 外壳保留，iframe 占满工作区。
4. `isolated` 模式不能复用目标 Cookie/Storage。
5. `target_state` 模式在浏览器允许第三方状态时可登录、刷新后保持；阻止第三方状态时给出可理解的兼容性路径。
6. Key 只在正确 iframe、正确 origin、正确 nonce 下交付一次。
7. 伪造 origin、错误 source、重放 nonce、过期 challenge、无权限用户全部失败。
8. Key 不出现在地址栏、浏览器历史、Referer、HTML 源码或 ScriptBoard 服务日志。
9. 目标设置拒绝 frame 的响应头时，iframe 不被绕过，新窗口入口仍可用。
10. HTTP/HTTPS 四种组合按行为矩阵验证。
11. 桌面与移动布局、侧栏收起、PJAX 前进后退、刷新和语言切换正常。
12. 删除 Key 页签后旧 challenge 和旧路由均不可再取凭据。

### 12.4 本地部署验收

实施完成后严格执行：

1. 先列出上述功能测试和基础访问测试清单。
2. 停止旧实例并重新本地部署，记录新实例地址及生成的登录用户名、密码。
3. 使用外部浏览器或 HTTP 接口逐项测试，不使用内部浏览器；允许创建并保留测试页签、测试用户、测试 Cookie 与测试 Key。
4. 不停止最终验证实例，保留部署与测试数据。
5. 在 `docs/test-reports/` 生成带日期的本地部署测试报告，记录通过、失败、受浏览器策略限制的项目及证据。

## 13. 验收标准

- “配置”中存在“自定义页签”入口，管理员和维护员可以管理记录。
- 启用记录按顺序出现在“外部”大类，停用或删除后立即消失。
- 点击记录只访问 ScriptBoard 稳定路由，并在 iframe 打开配置的 HTTP/HTTPS 页面。
- HTTP 与 HTTPS 均可显式保存；明文与混合内容限制有清晰说明且不会被静默改写。
- 隔离模式不交付身份或 Key。
- 保留状态模式只允许使用目标站自己的浏览器状态，不暴露 ScriptBoard 会话。
- Key 模式不把 Key 放入 URL、DOM、日志、审计或导出，只向正确 origin 的正确 iframe 交付。
- Key 静态存储为主机密钥绑定的密文；Key 页面只显示“已配置”。
- 无权限用户无法通过导航、稳定路由或 Key 接口枚举 Key 模式页签。
- 每个 iframe 响应只在 CSP 中放行自己的精确 origin，并使用与模式匹配的最小 sandbox。
- 目标站拒绝 frame、浏览器阻止第三方状态或 HTTPS→HTTP 时不尝试绕过，并保留安全的新窗口访问路径。
- 数据库升级、Go 测试、Web 集成测试、浏览器契约测试和本地重新部署验收全部完成。

## 14. 开发前需要再次确认的产品选择

本计划采用以下默认解释，进入实现前只需确认是否维持：

- 管理入口位于左侧“配置”大类，而不是底部“设置”的横向页签。
- 动态导航新建独立“外部”大类，不并入现有“监控”或“配置”。
- “保留登录状态”指目标站自己的浏览器状态，不转发 ScriptBoard 登录会话。
- Key 目标页面需要实现 `postMessage` 协议；MVP 不提供不安全的 query Key 兼容模式。
- Key 模式页签只对配置允许的管理员和维护员可见；不含 Key 的页签按配置的固定角色集合可见。

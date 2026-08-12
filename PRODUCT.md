# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Users

ScriptBoard 面向管理单台 Windows 或 Linux 主机文件与脚本的运维和开发人员。用户通常在本机或受控网络内工作，需要快速判断宿主与任务状态、执行脚本、检查输出，并处理失败、超时、权限或存储不足。

## Product Purpose

ScriptBoard 将分散在文件系统中的脚本变成一套可浏览、可执行、可计划、可审计的本地主机工作台。成功意味着用户无需搭建远程编排平台，也能可靠完成日常脚本操作，并始终知道当前发生了什么。

## Positioning

产品直接浏览主机文件系统并执行允许位置中的现有脚本，不要求注册、复制或改造成流水线；执行、日志、计划、文件管理、可恢复删除、审计与应用更新围绕同一台主机形成闭环。

## Operating Context

- 单台主机、单管理员上下文，通过受保护的服务端渲染网页操作。
- 主机文件与目录、脚本参数、运行日志、快捷执行、计划任务、变量、审计事件、文件操作进度和本机应用资源事实是核心工作材料。
- 桌面端是高密度操作的主界面；移动端保留全部关键流程，并采用抽屉导航和可重排布局。
- 产品正式支持 Windows 10/11、Windows Server 2019+ 与 systemd Linux。
- 正式 Release 安装为版本化系统服务；旧式单文件服务安装不迁移、不兼容。

## Capabilities and Constraints

- Web 信息架构按操作者意图分为监控、资源、配置、历史和设置；旧页面路由不保留兼容入口。
- 使用 Go `html/template`、原生 JavaScript、SSE 与嵌入式静态资源，不引入生产 Node.js 构建链或 SPA 运行时。
- Web 完整提供简体中文和美式英语；首次访问按 `Accept-Language` 协商，之后由 Cookie 保存选择。CLI 与托盘仍为简体中文。
- 所有关键页面和任务均保留无 JavaScript 的服务端页面；JavaScript 只增强为局部导航、右侧任务面板、实时状态和快捷键。
- Run 不进入队列；并发、停止、超时与日志保留遵循现有领域状态机。
- 主机文件操作、受保护路径、跨文件系统事务与审计必须清晰呈现风险、边界和不可逆影响。
- 应用观测只读采集宿主应用与本机 Docker 容器应用；不能借此加入进程控制、容器编排、通用阈值告警或远程监控。
- 正式构建可自动检查固定官方仓库的稳定版，但更新必须先验证 Ed25519 签名和归档摘要，再由管理员明确确认安装；有活动 Run 时不得切换版本。
- 便携运行只提供版本检查与手工下载提示，源码开发构建不联网检查；第一版不提供无人值守安装。

## Information Architecture

- 监控：`/monitor`、`/monitor/applications`
- 资源：`/resources/files`、`/resources/variables`、`/resources/trash`
- 配置：`/config/quick-runs`、`/config/schedules`
- 历史：`/history/runs`、`/history/runs/{id}`、`/history/audit`
- 设置：`/settings/account`、`/settings/users`、`/settings/name`、`/settings/display`、`/settings/updates`
- 任务链接使用语义化 GET 地址，可直接打开完整页面；增强模式在当前工作区右侧打开同一内容且不替换地址栏中的工作区 URL。

## Brand Commitments

- 产品名为 ScriptBoard；实例可自定义左上角导航显示名称，但不改变产品身份。
- 界面统一使用 Lucide 图标，禁止使用表情符号。
- 视觉语言是轻量、极简的“校准台账”：冷白画布、石墨色规则线、单一低饱和冷蓝强调、系统字体和静止时的平面层级。
- 不使用渐变、装饰性卡片、Canvas 背景或深色主题；动效只解释导航、状态与任务上下文。
- 第一屏必须先给出宿主判断和客观依据；CPU 与内存作为事实展示，不凭通用阈值制造告警。

## Evidence on Hand

- 产品边界与术语：`CONTEXT.md`
- 产品需求与验收：`docs/PRD.md`、`docs/ACCEPTANCE.md`
- 领域决策：`docs/adr/`
- 页面与真实业务文案：`internal/app/web/templates/`
- 当前没有客户证言、商业指标、品牌摄影或外部奖项背书；设计不得虚构这些内容。

## Product Principles

- 状态先于装饰：用户必须在第一眼看懂宿主、任务与风险。
- 操作必须可追溯：重要变化都有明确结果、历史或审计线索。
- 直接操作真实文件：不制造与文件系统割裂的脚本注册层。
- 复杂能力保持可控：高风险动作需要清楚的上下文、边界与反馈。
- 渐进增强不改变能力边界：可分享链接、键盘操作和无 JavaScript 路径指向同一服务端事实。
- 视觉表达服务于操作者的专注，不制造额外认知负担。

## Accessibility & Inclusion

核心操作键盘可达、焦点清晰、使用语义化标签，并尊重 `prefers-reduced-motion`。颜色不作为状态的唯一表达；正文与交互控件达到 WCAG 2.1 AA 对比度。Web 状态、动作、错误和日期均随当前语言环境本地化，原始标识只保留在技术详情中。

## Quality Gate

- 自动化浏览器门禁固定为桌面 Chromium，覆盖登录、主导航、任务面板、运行日志和关键截图。
- Chrome、Edge、Firefox 与 Safari 保持最佳努力兼容，但不作为每次提交的自动化门禁。
- 移动端必须可完成核心流程，通过响应式实现和人工验收保证，不纳入当前截图回归门禁。

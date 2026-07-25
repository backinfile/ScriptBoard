# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Users

ScriptBoard 面向管理单台 Windows 或 Linux 主机脚本的运维与开发人员。用户通常在本机或受控网络内工作，需要快速判断主机与任务状态、执行脚本、检查输出，并处理失败、超时、存储不足或版本保护异常。

## Product Purpose

ScriptBoard 将分散在文件系统中的脚本变成一套可浏览、可执行、可计划、可审计的本地主机工作台。成功意味着用户无需搭建远程编排平台，也能可靠地完成日常脚本操作，并始终知道当前发生了什么。

## Positioning

产品直接执行受管目录中的现有脚本，不要求注册、复制或改造成流水线；执行、日志、计划、文件管理、版本保护与审计围绕同一台主机形成闭环。

## Operating Context

- 单台主机、单管理员上下文，通过受保护的服务端渲染网页操作。
- 文件与目录、脚本参数、运行日志、快捷执行、计划任务、变量、审计事件和本地 Git 版本历史是核心工作材料。
- 用户会在桌面端进行高密度操作，也需要在现代移动浏览器完成关键操作。
- 产品正式支持 Windows 10/11、Windows Server 2019+ 与 systemd Linux。

## Capabilities and Constraints

- 保留现有页面、路由、服务端模板绑定、CSRF 防护和渐进增强行为；允许优化信息架构、文案与交互路径。
- 使用 Go `html/template`、原生 JavaScript、SSE 与嵌入式静态资源，不引入 Node.js 构建链或 SPA 运行时。
- MVP 仅提供简体中文界面。
- Run 不进入队列；并发、停止、超时与日志保留遵循现有领域状态机。
- 受管文件操作、版本保护与审计必须继续清晰呈现风险和不可逆影响。

## Brand Commitments

- 产品名为 ScriptBoard。
- 界面统一使用 Lucide 图标，禁止使用表情符号。
- 前端需要达到强烈、完整、先锋的数字体验，同时保证运维任务的清晰度与效率。

## Evidence on Hand

- 产品边界与术语：`CONTEXT.md`
- 产品需求与验收：`docs/PRD.md`、`docs/ACCEPTANCE.md`
- 领域决策：`docs/adr/`
- 现有页面与真实业务文案：`internal/app/web/templates/`
- 当前没有客户证言、商业指标、品牌摄影或外部奖项背书；设计不得虚构这些内容。

## Product Principles

- 状态先于装饰：用户必须在第一眼看懂主机、任务与风险。
- 操作必须可追溯：重要变化都有明确结果、历史或审计线索。
- 直接操作真实文件：不制造与文件系统割裂的脚本注册层。
- 复杂能力保持可控：高风险动作需要清楚的上下文、边界与反馈。
- 视觉表达服务于操作者的专注，而不是制造额外认知负担。

## Accessibility & Inclusion

核心操作需要键盘可达、焦点清晰、具备语义化标签，并尊重 `prefers-reduced-motion`。颜色不能成为状态的唯一表达方式；移动端保留完整的关键操作能力。

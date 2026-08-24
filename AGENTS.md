# Repository guidance

## Agent skills

### Issue tracker

Issues and PRDs are tracked as local Markdown under `.scratch/`; external PRs are not a triage surface. See `docs/agents/issue-tracker.md`.

### Triage labels

Use the canonical labels `needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, and `wontfix`. See `docs/agents/triage-labels.md`.

### Domain docs

This is a single-context repo using root `CONTEXT.md` and `docs/adr/`. See `docs/agents/domain.md`.

## 分支与发布

+ `dev` 是唯一主干和默认分支；`main` 已废弃，禁止继续提交、合并或发布。
+ 小改动可直接提交到 `dev`；大功能从 `dev` 创建功能分支，CI 通过后合并并删除。
+ 发布前在 `dev` 完成测试，并按需更新面向用户的 `README`。
+ 从 `dev` 创建 `release/X.Y.Z`，在同一提交打 `vX.Y.Z` Tag，由项目 workflow 发布。
+ 执行发布任务时，必须主动检查并优先遵循仓库已有 GitHub Actions workflow。
+ release 分支和正式 Tag 不可修改；发现问题须回到 `dev` 修复、测试，再发布新版本。修复问题时要添加简要注释记录下
+ worktree创建目录：../worktrees/projectName/xxxx

## 前端设计原则

+ 图标：统一使用 Lucide 图标，界面全程禁止使用表情符号。
+ 设计标准：对标 Awwwards 顶级网站水准，达到 Awwwards、FWA、CSS Design Awards 每日最佳网站同等设计品质。
+ 创意自由度：将浏览器视作交互式艺术画布，跳出传统布局框架，追求先锋视觉风格、实验性排版、流畅物理动效、极具冲击力的文字版式。
+ 沉浸式体验：融合代码、高级渲染逻辑，打造统一完整的精品页面，做出突破常规 UI 认知、令人惊艳的数字交互体验。

## 连接兼容性

+ 所有用户可配置连接默认同时支持 SSL/TLS 安全模式与非 SSL/TLS 明文模式；不得把 SSL/TLS 写死为唯一可用模式，也不得默认拒绝协议明确提供的明文连接。
+ 当协议提供跳过证书验证的显式选项时必须支持该选项并保留用户选择，同时在界面和文档中明确提示中间人攻击风险；不得静默启用、静默升级或替用户改写连接安全策略。
+ 新增或修改连接功能时，测试必须覆盖安全连接、明文连接，以及协议支持时的显式跳过证书验证模式。

# Avoid instruction-to-output leakage

Distinguish instructions from deliverable content. Embody the requirements; never restate them unless explicitly requested.


# 本地部署测试流程
1. 列出所有需要进行测试的条目，额外添加一些基础访问测试
2. 重新本地部署，获取登录用户名密码
3. 使用外部浏览器或者直接使用http接口访问进行逐项测试,本地部署允许创建测试数据来进行。不要使用内部浏览器。测试数据在测试后保留。
4. 保留当前的部署，生成测试报告。

# 基本要求
+ 当进行修改时，要在保证正确的前提下选择最小改动。
+ 当玩家对ai生成内容纠错时，文本或注释中多写正向引导，直接移除错误内容的所有信息（不写入注释）。
+ 进行bug修复时，要求在附近简要注明修复的问题和修复的方式。
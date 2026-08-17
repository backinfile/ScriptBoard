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

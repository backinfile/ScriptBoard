# 退出登录、安全扫描与 MCP 接入

## 原始用户输入

```text
# AGENTS.md instructions for [仓库根目录]

<INSTRUCTIONS>
# Global workspace conventions

- Create Git worktrees under `../worktrees/<projectName>/<task-slug>` relative to the repository root. Do not place worktrees directly alongside the repository in its parent directory.

<!-- set-global-rules:start -->
## 用户协作约定

### 表达偏好

请在所有回答及你撰写的内容中遵循以下表达偏好：

1. 直接说明结论、事实、理由和必要步骤，语气自然、明确。详细程度按任务需要展开，保留有价值的解释、推导、依据、例子和细节。精简的对象是赘述。

2. 对准备添加的反面解释、范围声明、保留说明和风险提醒，先判断：删去这句话，是否会实质改变我对答案、结果状态或下一步行动的理解？如果不会，且它主要用于预防假想误解、提前免责或凑齐注意事项，就直接删除。不要换一种委婉说法保留同样的冗余信息。

3. 优先正面陈述“是什么、能做什么、已经做了什么、为何如此”。避免无端追加“这只是……不代表……”“这并不意味着……”，避免主动罗列没做什么、未涉及什么和不保证什么。“不是……而是……”仅在对比或纠错确有帮助时使用，避免成为惯用句式。

4. 让结论的强度与证据一致。将必要的对象、条件、数据范围和实际状态直接写进结论。证据充分时明确判断；确有不确定性时，指出具体不确定之处及其影响。避免反复堆叠“可能、一定程度上、相对而言”等弱化词。

5. 直接回答问题、解释机制、纠正错误所需的否定句正常使用。对于我明确要求说明的事项，以及省略后会造成实质误解的重要限制、失败或未完成事项，应准确交代。根据重要性放在相关结论旁或简短的状态说明中，优先用一句话说清具体事实及影响，下一步按需补充。避免孤立地写“未验证”“不保证”“仅供参考”，也不要机械增加“风险提示”“局限性”等段落。

6. 撰写文档、介绍、邮件等成稿时，按目标读者的需求组织正文。必要的制作过程说明放在交付说明中；成稿聚焦读者需要的内容，避免混入对自身工作范围的解释、假想争议和例行免责声明。

7. 输出前自行检查并清理上述赘述，直接交付整理后的答案。无需复述这些偏好，也无需宣布你已遵守。

### 修改、设计与测试

- 进行修改任务时使用最小改动。
- 进行设计类任务时，文本内容面向使用者生成。明确区分“实现要求”和“面向用户的文本”，实现要求只用于指导实现，不写入面向用户的文本；用户侧内容保持简洁、易理解。少用标语和 slogan。界面文案只保留名称、数量、状态和必要的按钮文字。操作提示、辅助说明、副标题、帮助文案及设计解释，仅在我明确要求时添加。功能通过布局、图标和交互体现。交付前删除所有自行添加的多余说明。
- 进行任务时，只自动执行验证当前改动所必需的最小测试；完整、全面的测试等全部工作完成后，再向用户说明测试范围并请求同意，取得同意后执行。
<!-- set-global-rules:end -->

--- project-doc ---

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
+ 本地部署在默认情况下都是测试用的，可以添加足够的测试数据来进行测试（不用删除），可以直接打印本地部署的相关密码等到输出中。
+ 要求同步修改readme文档，要求：尽量精简描述；文字需要面向使用者，不要过多的解释性文字。
+ 发布中进行检查发布状态时，可以一分钟检查一次。

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
</INSTRUCTIONS><environment_context>
  <cwd>[仓库根目录]</cwd>
  <shell>powershell</shell>
  <current_date>2026-11-19</current_date>
  <timezone>Asia/Shanghai</timezone>
  <filesystem><workspace_roots><root>[仓库根目录]</root><root>[Codex 可视化目录]</root></workspace_roots><permission_profile type="managed"><file_system type="restricted"><entry access="read"><special>:root</special></entry><entry access="write"><path>[仓库根目录]</path></entry><entry access="write"><path>[Codex 可视化目录]</path></entry><entry access="write"><special>:slash_tmp</special></entry><entry access="write"><special>:tmpdir</special></entry><entry access="read"><path>[仓库根目录]/.git</path></entry><entry access="read"><path>[Codex 可视化目录]/.git</path></entry><entry access="read"><path>[仓库根目录]/.agents</path></entry><entry access="read"><path>[Codex 可视化目录]/.agents</path></entry><entry access="read"><path>[仓库根目录]/.codex</path></entry><entry access="read"><path>[Codex 可视化目录]/.codex</path></entry><entry access="read"><path>[仓库根目录]/.aws</path></entry><entry access="read"><path>[Codex 可视化目录]/.aws</path></entry></file_system></permission_profile></filesystem>
</environment_context>
```

```text
1 怎么没有推出登录 2 看看github上提示的这个项目的安全检查警告，修复下 3 创建mcp怎么这么繁琐，你来简化下流程 [$worktree-task]($worktree-task)
```

```text
<skill>
<name>worktree-task</name>
<path>$worktree-task</path>
---
name: worktree-task
description: Use when completing a repository change in an isolated Git worktree, consolidating its task changes into one commit, and merging it back to the starting branch.
---

# Worktree Task

Complete the requested change in a temporary worktree, then integrate it safely into the branch that was current when the task began.

## Rules

- Capture the original checkout, branch, and revision once; never re-infer the integration target.
- Refuse an active Git operation. If the original checkout is dirty, warn that its uncommitted changes will not enter the worktree, preserve them untouched, and proceed from the current branch's latest commit by default.
- Place the worktree at `../worktrees/<projectName>/<task-slug>` relative to the repository root, unless repository instructions override it.
- Perform all modification and verification work inside the worktree.
- Keep a task document under `docs/worktree-tasks/<task-slug>/` inside the worktree and include it in the task commit, so it survives integration and worktree cleanup.
- Sanitize every document tracked by Git or being added to Git, including existing unchanged documents. Replace sensitive data such as private personal details and credentials with descriptive placeholders while preserving meaning.
- Use repository-relative paths in persisted documents and express worktree locations relative to the repository root. Never persist local absolute paths, including in quoted user input or Markdown links. Use descriptive placeholders for paths outside the repository and `$skill-name` for local skill references. Tool calls and safety checks may use absolute paths internally.
- Do not perform remote operations or rewrite existing original-branch history unless explicitly requested.
- For merge and cleanup confirmation, use `request_user_input_async` when available and permitted; otherwise ask in chat. Ask one short, direct question with the action choices. One explicit confirmation may approve both merging and the listed cleanup; the original task request alone is not approval.
- Preserve the task branch and worktree whenever a safety check or integration fails.

## Task Document

Use `docs/worktree-tasks/<task-slug>/README.md` by default, with the same task slug as the worktree. Create it before implementation. Keep the input record complete and the plan and results concise:

- **Original user input:** Record the full text of every task-related user message in chronological order, including introductory guidance, skill invocations, constraints, and follow-up corrections. Preserve the original wording and formatting, applying only the sanitization above. Append later messages in full.
- **Brief task plan:** Record the intended approach and main steps; update it when the approach changes.
- **Result and changes:** After implementation, summarize the actual completion status, changed files and behavior, verification results, and any remaining work or limitations. If work stops early, record partial progress accurately.

Before each commit, check the input record against the user messages, finalize the result section, and review all Git-tracked documents and documents being added for sensitive data and local absolute paths, including quoted input and link targets. If synchronization changes the implementation or verification results, update the document in the synchronization commit. Preserve the document with the task changes and include its path in the final report.

## Workflow

1. Check the repository state and refuse conflicting worktree paths or branches. Record any original uncommitted state so it can be protected and verified later.
2. Create a uniquely named task branch and worktree from the captured original branch's latest commit. Create the task document with the sanitized user input and brief plan.
3. Establish a clean baseline, implement the task, and verify it. Complete the task document's result section, then consolidate all task changes, including the document, into one commit before synchronization unless the user or repository policy requires multiple commits.
4. Merge the latest local original branch into the task branch. Resolve conflicts only in the worktree, then verify and commit the synchronized result.
5. Confirm the original checkout is still on the captured branch, its committed HEAD is unchanged since synchronization, and any pre-existing uncommitted state remains intact. If HEAD advanced, repeat synchronization; stop after three races.
6. Show the target branch, task commit, change summary, verification results, any original uncommitted state, and proposed cleanup items once; reuse this summary if already shown. Ask directly whether to merge and clean up, merge and keep, or defer.
7. Only after confirmation, merge the synchronized task branch back without creating an additional integration commit when possible. If integration would overwrite uncommitted state, preserve the worktree and report that integration is pending instead.
8. Verify the committed trees match, checks pass, the original branch contains the consolidated task commit, and pre-existing uncommitted state was preserved.
9. After successful integration verification, remove this run's clean worktree, task branch, and disposable temporary files covered by the cleanup approval, after proving needed content is preserved. Reuse combined approval from step 6 without asking again. If only merging was approved, retain these items until the user approves cleanup; request further confirmation for any additional cleanup items.

Report the resulting branch, commit, verification, cleanup state, and task document path.
</skill>
```


```text
<send_user_message_question_reply>
[{"questionItemId":"[\"request_user_input_async\",\"call_61ff3cdfc8ae411ba40de3d7d91bafaf\",0]","question":"如何处理提交 adb9a48？清理范围为本次 worktree 和任务分支，本地测试部署会保留。","answer":"合并到 dev 并清理"}]
</send_user_message_question_reply>
```

## 计划

- 从 `dev` 的 `c0665ff` 创建 `../worktrees/ScriptBoard/logout-security-mcp`，保留原工作区的未提交变更。
- 确认安全工作流失败原因和修复版本，在侧栏提供退出入口，简化 MCP OAuth 接入并同步中英文 README。
- 运行针对性测试，使用本地部署验证功能，提交后征求合并和清理确认。

## 结果

- 共享侧栏底部提供退出登录入口，沿用 CSRF 校验和服务端会话注销。
- MCP 设置先展示与 OAuth 发现一致的服务地址，可一键复制。支持动态注册的客户端可直接连接后完成浏览器授权。手动注册折叠在客户端列表下，客户端与授权管理继续可用。
- GitHub [定时安全工作流](https://github.com/backinfile/ScriptBoard/actions/runs/43856702597) 的 Go 漏洞扫描失败。Go 1.26.9 在本地复现 GO-2026-6809 和 GO-2026-6859。构建基线提升至 [Go 1.26.11](https://go.dev/doc/devel/release#go1.26.11) 后，Windows 与 Linux 目标的漏洞扫描均通过，可达漏洞与已导入包告警均为零。模块扫描仍列出 GO-2026-5932，涉及项目未导入的 OpenPGP 包。远端检查随修复提交进入 GitHub 后重新验证。
- 更新中英文 README。历史测试报告中的本机路径、测试密码和远程测试地址改用相对路径或占位值。

### 本地验证

- 两项 Web 回归断言在修复前失败，修复后与其他五项账号和 MCP Web 检查一起通过。另通过 OAuth 授权码、令牌轮换与重定向地址匹配测试。
- 外部 Chromium 验证 9 项通过，涵盖基本页面和样式访问、管理员登录、桌面与折叠侧栏退出按钮、MCP 抽屉复制、键盘展开、OAuth 发现、动态注册到 MCP 初始化、手动注册以及移动侧栏注销会话。
- 保留回环测试部署和注册数据。部署地址为 http://127.0.0.1:8101 ，登录账号为 `admin`，密码保存在测试部署私有文件中。
- 界面检查、Git 差异格式检查和本次暂存内容的 Gitleaks 扫描通过。

# Embed a Tool-constrained AI Agent

ScriptBoard embeds an AI Agent whose model transport is the pinned `pi-llm-go`
dependency behind types owned by `internal/ai`. The application owns the Agent
Loop, persistence, Skills, permissions, approval workflow, and recovery rules.
No model receives a shell, process, arbitrary HTTP, MCP, or public extension
surface.

Every registered Tool has one fixed risk class: Query, Execute, or Modify.
Execute and Modify imply Query. Effective authority is the intersection of the
model-profile ceiling and the conversation grant. Query calls execute
immediately within bounded read budgets. Execute and Modify calls only prepare
actions; `submit_action_batch` freezes at most one ordered batch per user
message. A batch is durably stored before manual or automatic approval and is
revalidated immediately before execution.

Script execution is never implemented in the AI module. It enters the existing
Run Manager and therefore retains Managed Root checks, Run Leases, overlap
rules, variables, logging, Version Protection, and host-identity behavior.
Likewise, file, schedule, variable, Quick Run, and Version Protection actions
delegate to existing ScriptBoard domain modules.

Model profiles support OpenAI Responses, OpenAI Chat Completions and compatible
servers, and Anthropic Messages and compatible servers. Remote endpoints require
HTTPS; loopback HTTP is allowed. Model and custom-header secrets are separate
mode-restricted files under State Root, while SQLite stores only random
references.

Built-in Skills are read-only, embedded project knowledge. The model sees their
catalog and may load any number through `read_skill`; Skills never register
Tools or raise authority. Actual Skill versions, content, and hashes are
recorded per turn.

Conversations persist normalized messages, Tool calls and results, attempts,
events, frozen batches, attachments, model snapshots, and versioned history
summaries. Browser disconnects do not cancel work. Restart marks active model
turns and running batches interrupted and does not replay them. The persistent
Kill Switch rejects new work and cancels model requests and remaining batch
actions, but never stops a Run that already started.

ScriptBoard still does not provide a script sandbox. An automatically approved
full-control model can write and start a Trusted Script with the service's
Runtime Identity. Existing mandatory low-disk write gates remain in force; this
decision adds no additional AI-specific disk quota or warning gate.

---
name: script-authoring
description: Create or repair trusted scripts inside the Managed Root.
version: 1
---

# Script authoring

Inspect the target directory and related files before proposing changes. Prefer a focused,
portable script and explain its inputs, outputs, interpreter requirements, and failure
behavior. ScriptBoard executes trusted scripts through the Run Manager; never ask for or
attempt direct shell access. Use a frozen action batch for file changes and execution.

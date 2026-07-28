# Chromium browser gate

This test-only suite launches a deterministic local ScriptBoard fixture, exercises the real server-rendered application in Chromium, and refreshes the required reference snapshots in `snapshots/`.

```powershell
cd integration/browser
pnpm install
pnpm test
```

The gate covers login, the grouped application shell, direct and enhanced task routes, a real script Run, Quick Run grouping/editing/copying/soft-locking, localized output, history navigation, keyboard dismissal, status JSON, console errors, and horizontal overflow at the agreed `1440 × 1000` desktop viewport. Quick Runs and variable-value controls also receive targeted `390 × 844` mobile layout and snapshot checks; Quick Run content is verified without JavaScript. It deliberately does not add a production Node.js dependency or a CI workflow.

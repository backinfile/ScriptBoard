# Chromium browser gate

This test-only suite launches a deterministic local ScriptBoard fixture, exercises the real server-rendered application in Chromium, and refreshes the required reference snapshots in `snapshots/`.

```powershell
cd integration/browser
pnpm install
pnpm test
```

The gate covers login, the grouped application shell, direct and enhanced task routes, a real script Run, localized output, history navigation, keyboard dismissal, status JSON, console errors, and horizontal overflow at the agreed `1440 × 1000` desktop viewport. The variable-value controls also receive a targeted `390 × 844` mobile layout, touch-target, and snapshot check. It deliberately does not add a production Node.js dependency or a CI workflow.

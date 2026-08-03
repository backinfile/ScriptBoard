# Design schedule

Use to design or adjust a ScriptBoard schedule after the user identifies the intended script and business timing.

1. Resolve the stable script or Quick Run reference and inspect its bounded execution configuration.
2. Ask for missing business intent: time zone, desired local times, acceptable delay, overlap policy and retry expectations.
3. Preview the cron expression and next occurrences before proposing creation or changes.
4. Check timeout, arguments and overlap consequences; prefer a disabled draft when important intent remains uncertain.
5. Stop if the target script is ambiguous, the time zone is unknown, or the requested cadence cannot be represented safely.
6. Report: proposed expression, time zone, next occurrences, overlap/timeout policy, assumptions and approval-requiring action. Never create or change a schedule without the ordinary ScriptBoard approval path.

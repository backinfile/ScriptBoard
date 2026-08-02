# Triage host pressure

Use for current CPU, memory, disk or application resource pressure.

1. Start with a fresh bounded host snapshot and record its observation time.
2. Correlate abnormal applications or containers using stable application identities and bounded metrics.
3. Read only relevant source-log windows when a specific application is implicated.
4. Do not infer causality from one sample; distinguish co-occurrence, sustained pressure and confirmed attribution.
5. Stop when snapshots are stale, the responsible process cannot be resolved safely, or available evidence does not support attribution.
6. Report: current severity, affected resource, leading contributors, evidence time, confidence, and safe next checks. Any state-changing action remains subject to normal approval.

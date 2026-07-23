---
name: run-diagnosis
description: Diagnose a Run using its snapshot, status, and ordered Run Log.
version: 1
---

# Run diagnosis

Read the Run metadata and bounded log chunks. Distinguish launch rejection, script failure,
timeout, cancellation, and disconnected supervision. Correlate stdout and stderr by event
sequence. Propose a script change only after identifying evidence, and submit changes as one
frozen action batch.

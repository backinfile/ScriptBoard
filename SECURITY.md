# Security Policy

## Supported versions

Security fixes are provided for the latest stable release only. Before reporting an issue, reproduce it on the newest release when it is safe to do so.

## Reporting a vulnerability

Do not open a public issue for a suspected vulnerability. Use GitHub's private vulnerability reporting for this repository. Include the affected version, operating system, deployment topology, required privileges, reproduction steps, impact, and any known indicators of compromise. Remove passwords, API keys, session cookies, private files, and customer data from the report.

We will acknowledge a complete report as soon as practical, coordinate validation and remediation privately, and publish an advisory after a fix or mitigation is available. Please do not test against systems you do not own or have explicit permission to assess.

## Emergency containment

If compromise is suspected, isolate the host from untrusted networks and stop ScriptBoard. From a trusted local administrator shell, run `scriptboard emergency pause-external --confirm PAUSE-EXTERNAL`, revoke each exposed capability with `scriptboard emergency revoke-key --key-id ID --confirm-key-id ID`, and export the verified audit chain to a new absolute path with `scriptboard emergency export-evidence --output PATH`. Preserve the State Root, exported evidence, and service logs before rotating administrator, provider, or database credentials. Reinstall or roll back only from a verified signed release. `update recover` restores the selected operation's pre-update database snapshot and therefore discards later database changes; preserve the current state first. Do not delete logs before collecting evidence.

For a release-signing key incident, follow [the update signing key runbook](./docs/UPDATE-SIGNING-KEY-RUNBOOK.md). Embedded revocation protects only clients that have independently obtained a release containing the revocation list.

## Security boundary

ScriptBoard executes administrator-trusted host scripts and manages host files. It is not a general-purpose sandbox. Keep it behind trusted network controls, use TLS for non-loopback access, configure trusted proxies explicitly, and grant access only to trusted users.

### Credential master key

Recoverable credentials are encrypted with a random master key stored outside State Root. Windows wraps the key with machine-scope DPAPI and relies on the protected file ACL to restrict the blob to the configured service identity, SYSTEM, and administrators. Unix stores the random key in a root- or dedicated-service-owned directory; the directory must be `0700` and the regular key file must be `0600`. Startup, recovery, read-only inspection, and `doctor` reject symlinks, non-regular files, owner changes, group/other Unix permissions, and public Windows trustees.

This protects copied State Root data and separates the Runner identity from credential material. It does not protect against root, administrators, a compromised Broker/service identity, or another process already running as the same Unix UID. Treat the external `secrets` directory as independent recovery material and never copy it into ordinary State Root backups. Deployments requiring hardware-backed at-rest protection must protect that directory with the platform's TPM, encrypted filesystem, or managed credential facility; ScriptBoard does not silently derive a wrapping key from readable machine identifiers.

### External Interface transport and signatures

External Trigger signatures use the body-binding v2 format documented in ADR-0170. They detect request mutation and replay but do not hide the Bearer Key or body. Plain HTTP exposes enough material for an active intermediary to create new valid requests, so use TLS for every untrusted network path.

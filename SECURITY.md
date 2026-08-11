# Security Policy

## Supported versions

Security fixes are provided for the latest stable release only. Before reporting an issue, reproduce it on the newest release when it is safe to do so.

## Reporting a vulnerability

Do not open a public issue for a suspected vulnerability. Use GitHub's private vulnerability reporting for this repository. Include the affected version, operating system, deployment topology, required privileges, reproduction steps, impact, and any known indicators of compromise. Remove passwords, API keys, session cookies, private files, and customer data from the report.

We will acknowledge a complete report as soon as practical, coordinate validation and remediation privately, and publish an advisory after a fix or mitigation is available. Please do not test against systems you do not own or have explicit permission to assess.

## Emergency containment

If compromise is suspected, isolate the host from untrusted networks, stop ScriptBoard, preserve the State Root and service logs as evidence, rotate administrator credentials and external interface keys from a trusted host, revoke exposed provider or database credentials, and reinstall or roll back only from a verified signed release. Do not delete logs before collecting evidence.

## Security boundary

ScriptBoard executes administrator-trusted host scripts and manages host files. It is not a general-purpose sandbox. Keep it behind trusted network controls, use TLS for non-loopback access, configure trusted proxies explicitly, and grant access only to trusted users.

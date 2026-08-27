# MCP Streamable HTTP implementation

## Boundary

ScriptBoard serves stateless MCP at `POST /mcp` from the existing Web listener. It inherits `listen`, TLS, trusted-proxy processing, Allowed Hosts, and `canonical_external_url`; it never opens a second socket. `mcp_enabled` defaults to `true`, while `false` removes MCP and OAuth routes together.

- `internal/mcpaccess` owns OAuth clients, grants, opaque tokens, current-user authorization, and invocation records.
- `internal/mcpserver` owns the official Go SDK Streamable HTTP adapter and filtered tool catalogue.
- `internal/runcontrol` owns Quick Run publication verification and Run ownership rules shared by Web and MCP.

MCP never calls a Web handler and does not expose a generic Broker, filesystem, source-text, configuration, or one-time-code capability.

## OAuth flow

An unauthenticated `/mcp` request returns HTTP 401 and Protected Resource Metadata discovery. Public clients use Authorization Code with PKCE S256 and an exact `/mcp` resource. Consent uses the existing ScriptBoard session; `scriptboard.execute` requires recent Step-up.

Authorization codes are one-use and live five minutes. Access Tokens live ten minutes. Refresh Tokens rotate and have a 30-day absolute family lifetime; reuse revokes the family. SQLite stores hashes and short hints, never complete credentials. Each authentication checks the user's current enabled state, fixed role and `auth_version`, plus client, grant, token and family revocation.

Public clients may use DCR. Client secrets, static Bearer credentials and `client_credentials` are unsupported. Loopback HTTP redirects may vary only by port; other redirect URIs require exact HTTPS matching.

## Scopes and tools

`scriptboard.observe` is available to all enabled fixed roles. `scriptboard.execute` is available to Operator, Maintainer and Administrator and implies observe.

Observe tools are `get_host_status`, `list_quick_runs`, `get_run`, and `get_run_logs`. Execute adds `start_quick_run` and `stop_run`. Tool lists are built after authenticating each stateless request. Starts recheck the published script digest and current resource state. Operator may stop only a Run whose stable initiator ID matches the current user; Maintainer and Administrator may stop any Run.

## Verification checklist

- Network: loopback HTTP, explicit non-loopback HTTP, TLS, reverse proxy, Allowed Host, canonical URL, and `mcp_enabled=false`.
- OAuth: real 401, metadata, PKCE, exact resource/redirect, code replay, expiry, refresh rotation/reuse, revocation, role/password/enable changes.
- Tools: role/scope matrix, hidden tool calls, publication changes, overlap, 24-hour idempotency, stop ownership, bounded redacted logs, and cancellation.
- Interoperability: Codex, MCP Inspector, and a DCR-capable generic client.
- Deployment: follow the local-deployment workflow and retain the deployment, test data, and dated report under `docs/test-reports/`.

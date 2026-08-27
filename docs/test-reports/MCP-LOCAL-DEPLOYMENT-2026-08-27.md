# MCP local deployment verification — 2026-08-27

## Retained deployments

- Enabled: `D:\Github\worktrees\ScriptBoard\mcp-streamable-http\.scratch\mcp-local-2026-08-27`, `http://127.0.0.1:18787`, final PID `44028`.
- Disabled: `D:\Github\worktrees\ScriptBoard\mcp-streamable-http\.scratch\mcp-disabled-2026-08-27`, `http://127.0.0.1:18788`, final PID `21668`.
- Login username: `admin`. The generated 32-character initial password remains only in each deployment's protected State Root and is not copied into this report.
- Test clients, OAuth grants, token hashes, invocation data, and other test state are intentionally retained in the enabled deployment database.

## Checks

| Check | Result |
| --- | --- |
| Main listener bound only to `127.0.0.1` | Pass |
| Protected Resource Metadata binds exact `http://127.0.0.1:18787/mcp` resource | Pass |
| Authorization Server Metadata declares code, refresh and PKCE S256 | Pass |
| Unauthenticated `POST /mcp` returns real HTTP 401 | Pass |
| 401 includes `WWW-Authenticate` discovery and is not HTML | Pass |
| Disallowed Host rejected with HTTP 421 | Pass |
| MCP request larger than 1 MiB rejected with HTTP 413 | Pass |
| DCR creates a public client without a client secret | Pass |
| Existing ScriptBoard browser session renders consent | Pass |
| Authorization Code + PKCE exchanges for a 10-minute Access Token | Pass |
| Bearer-authenticated MCP `initialize` returns HTTP 200 and ScriptBoard server info | Pass |
| `mcp_enabled=false` removes `/mcp`, both discovery routes, and `/oauth/authorize` | Pass |
| Full Go repository test suite | Pass |

The real HTTP flow exercised `DCR → session login → consent → authorization-code redirect → PKCE token exchange → Bearer MCP initialize`. No complete authorization code, Access Token, Refresh Token, password, Cookie, or CSRF token was printed or written to this report.

TLS, explicit non-loopback plaintext, and trusted reverse-proxy behavior are covered by the existing configuration/bootstrap/proxy test suites. They were not exposed on a physical non-loopback adapter during this local run; that avoids opening the development machine to its LAN. The production warning and shared-listener behavior are documented in both READMEs.

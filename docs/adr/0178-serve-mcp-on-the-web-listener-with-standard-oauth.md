# 0178: Serve MCP on the Web listener with standard OAuth

## Status

Accepted

## Decision

ScriptBoard exposes stateless Streamable HTTP MCP at `/mcp` on the existing Web listener. It inherits TLS, Allowed Hosts, canonical external URL, and trusted-proxy handling. There is no MCP-specific listener or remote switch.

Protected requests use OAuth Authorization Code with PKCE S256 and resource indicators. ScriptBoard acts as authorization server for public clients and stores only opaque credential hashes. Static tokens, environment-variable tokens, client secrets, token passthrough, and `client_credentials` are unsupported.

OAuth identifies a current local user but does not replace authorization. Every MCP request derives tools from current fixed role, scopes, `auth_version`, client/grant state, resource state, and Run ownership. Web and MCP share the Run-control module.

## Consequences

The secure default remains the loopback listener. Deliberately exposing the main listener also exposes MCP under the same HTTP or HTTPS policy, including the risks of non-loopback plaintext transport. Generic MCP clients can discover and authenticate without Codex-specific credentials; clients unable to perform OAuth are unsupported.

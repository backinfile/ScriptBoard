# Centralize process launch policy

All production Go code constructs child processes through `internal/processlaunch`. The boundary requires a context and an explicit choice between inheriting the service environment and supplying an exact environment; an unspecified policy, malformed environment entry, invalid UTF-8, or NUL-bearing executable/argument is rejected before `os/exec` is reached.

Domain modules remain responsible for stronger argument types and executable trust because a firewall rule, database identifier, script path, and service operation have different valid grammars. Shell interpreters may receive only fixed application-owned programs; untrusted values must use typed arguments or an encoded data channel. A repository-wide AST test rejects direct production use of `exec.Command` and `exec.CommandContext`, including aliased imports, outside the shared launcher.

This boundary does not itself grant a child identity or sandbox. Run Worker, privileged Broker, platform Job Objects/namespaces, and resource limits build on this process construction seam.

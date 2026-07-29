# Own Website Monitoring in a Bounded Module

ScriptBoard provides a neighboring Website Monitor context for checking HTTP,
HTTPS, WebSocket, and WSS endpoints from the current host. It is not part of
Host Status: Host Status describes resource facts about this machine, while
Website Monitor owns endpoint configuration, active checks, failure
confirmation, incidents, history, and Nginx discovery.

`internal/websitemonitor` is the deep module boundary. The web layer submits
typed configuration and renders returned monitor facts; it does not implement
network protocol rules, scheduling, state transitions, retention, or Nginx
parsing. The module limits the active set to 100 monitors, limits concurrent
checks to 10 by default, coalesces checks per monitor, confirms a failure with a
second check, and invalidates late results with a configuration generation.

HTTP checks support GET and POST, bounded response inspection, status or
keyword success rules, redirect limits, TLS verification, and a local dial
override that preserves the URL Host and TLS SNI. WebSocket checks keep
application text/binary messages separate from RFC 6455 control frames.
Ping/Pong success requires an actual Pong control frame whose opaque payload is
byte-for-byte equal to the sent Ping payload; a text or binary frame with the
same bytes is not success. Decoded control-frame payloads are limited to 125
bytes.

Nginx discovery is an explicit read-only scan followed by a separate selected
import. It reads explicit paths, running-process `-p`/`-c` arguments, and known
platform defaults under bounded file, byte, and include-depth limits. It never
starts, reloads, or modifies Nginx and never scans listening ports.

Raw check results are retained for 24 hours, half-hour availability buckets are
derived from those persisted results, hourly aggregates are retained for 30
days, and completed incidents plus soft-deleted monitor records are retained
for one year. This keeps the local database bounded without turning
ScriptBoard into a long-term observability or notification platform.

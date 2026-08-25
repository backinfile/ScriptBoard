# ADR 0177: Render custom tabs as origin-bound browser frames

## Status

Accepted

## Context

ScriptBoard needs instance-defined links to local HTTP/HTTPS tools while optionally retaining the target site's own browser state or passing a target-specific Key. A server-side proxy would broaden SSRF and credential boundaries, and putting a Key in a URL would expose it through history, logs and referrers.

## Decision

The browser loads each target directly in an iframe. Every frame response emits a `frame-src` policy containing only the configured target Origin and uses a mode-specific sandbox. Isolated mode omits `allow-same-origin`; target-state and Key modes grant only the same-origin and storage capabilities needed by the target, without navigation, popup, download or device capabilities.

Keys are encrypted at rest with a purpose bound to the tab ID and target Origin. The parent obtains a short-lived nonce, sends it to the exact target Origin, validates the reply's source, Origin, tab ID, protocol version and nonce, then consumes the challenge once to retrieve and forward the Key with another exact-Origin message. Keys are never placed in the URL or rendered HTML. HTTP remains supported as an explicit plaintext choice and is visibly marked as risky.

Each tab also stores a non-empty set of visible fixed roles. Navigation, stable frame routes and Key delivery all enforce that set; Key tabs retain the stricter manage-operations permission boundary. External-tab navigation is a native document navigation because the browser must apply the frame page's target-specific CSP before loading its iframe.

## Consequences

Targets must permit framing and implement the versioned message handshake to receive a Key. Existing target cookies remain owned by the target Origin rather than ScriptBoard. Pages that deny framing or browsers that block HTTPS-to-HTTP mixed content cannot be made compatible by this feature; users can still open those targets in a separate window.

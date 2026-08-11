# Keep public dashboards result-only

Public custom dashboards render only card names, derived values, freshness, website availability, latency, and SSL status. Source URLs, request headers, JSON paths, formulas, monitor identifiers, and raw errors remain authenticated configuration because making a dashboard shareable must not also publish credentials or integration structure; public rendering therefore resolves website references and sanitizes data cards on the server instead of exposing a reusable configuration payload.

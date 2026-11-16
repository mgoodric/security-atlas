- **docs(auth):** slice 325 — OAuth grants landing map. New
  contributor-facing reference page at `docs-site/docs/oauth-grants.md`
  mapping every registered OAuth endpoint and grant type to the Go
  file that lands it, plus a common-validators table (`oauthclient`,
  `tokensign`, `keystore`, `jwtmw`, `pkce` helpers) and a "Where the
  JWT claims come from" subsection documenting the five
  atlas-namespaced claims (`atlas:idp_issuer`,
  `atlas:current_tenant_id`, `atlas:available_tenants`, `atlas:roles`,
  `atlas:super_admin`) and the RFC 8693 tenant-switch contract. Sourced
  by direct crawl of `internal/api/oauth/*.go` handler registrations —
  no aspirational rows. `password` and `refresh_token` grants are
  documented under "Grants intentionally omitted" with the reason
  (not implemented in v1). mkdocs nav entry added adjacent to the
  REST API reference; ADR-0003 gains a one-line `See also:` pointer.
  Closes the contributor-onboarding gap surfaced by the 2026-05-27
  architecture review ("eight Go files plus integration tests for one
  wire surface — future contributors will benefit from a one-page
  'where does each grant land' map"). Decisions log at
  `docs/audit-log/325-oauth-grants-landing-map-decisions.md` records
  the file-location call, the AC-2 expected-list reconciliation, the
  symbol-name corrections, and the OpenAPI spec drift identified as
  spillover.

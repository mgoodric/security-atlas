- **chore(auth):** slice 326 — `legacyBearerDeprecation` 410-Gone
  responder for `atlas_`-prefixed legacy bearers is retired. The
  slice 191 410-Gone responder + its supporting helpers + the
  JWT/legacy coexistence comment block are removed from
  `internal/api/httpserver.go`. The slice 191 → 197 → 201 cutover
  closed all known callers; the 6-day deprecation window
  (2026-05-21 → 2026-05-27) elapsed with no production
  integrations on the legacy path. Post-retirement contract:
  requests carrying a legacy `atlas_`-prefixed bearer traverse
  the slice 190 JWT middleware (`jwtmw.extractJWT` rejects the
  shape as non-JWT and passes through) and are terminated by the
  slice 197 `requireCredential` gate with 401 Unauthorized —
  NEVER 410, NEVER an authorized handler. The slice 191
  migration URL (`<ATLAS_OAUTH_DEPRECATION_URL>` or default
  `<issuer>/docs/migration/oauth`) remains documented in v1.X
  release-notes for any holder still on the legacy path. The
  `Server.deprecationMigrationURL` field +
  `AttachDeprecationMigrationURL` wiring +
  `ATLAS_OAUTH_DEPRECATION_URL` env var are PRESERVED as
  reserved-for-future-deprecation-events config knobs; the next
  API cutover can mount a new responder without re-plumbing
  config. Load-bearing regression test at
  `internal/api/legacy_bearer_retirement_test.go` (integration
  build tag) asserts the elevation-of-privilege guard from the
  slice 326 threat model (P0-326-1). Decisions log at
  `docs/audit-log/326-legacy-bearer-410-responder-retirement-decisions.md`.

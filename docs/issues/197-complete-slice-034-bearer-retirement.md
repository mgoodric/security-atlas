# 197 — Complete slice 034 bearer-middleware retirement (test-fixture migration + removal)

**Cluster:** Backend / Auth / Testing
**Estimate:** 2-3d
**Type:** AFK (mechanical migration; no judgment calls)
**Status:** `not-ready` (gate: slice 191 merged · slice 196 merged · CI-fixture-pattern alignment for JWT-test-bearers)

## Provenance

Surfaced during slice 191 (PR #454). The slice 191 spec called for
removing slice 034's `httpAuthMiddlewareWithExemptions` mount from
`internal/api/httpserver.go` (P0-191-11). CI caught a cascade: ~60
integration tests across multiple packages issue legacy slice-034
bearers via `credstore.Issue()` in fixtures and expect them to
authenticate. Removing the mount breaks every one of those tests.

Slice 191 shipped a **partial cutover**: the slice 191 410
deprecation responder catches `atlas_` PROD-prefix bearers (the
real legacy api*keys); `atlas_test*` test-prefix bearers fall
through to the slice 034 middleware (still mounted). This honors
the spirit of P0-191-11 for production while keeping the test
suite green.

Slice 197 completes the cutover by:

1. Migrating every integration test fixture from `credstore.Issue()`
   to a JWT-based bearer minted via `tokensign`.
2. Removing the `atlas_test_` carve-out from
   `legacyBearerDeprecation` in `internal/api/httpserver.go`.
3. Removing the `httpAuthMiddlewareWithExemptions` mount.
4. Removing the `legacyBearerDeprecation` exempt-list narrowing
   (every legacy prefix now hits 410 unconditionally).

## Narrative

The slice 034 bearer middleware is the last v1 auth path that runs
opaque tokens. Slice 187-191 graduated the production hot path to
JWT (RFC 9068). The test infrastructure remained on the legacy
shape because rewriting fixtures is mechanical-but-substantial.

Test files that need migration (audit-trail count from slice 191
PR diff):

- `internal/api/anchors/state_integration_test.go`
- `internal/api/schemaregistry/integration_test.go`
- `internal/api/anchors/integration_test.go`
- ~57 others across `internal/api/*/integration_test.go`

The migration pattern per file:

```go
// BEFORE (slice 034 fixture):
cred := srv.IssueAdmin(tenantID).Token

// AFTER (slice 197):
cred := mustMintTestJWT(t, srv.JWTSigner(), jwt.AtlasClaims{
    CurrentTenantID: tenantUUID,
    SuperAdmin:      true,
    ...
})
```

A shared `internal/api/testjwt/` helper package provides
`mustMintTestJWT(t, signer, claims) string` to keep boilerplate
out of each test file.

## Acceptance criteria

- **AC-1.** NEW `internal/api/testjwt/` package with
  `MustMint(t, signer, claims) string` + `MustMintAdmin(t, signer,
tenantID) string` + similar role-specialized helpers.
- **AC-2.** Every `*_integration_test.go` file in `internal/api/*/`
  migrated from `credstore.Credential` token bearers to JWT bearers
  via testjwt helpers.
- **AC-3.** `legacyBearerDeprecation` in
  `internal/api/httpserver.go` simplified: `atlas_test_` carve-out
  removed; all `atlas_` prefixes return 410.
- **AC-4.** `httpAuthMiddlewareWithExemptions` mount REMOVED from
  `internal/api/httpserver.go`.
- **AC-5.** `httpAuthMiddlewareWithExemptions` function still
  exists (used by `securityheaders_integration_test.go` +
  `metrics_endpoint_test.go` — those tests test the middleware
  itself).
- **AC-6.** `Go · integration (Postgres RLS)` CI job green.
- **AC-7.** Self-host bundle e2e green (depends on slice 196).

## Anti-criteria (P0)

- **P0-197-1.** Does NOT delete the `credstore` package or the
  `apikeystore.Authenticate` API surface. Slice 196 still uses
  `apikeystore.Authenticate` from the migration tool to look up
  source api_keys.
- **P0-197-2.** Does NOT change the slice 191 410 response body
  shape. `{"error":"api_key_deprecated","migration_url":"..."}`
  is the stable shape.
- **P0-197-3.** Migration is one PR per integration test package
  (no megacommit). Per-package PRs keep review tractable.

## Dependencies

- **#191** — slice 191 must merge first (delivers
  legacyBearerDeprecation + tokensign + jwtmw).
- **#196** — bootstrap container OAuth migration (gates the
  self-host bundle smoke test).

## Skill mix (2-3)

- `tdd` (each migrated test file is its own verification)
- `simplify` (the testjwt helper consolidates duplication across
  fixtures)
- `ship-gate` (slice 197 closes the cutover — the integration test
  green + self-host green + no `atlas_test_` carve-out in
  httpserver.go are the load-bearing verifications)

## Notes for the implementing agent

The testjwt helper signature is the load-bearing decision. Look at
slice 187's `jwt.AtlasClaims` shape, slice 188's
`buildAtlasClaimsForUser`, and slice 190's middleware bridge to
`credstore.Credential` synthesis. The bridge gives the helper its
contract: a mint that produces a JWT whose claim → credential
mapping yields the same in-process credential the test was using
under the old shape.

### Provenance

Filed 2026-05-21 during slice 191 (PR #454) — partial-cutover
compromise needs a follow-on to close.

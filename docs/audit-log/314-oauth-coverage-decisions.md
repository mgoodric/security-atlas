# Slice 314 — decisions log: lift `internal/api/oauth` coverage to 70%+

**Type:** AFK (with real test-design judgment)
**Outcome:** `internal/api/oauth` merged coverage **15.7% → 74.7%**; floor set at **72**.

## What this slice did

1. **Enrolled `internal/api/oauth` in CI's `tests-integration` job.** Added `./internal/api/oauth/...` to the "Run integration tests" package list in `.github/workflows/ci.yml`, and removed `internal/api/oauth` from `KNOWN_UNENROLLED` in `scripts/audit-integration-enrolment.sh` (the slice-345 guard). The slice-187/188/189/190/191 integration suites carried `//go:build integration` but had never been enrolled, so they had never run in CI.
2. **Added unit suites** for the pre-DB RFC-conformance branches, one file per RFC area, with real RFC-error-code/claim assertions.
3. **Added a `DBUserResolver` integration suite** (the authorize integration suite used a stub resolver, leaving the real slice-192 resolver at 0%).
4. **Set the floor** at `floor(74.7 − 2) = 72`.

## Measurement (local, parity with CI's gocovmerge gate)

- Brought up Postgres 16, bootstrapped `atlas_app`, applied forward migrations (same sequence as `ci.yml`).
- Unit profile: `go test -covermode=atomic -coverpkg=./internal/api/oauth/... ./internal/api/oauth/` → 45.6%.
- Integration profile: `go test -tags=integration -p 1 -covermode=atomic -coverpkg=./internal/api/oauth/... ./internal/api/oauth/...` → 74.7%.
- Merged via `gocovmerge` → **74.7%** (integration is a superset of unit here).
- `coverage-gate -profile=<merged>` confirms `internal/api/oauth: got 74.7%` against the 72 hard floor (passes; the only oauth warning is the non-blocking advisory-90% tier marker).
- `golangci-lint run ./internal/api/oauth/` → 0 issues. `-race` integration run clean.

## Test-design judgment

### The unit/integration split (what each tier owns)

The OAuth AS handlers split cleanly into **pre-DB request-validation/auth branches** (unit-testable) and **DB-backed effect branches** (integration-only). The dividing line is the first call into a `*Store`:

- **Unit-covered (no Postgres):** content-type rejection, missing/invalid required params, RFC error-code formatting, PKCE method gate, the self-revoke-via-bearer auth path (signer + `jwt.Validate` only — rejects on subject-mismatch BEFORE the revocation write), the not-configured 503 branches, the in-memory `devicePollTracker`, and the pure claim-projection functions (`buildDeviceCodeClaims`, `buildAtlasClaimsForUser`).
  - **Key enabler:** `oauthclient.Store.Verify` returns `ErrUnknownClient` _before_ dereferencing the pool when a credential is empty. That makes the inspector/revoker **auth-failure** branches reachable with a `New(nil)` store — so introspect/revoke 401 paths are unit-tested without a DB. The **auth-success** paths (which would deref the nil pool) are deliberately left to integration.
- **Integration-covered (real Postgres + RLS):** the `*Store` CRUD (`DeviceCodeStore`, `oauthcode`, `oauthclient`, `revocation`), the audit-log writes, the happy-path token mints, the introspect `{active:true}` path, the full authorize redirect flow, and the slice-192 `DBUserResolver`.

This is the same "enrolment is the load-bearing move" pattern as slices 290/297/310/313/315/317/318/319/320.

### What I deliberately did NOT unit-test (and why)

- **The S256-default downstream effect in authorize** (`code_challenge_method` omitted → defaults to S256, then proceeds to the registry lookup). Reaching the observable effect requires the request to pass the method gate into `LookupRedirectURI`, which against a `nil` pool **panics** (not a clean error) and crashes the test server. I removed the attempted unit test and left a comment; the S256 default's effect is covered in `authorize_integration_test.go`. The explicit-`plain`-rejection gate is the unit-testable half and is asserted.
- **`enumerateMemberships` + `lookupSuperAdmin`** in the user resolver. These require the BYPASSRLS `authPool` (cross-tenant `users` enumeration + the no-RLS `super_admins` table). The new `user_resolver_integration_test.go` wires `authPool=nil` — the single-tenant resolution path — which covers `ResolveForOAuth` steps 1+3 (`readSessionIdentity` + `queryUserRoles`) without the cross-tenant plumbing. The cross-tenant + super_admin branches are a candidate for a focused follow-on if that shape needs dedicated coverage; they were below the leverage needed to clear 70% and would introduce a new migrate-role pool dependency into the test harness.

### Assertion rigor (P0-314-4)

Tests assert RFC-specific outcomes, not just status codes:

- Error paths assert the RFC 6749 §5.2 / 7009 / 7662 / 8628 error **codes** (`invalid_request`, `invalid_client`, `invalid_token`, `unsupported_response_type`, `slow_down`, `server_error`), not merely the HTTP status.
- The token-exchange super_admin tests (pre-existing) + the new `buildDeviceCodeClaims`/`buildAtlasClaimsForUser` tests assert the **copy-not-synthesize** invariant (P0-188-4) in both the true and false directions.
- The PKCE tests assert the RFC 7636 Appendix B vector and the S256 challenge shape.
- The device user_code test asserts the P0-191-4 unambiguous-alphabet membership over 64 draws.

### Test-fixture token hygiene

All fixture tokens are neutral placeholder strings (`atlas-opaque-target-token`, `atlas-device-code-value`, etc.). The one structurally-JWT-shaped fixture (`TestRevoke_SelfRevokeRejectsUnsignedBearer`) uses a hand-built `eyJhbG…`-prefixed string that is a real JOSE header but a deliberately-invalid signature — required to exercise the `strings.HasPrefix(bearer, "eyJ")` shape guard in `revoke.authenticate`. It is not a real credential and carries no vendor prefix.

## Bug surfaced by enrolment → fixed in-suite (NOT a spillover)

`device_code_integration_test.go` misused the shared `postForm` helper, which **always** appends `oauth.PathToken`. The device suite passed `srv.URL+"/oauth/device_authorization"` as the base, so the request actually went to `…/oauth/device_authorization/oauth/token` → 404. The token-poll sites passed `srv.URL+"/oauth/token"` → `…/oauth/token/oauth/token` → 404. Because the package was never enrolled, this latent test bug went undetected across five slices (187–191).

**Disposition:** fixed in-suite rather than spilled out. Rationale: the slice's explicit purpose is to make these integration tests _actually run green in CI_; enrolling a red suite would defeat the slice. The fix is test-only (added a `postFormTo` helper that posts to an explicit full URL; repointed the poll sites to `postForm(srv.URL, …)`). No production code changed. This is the "genuine bug a newly-run integration test surfaces" case the brief anticipated — but it is a **test** bug, in the package under test, directly on the critical path of the AC, so it belongs in this slice.

## Spillover

None filed. The only finding (the device-flow test-path bug) is a test-only defect on this slice's own critical path and was fixed here per the reasoning above. No production-code bug was surfaced.

## Files touched

- `.github/workflows/ci.yml` — enrol `./internal/api/oauth/...`.
- `scripts/audit-integration-enrolment.sh` — remove `internal/api/oauth` from `KNOWN_UNENROLLED`.
- `cmd/scripts/coverage-thresholds.json` — add `internal/api/oauth: 72`; append slice-314 `$comment`.
- `internal/api/oauth/introspect_test.go` (new)
- `internal/api/oauth/revoke_test.go` (new)
- `internal/api/oauth/authorize_extra_test.go` (new)
- `internal/api/oauth/wellknown_test.go` (new)
- `internal/api/oauth/device_test.go` (new)
- `internal/api/oauth/helpers_test.go` (new)
- `internal/api/oauth/user_resolver_integration_test.go` (new)
- `internal/api/oauth/export_test.go` — added test seams (pure-function exports).
- `internal/api/oauth/device_code_integration_test.go` — fix `postForm` misuse + `postFormTo` helper.
- `CHANGELOG.md`

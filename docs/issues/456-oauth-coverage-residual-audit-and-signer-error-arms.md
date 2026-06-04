# 456 — `internal/api/oauth` residual coverage: audit-write + signer-failure error arms

**Cluster:** Quality
**Estimate:** 1d (S-M)
**Type:** AFK
**Status:** `ready`
**Priority:** P2

## Narrative

**WHY.** Surfaced during slice 422. Slice 422 lifted `internal/api/oauth`
merged coverage from 74.7% to 79.0% and the hard floor from 72 to 77 by
covering the reachable RFC DENY branches (token-exchange refusals,
device-code `expired_token` / cross-client `invalid_grant`, introspect
`{active:false}` arms). It deliberately stopped short of the 90%
security-critical advisory (slice 350) per its own scope discipline. The
residual ~11pp is concentrated in a small set of error arms that need
more plumbing than slice 422's budget allowed.

**WHAT.** Cover the residual `internal/api/oauth` error arms toward the
90% advisory and lift the hard floor again (same-PR monotonic ratchet,
slice 069):

1. **Best-effort audit-write failure paths** — `token.go writeAudit`
   and `pkce.go writeAuthCodeAudit` have branches for
   `tenancy.WithTenant` failure, `BeginTx` failure, `ApplyTenant`
   failure, the empty-jti fallback, and the `Exec` failure (each a
   silent return that must NOT block the token response, per decision
   D3). These need a fault-injecting pool or a seam to drive the
   error returns deterministically.
2. **Signer-failure 500 arms** — the `t.signer.Sign(...) → server_error`
   branches in the client_credentials, token-exchange,
   authorization_code, and device_code handlers (`token.go:272/366`,
   plus the auth-code + device-code mint sites). A failing signer stub
   (or a keystore put into a failure state) drives the 500.
3. **Rate-limiter internals** — the `tokenBucketLimiter` overflow cap
   (`token.go:574`) and `WindowSeconds`/`max` edge arms
   (`token.go:590/597`). These are not security DENY branches but are
   cheap statement coverage via the existing exported test seams.

**SCOPE DISCIPLINE.** Coverage + floor lift only — no AS runtime
behavior change. If driving the audit-write failure arms requires a new
test seam (e.g. an injectable audit-writer interface or a
fault-injecting pool wrapper), that seam must be unexported and
`New*Endpoint` must stay byte-for-byte unchanged (slice 409 precedent).
Closing the entire gap to 90% may itself spill if the audit-write fault
injection proves to need integration plumbing beyond this slice.

## Threat model

**Verdict:** `has-mitigations`. The audit-write failure arms are a
**Repudiation** surface (a silently-dropped audit row weakens forensic
reconstruction of a tenant-switch), and the signer-failure arms are an
**availability** surface (a 500 the caller can retry). Neither is an
EoP/spoofing headline like slice 422's branches — which is why they are
the residual tail rather than slice 422's core. The tests assert the
SECURE outcome: an audit-write failure does NOT block nor corrupt the
token response (D3), and a signer failure surfaces `server_error` (500)
without leaking internal detail (composes with slice 367).

## Acceptance criteria

- [ ] **AC-1 (test).** `token.go writeAudit` + `pkce.go
writeAuthCodeAudit` failure arms covered (tenant-context failure,
      begin/apply/exec failure, empty-jti fallback) — each asserting the
      token response is still 200 (best-effort, non-blocking, D3).
- [ ] **AC-2 (test).** Signer-failure `server_error` (500) arms covered
      across the client_credentials, token-exchange, authorization_code,
      and device_code mint sites, asserting the body carries only the
      RFC error code + generic description (no internal detail — slice
      367).
- [ ] **AC-3 (test).** Rate-limiter overflow cap +
      `WindowSeconds`/`max` edge arms covered via the existing exported
      seams.
- [ ] **AC-4.** `internal/api/oauth` measured merged coverage rises
      materially above the slice-422 floor of 77 (target: into the
      mid-to-high 80s); residual gap to 90 documented if any remains.
- [ ] **AC-5.** `cmd/scripts/coverage-thresholds.json` lifts the
      `internal/api/oauth` floor to `max(0, floor(measured − 2pp))` in
      the SAME PR (monotonic ↑, never above measured).
- [ ] **AC-6.** The `90` advisory in `$security_critical_packages` is
      left unchanged.

## Anti-criteria (P0 — block merge)

- **P0-456-1.** Does NOT raise the floor without the tests that hit the
  new bar (slice 069 ratchet).
- **P0-456-2.** Does NOT change any AS runtime behavior — tests + floor
  lift only (plus an optional unexported test seam with `New*Endpoint`
  unchanged). A real bug found → spillover fix slice.
- **P0-456-3.** Does NOT assert only HTTP status — error-arm tests
  assert the RFC error code / the best-effort non-blocking outcome.
- **P0-456-4.** Does NOT modify `_STATUS.md` / `_INDEX.md` from inside
  this slice's own commits.

## Dependencies

- **#422** (`internal/api/oauth` floor 72 → 77, RFC error branches) —
  this slice builds on the floor it established.
- **#350** (security-critical advisory tier) — defines the 90% advisory
  this slice lifts further toward.
- **#367** (error-detail leakage) — composes: the signer-failure arms
  assert no internal-detail leakage.

## Notes for the implementing agent

- Read slice 422's `error_branches_test.go` + the two new integration
  files (`device_deny_integration_test.go`, the introspect
  `{active:false}` additions in `revoke_introspect_integration_test.go`)
  for the established harness + the controllable-clock pattern.
- The audit-write failure arms are the hardest: the audit pool is a
  concrete `*pgxpool.Pool` today. The cheapest fault injection is a
  pool pointed at a DB where `oauth_token_exchanges` is locked /
  missing a required column in a throwaway schema, OR an unexported
  audit-writer seam. Prefer the seam only if the DB approach proves
  flaky.
- No JWT/vendor-shaped fixture literals (GitGuardian, slice 314) —
  neutral test values; mint tokens in-process via the real signer.

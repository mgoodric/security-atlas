# 472 — `internal/api/oauth` coverage: device-approval flow + DBUserResolver BYPASSRLS path

**Cluster:** Quality
**Estimate:** 1d (S-M)
**Type:** AFK
**Status:** `ready`
**Priority:** P3

## Narrative

**WHY.** Surfaced during slice 456. Slice 456 lifted `internal/api/oauth`
merged coverage 79.0% → 80.8% (floor 77 → 78) by covering its three
**named** residual arm-categories (best-effort audit-write failures,
signer-failure 500s, rate-limiter internals). It deliberately stopped at
its named scope. The remaining gap to the slice-350 90% security-critical
advisory is concentrated in two surfaces that belong to **other** slices'
code and were therefore out of slice 456's scope:

1. The **device-approval browser flow** (slice 191 surface):

   - `device_approve.go` `ServeApprove` (~34%), `ServeDeny` (~44%),
     `NewDeviceApprovalEndpoint` (~67%).
   - `device_authorization.go` `Approve` (~37%), `Deny` (0%),
     `Consume` (~79%), `LookupByUserCode` (0%), `readRandom` (~75%).
   - `oauth.go` `AttachDeviceApprovalEndpoint` (0%).

2. The **`DBUserResolver` BYPASSRLS authPool path** (slice 192 surface):
   `user_resolver.go` `NewDBUserResolverWithAuthPool` (0%),
   `enumerateMemberships` (0%), `lookupSuperAdmin` (0%), plus the
   higher-coverage `ResolveForOAuth` / `readSessionIdentity` /
   `queryUserRoles` arms that only the no-authPool path currently exercises.
   This is a real security surface — cross-tenant membership enumeration +
   super_admin lookup — and is in the slice-350 security-critical tier, so
   it is worth its own focused lift.

**WHAT.** Cover these two surfaces toward the 90% advisory and lift the
`internal/api/oauth` hard floor again (same-PR monotonic ratchet, slice 069).
Prefer reusing the existing enrolled integration harnesses
(`device_code_integration_test.go`, `authorize_integration_test.go`,
`user_resolver_integration_test.go`) — most of these arms need DB state
(an approved device-code snapshot; a `super_admins` row; multi-tenant
`users` + `user_roles` rows + the BYPASSRLS authPool).

**SCOPE DISCIPLINE.** Coverage + floor lift only — no AS runtime behavior
change (an optional unexported test seam is fine if a branch needs one,
with `New*Endpoint` unchanged — slice 409 precedent). Measure with the real
gate flow (merged unit + integration via gocovmerge); lift to
`floor(measured − 2pp)`.

## Acceptance criteria

- [ ] **AC-1 (test).** Device-approval flow arms covered: `ServeApprove`
      success + deny + the error branches; `Approve` / `Deny` / `Consume` /
      `LookupByUserCode` against a real device-code row.
- [ ] **AC-2 (test).** `DBUserResolver` BYPASSRLS path covered:
      `enumerateMemberships` (cross-tenant), `lookupSuperAdmin` (present +
      absent), via a multi-tenant seed + a `super_admins` row + the authPool.
- [ ] **AC-3.** `internal/api/oauth` measured merged coverage rises
      materially above the slice-456 floor of 78; residual to 90 documented.
- [ ] **AC-4.** `cmd/scripts/coverage-thresholds.json` lifts the
      `internal/api/oauth` floor to `max(0, floor(measured − 2pp))` in the
      SAME PR (monotonic ↑).
- [ ] **AC-5.** The `90` advisory in `$security_critical_packages` is
      left unchanged.

## Anti-criteria (P0 — block merge)

- **P0-472-1.** Does NOT raise the floor without the tests that hit the new
  bar (slice 069 ratchet).
- **P0-472-2.** Does NOT change any AS runtime behavior — tests + floor lift
  only. A real bug found → spillover fix slice.
- **P0-472-3.** Does NOT assert only HTTP status — error-arm tests assert
  the RFC error code / the specific secure outcome.
- **P0-472-4.** Does NOT modify `_STATUS.md` / `_INDEX.md` from inside this
  slice's own commits.

## Dependencies

- **#456** (residual audit-write + signer-failure + rate-limiter arms; floor
  77 → 78) — this slice builds on the floor it established.
- **#350** (security-critical advisory tier) — defines the 90% advisory.
- Touches surfaces owned by **#191** (device flow) and **#192** (DBUserResolver).

## Notes for the implementing agent

- Read slice 456's `residual_audit_integration_test.go` for the verify-ok/
  sign-fail keystore + the RLS-scoped read helper, and the existing
  `device_code_integration_test.go` + `user_resolver_integration_test.go`
  for the device-approval + user-seed harnesses.
- No JWT/vendor-shaped fixture literals — mint in-process; neutral test
  values only (GitGuardian, slice 314).

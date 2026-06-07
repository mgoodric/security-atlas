# Slice 472 — `internal/api/oauth` coverage tail (device-approval + DBUserResolver authPool) — decisions log

**Type:** JUDGMENT (coverage + floor lift only — no AS runtime behavior change)
**Parent:** slice 472 (`docs/issues/472-oauth-device-approval-and-dbuserresolver-coverage.md`)
**Builds on:** slice 456 (floor 77 → 78) · slice 422 (72 → 77) · slice 314 (floor added at 72) · slice 350 (security-critical 90% advisory tier)

## Outcome (numbers)

| Metric                                                     | Value                           |
| ---------------------------------------------------------- | ------------------------------- |
| Baseline merged coverage (slice 456 state, on this branch) | **80.8%**                       |
| New merged coverage (this slice)                           | **89.3%**                       |
| Lift                                                       | **+8.5pp**                      |
| Old hard floor                                             | 78                              |
| **New hard floor**                                         | **87** (`floor(89.3 − 2) = 87`) |
| Security-critical advisory                                 | **90** — left UNCHANGED (AC-5)  |
| Residual gap to 90                                         | **~0.7pp**                      |

Measured with the real gate flow: `go test` unit + `go test -tags=integration -p 1` against a real Postgres 16 (roles + all `migrations/sql/*.sql` applied), merged with `gocovmerge`, scored by `go run ./cmd/scripts/coverage-gate`. Gate exits 0; the `internal/api/oauth` hard floor passes at 89.3% and the advisory correctly prints `89.3% < 90.0%` as a **non-blocking** warning.

## Arms covered

**(1) Device-approval browser flow (slice-191 surface)** — new `device_approval_integration_test.go` (integration; real `DeviceCodeStore`, real `Handler` via `AttachDeviceApprovalEndpoint`, credential injected the way the upstream bearer middleware does):

- `oauth.go:151 AttachDeviceApprovalEndpoint` (0% → covered)
- `device_approve.go ServeApprove` (34.5% → 93.1%): success + the `cred.TenantID != ""` snapshot-build branch (AvailableTenants + RolesJSON marshal) + the no-tenant branch; the store-error mappings — not-found→404 `invalid_request`, expired→400 `expired_token`, denied/consumed→400 `invalid_grant`, already-approved race→400 `invalid_grant`.
- `device_approve.go ServeDeny` (44.4% → 83.3%): success (consumed_at set, snapshot NOT written) + not-found→404.
- `device_authorization.go` `Approve` (36.8% → 89.5%), `Deny` (0% → 85.7%), `LookupByUserCode` (0% → covered) against real rows; the Approve 0-rows-affected diagnostic ladder (not-found / consumed / expired / raced).

**(2) DBUserResolver BYPASSRLS authPool path (slice-192 surface)** — new `user_resolver_authpool_integration_test.go` (integration; `NewDBUserResolverWithAuthPool` with the BYPASSRLS admin pool from `DATABASE_URL`):

- `user_resolver.go:70 NewDBUserResolverWithAuthPool` (0% → covered)
- `enumerateMemberships` (0% → 83.3%): cross-tenant — one OIDC subject (idp_issuer+idp_subject) active in TWO tenants → both in `available_tenants`, per-tenant roles resolved from the correct tenant-scoped user_id; PLUS the session-fallback append branch (`user_resolver.go:261`) via a `disabled` session user the active-filter enumeration misses.
- `lookupSuperAdmin` (0% → 80.0%): present (super_admin true) AND absent (false) — the `COUNT(*) > 0` branch both ways. P0-188-4: super_admin is read from `super_admins`, never synthesized.
- `ResolveForOAuth` (63.0% → 85.2%) — the authPool steps 2/4 now exercised.

**(3) Pure-Go constructor arms** — new `device_approval_constructor_test.go` (unit, no DB, slice-353 Q-2 fast loop): `NewDeviceApprovalEndpoint` nil-codes fail-loud panic + nil-Now default.

## Decisions

- **D1 — floor = 87, not higher.** `floor(measured − 2pp)` per the slice-069 monotonic ratchet (P0-472-1). 89.3 − 2 = 87.3 → floor 87. Leaves a 2.3pp noise band below the measured value; the integration tier is `-p 1` serialized with real services, so the 2pp band absorbs ordering jitter.
- **D2 — authPool = `DATABASE_URL` (BYPASSRLS), not `DATABASE_URL_APP`.** The cross-tenant `users` enumeration and the no-RLS `super_admins` lookup are only reachable on a BYPASSRLS connection — the exact production wiring (the `atlas_migrate` pool) and the same model the `adminsuperadmins` integration suite uses. The new `openAuthIntegrationPool` skips when `DATABASE_URL` is unset, so the suite degrades gracefully where only the app pool is provided (it does NOT silently fall back to the authPool=nil path, which slice 314 already covers).
- **D3 — assert the RFC error CODE, never just the status (P0-472-3).** Every deny-arm test decodes the OAuth error body and asserts the specific code (`invalid_request` / `expired_token` / `invalid_grant`), matching the slice-422/456 discipline.
- **D4 — no runtime behavior change (P0-472-2).** Tests + floor lift + decisions log only. `AttachDeviceApprovalEndpoint`, `ServeApprove/Deny`, `DeviceCodeStore`, and `DBUserResolver` are unmodified. No new test seam was needed (the existing public constructors + `Handler.Mount` sufficed) — cleaner than the slice-456 `export_test.go` seam route.
- **D5 — session-fallback test uses `disabled`, not `suspended`.** The `users_status_check` admits only `active` / `disabled`; `disabled` is the only non-active value that excludes the row from the `status = 'active'` enumeration filter, forcing the `sawSession=false` fallback.

## Residual to 90 (AC-3)

The remaining ~0.7pp is genuinely hard-to-reach and/or owned by other slices:

- Resolver `readSessionIdentity` / `queryUserRoles` `BeginTx` / `ApplyTenant` mid-transaction-failure arms — defensive, reachable only on a connection broken mid-tx (same class slice 456 flagged for the audit ApplyTenant arm).
- `readRandom` crypto-failure arm; `Consume` race-window arms.
- A long tail of initiate/authorize/introspect/revoke error paths (`device_authorization.go ServeHTTP`, `authorize.go`, `introspect.go`, `revoke.go`) outside slice 472's named scope (device-approval + DBUserResolver).

**Advisory-promotion note (per the slice instruction):** the package is now ~0.7pp under the 90 advisory. A single follow-on covering the cross-package initiate/authorize/introspect/revoke error tail could clear 90 and make promoting the advisory to a hard floor viable. This slice deliberately does **NOT** change the advisory mechanism — that promotion is a separate decision that also requires draining arms owned by slices 187/189/190/191. Filed as a forward note here only.

## Confidence

**High** for the two named surfaces (real DB, real handlers, RFC-code assertions, both super_admin arms). **Medium** that 90 is reachable without touching other-slice code — the residual tail spans four other endpoints.

## Detection-tier classification (slice 353 Q-13)

- `detection_tier_actual`: **none** — no bug surfaced during the slice. The `granted_via` CHECK and `users_status` CHECK were fixture-shape corrections caught locally during first run, not product defects.
- `detection_tier_target`: **none**.

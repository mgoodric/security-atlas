# 422 — Lift `internal/api/oauth` coverage toward the 90% security-critical advisory

**Cluster:** Quality
**Estimate:** 1-2d (M)
**Type:** AFK
**Status:** `ready`
**Priority:** P1

## Narrative

**WHY.** `internal/api/oauth` is on the slice-350 security-critical
advisory roster (`$security_critical_packages` in
`cmd/scripts/coverage-thresholds.json`, `advisory_target_pct: 90`), but
its hard floor is **72** and measured coverage post-slice-314 is
~74.7%. That is ~15pp under the advisory. The dangerous part is _which_
25% is untested: the RFC error branches — `invalid_grant`,
expired/revoked authorization code, denied tenant-switch (RFC 8693),
refused super*admin escalation, `invalid_client`, `unauthorized_client`.
An untested auth \_error* branch is an auth-bypass-class bug: the
package is the largest auth surface in the tree (921 statements) and is
the OAuth 2.0 Authorization Server that mints every atlas JWT (CLAUDE.md
"Authorization Server"). Slice 314 lifted the _floor_ with
happy-path-weighted coverage; slice 333 Q-4 (`$trigger` in the
thresholds file) flags exactly this gap — "a 75% line-floor can be
satisfied by happy-path coverage while dangerous error branches sit
untested."

**WHAT.** Unit and (where the branch genuinely needs real services)
integration tests that drive the RFC _error_ branches across the
endpoint family — `/oauth/token`, `/oauth/authorize`, `/oauth/introspect`,
`/oauth/revoke`, `/oauth/device_authorization` — asserting the specific
RFC error codes, not just status codes. Lift the hard floor in
`cmd/scripts/coverage-thresholds.json` in the **same PR** as the new
tests (the slice-069 monotonic-ratchet contract).

**SCOPE DISCIPLINE.** This is an error-branch coverage lift, not a
re-architecture and not a 90%-or-bust slice. The realistic target is a
material lift of the hard floor toward the 90% advisory (e.g. into the
low-to-mid 80s) backed by real error-branch tests; the advisory stays
advisory. No new endpoint, no RFC behavior change — only tests + the
floor lift. Closing the _entire_ gap to 90% may spill into a follow-on
if the residual branches require integration plumbing beyond this
slice's budget.

## Threat model

**S — Spoofing (relevant).** An untested `invalid_client` /
`unauthorized_client` branch could let a forged or mis-authenticated
client reach a grant path.

- Mitigation: AC drives client-auth failure branches and asserts the
  RFC 6749 §5.2 `invalid_client` / `unauthorized_client` response — the
  test pins that the spoof path is rejected.

**T — Tampering.** Token-exchange (RFC 8693) with a tampered
`subject_token` or a cross-tenant `audience` must be refused.

- Mitigation: AC covers the denied tenant-switch branch and asserts the
  refusal, so a tampered exchange request cannot silently succeed.

**R — Repudiation.** OAuth grants/denials should be observable.

- Mitigation: tests assert error responses; where the AS already writes
  audit rows on denial, the test asserts the row is present (no new
  audit surface introduced).

**I — Information disclosure.** An error branch that echoes internal
detail (SQL error, stack) in the RFC error body leaks state to an
unauthenticated caller.

- Mitigation: AC asserts error bodies carry only the RFC error code +
  description — never raw internal detail (composes with the slice-367
  errleak discipline).

**E — Elevation of privilege (HEADLINE).** A refused super_admin
escalation or a denied cross-tenant grant that is untested is the
worst-case: a regression there is silent privilege escalation.

- Mitigation: the slice's core ACs drive exactly these branches —
  refused super_admin escalation, denied tenant-switch, expired/revoked
  code — and assert the deny outcome with the specific RFC error.

**Verdict:** `has-mitigations`. EoP + Spoofing are the headline; the
slice's purpose is to make the deny branches non-regressable.

## Acceptance criteria

- [ ] **AC-1 (test).** `/oauth/token` error branches covered: at minimum
      `invalid_grant` (bad/expired/revoked authorization_code),
      `unsupported_grant_type`, `invalid_client`, `unauthorized_client`,
      `invalid_scope` — each asserting the RFC 6749 §5.2 error code in
      the response body.
- [ ] **AC-2 (test).** RFC 8693 token-exchange denied-tenant-switch
      branch covered — a tenant the subject is not entitled to is
      refused with the expected error; a refused super_admin escalation
      branch is covered and asserts the deny.
- [ ] **AC-3 (test).** `/oauth/authorize` error branches covered:
      redirect_uri mismatch, invalid PKCE verifier, missing/invalid
      `response_type`, scope rejection.
- [ ] **AC-4 (test).** `/oauth/introspect` covered for the active /
      expired / revoked branches **and** unauthenticated-client
      rejection.
- [ ] **AC-5 (test).** `/oauth/revoke` covered for `token_type_hint`
      handling + the idempotent already-revoked path.
- [ ] **AC-6 (test).** `/oauth/device_authorization` + device_code grant
      covered for the `authorization_pending` / `expired_token` /
      `access_denied` branches (RFC 8628 §3.5).
- [ ] **AC-7.** Every new test asserts the RFC error _code_ (e.g.
      `invalid_grant`), not merely the HTTP status — no vacuous
      status-only assertions.
- [ ] **AC-8.** `internal/api/oauth` measured merged coverage rises to a
      value materially above the current 72 floor (target: into the low-
      to-mid 80s); the residual gap to the 90% advisory, if any, is
      documented + spilled.
- [ ] **AC-9.** `cmd/scripts/coverage-thresholds.json` lifts the
      `internal/api/oauth` floor to `max(0, floor(measured - 2pp))` in
      the SAME PR — monotonic ↑, never above measured.
- [ ] **AC-10.** The advisory `90` target in `$security_critical_packages`
      is left unchanged (it is advisory; this slice lifts the hard floor,
      not the advisory).

## Constitutional invariants honored

- **Tenant isolation enforced at the DB layer (invariant #6).** The
  token-exchange / introspect tests assert cross-tenant denial — they
  reinforce the RLS-backed tenant boundary, never bypass it.
- **Testing discipline (CLAUDE.md).** Floor lift + tests in one PR;
  monotonic ratchet (slice 069 / 314 methodology).
- **AI-assist boundary.** OAuth AS is critical security infrastructure —
  human-written per-RFC-section branch tests, no LLM-generated test
  bodies.

## Canvas references

- `Plans/canvas/09-tech-stack.md` — Authorization Server (slices
  187-192; RFC 9068 / 8693 / 7636 / 8628 / 7009 / 7662).
- CLAUDE.md "Authorization Server" + "Defect detection-tier
  classification".

## Dependencies

- **#314** (`internal/api/oauth` floor to 72) — `merged`. This slice
  builds on the floor it established.
- **#350** (security-critical advisory tier) — `merged`. Defines the 90%
  advisory this slice lifts toward.
- **#367** (error-detail leakage) — `merged`. Composes: the error-branch
  tests assert no internal-detail leakage.

## Anti-criteria (P0 — block merge)

- **P0-422-1.** Does NOT raise the floor without writing the tests that
  hit the new bar (slice 069 ratchet — tests + lift in one PR).
- **P0-422-2.** Does NOT lower any existing floor; does NOT lower the
  advisory.
- **P0-422-3.** Does NOT assert only HTTP status — every error-branch
  test asserts the specific RFC error code.
- **P0-422-4.** Does NOT modify `_STATUS.md` from inside this slice's own
  commits (orchestrator batch-registers).
- **P0-422-5.** Does NOT change any AS runtime behavior — tests + floor
  lift only; if a test reveals a real bug, file a spillover fix slice.

## Skill mix (3-5)

- `tdd` (per-RFC error-branch tests)
- `claude-api` n/a → `engineering-advanced-skills:api-test-suite-builder`
  (RFC error-branch matrix)
- `Security` (STRIDE EoP re-verification)
- `simplify` (pre-PR)

## Notes for the implementing agent

- Read slice 314's notes + the slice-312 round-3 audit doc
  (`docs/coverage-audit-2026-05-round-3.md`) for the surface map and the
  test-file layout (`token_test.go`, `authorize_test.go`, etc.) already
  established.
- The 25% untested is concentrated in error formatting + deny branches;
  prioritize `invalid_grant` (expired/revoked code), denied
  tenant-switch, and refused super_admin escalation — those are the
  highest-asymmetry (auth-bypass-class) branches.
- Pattern-match assertions to `internal/auth/jwt` (95.5%) for JWS
  validity checks and to slice 367's errleak tests for the
  no-internal-detail assertion.
- Suggested phasing: per-endpoint error-branch test commit, then the
  single floor-lift commit (bisectable).

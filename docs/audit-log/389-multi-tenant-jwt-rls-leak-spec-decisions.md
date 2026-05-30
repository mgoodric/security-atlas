# Slice 389 — decisions log

Multi-tenant JWT harness + real-RLS cross-tenant-leak e2e spec.

This slice is JUDGMENT-typed: the build-time calls (test-JWT harness
shape, seed strategy, the super_admin-vs-RLS reasoning) are recorded
here rather than blocked on a human sign-off. The product-runtime
AI-assist boundary is untouched and irrelevant to this slice.

## Context

Slice 351 authored `web/e2e/tenant-switch.spec.ts` as a `route.fulfill`
mocked spec because the docker-compose bring-up could not provision a
multi-tenant user: `internal/api/testissuejwt.go` minted a single-tenant
JWT (`AvailableTenants: []uuid.UUID{tenant}`), and the RFC 8693
token-exchange tenant-switch requires the target tenant to already be in
`available_tenants[]`. Slice 351 deferred the real-RLS depth assertion to
this slice (its MOCK STRATEGY block + slice 333 Q-9).

## Decisions

### D1 — Harness extension shape: optional `available_tenants[]` + `roles_by_tenant`, backward compatible

Extended the request body of `POST /v1/test/issue-jwt` with two OPTIONAL
fields:

- `available_tenants []string` — when omitted/empty, the minted JWT is
  single-tenant exactly as slice 201 shipped (byte-identical claim
  shape). When supplied, the JWT carries that set verbatim.
- `roles_by_tenant map[string][]string` — per-tenant role lists for the
  multi-tenant case, with the top-level `roles` as the fallback for any
  tenant absent from the map.

Rejected alternative: a separate `/v1/test/issue-multi-tenant-jwt`
endpoint. That would have introduced a second handler + a second mount
gate to keep airtight — strictly more attack surface. One endpoint, one
gate, optional fields, is the smaller and safer change.

### D2 — Production-safety review (P0-201-4): no new signing surface, gate unchanged

The extension is purely additive request-parsing inside the SAME
handler. It:

- still refuses with 404 when `ATLAS_TEST_MODE != "1"` (per-request gate);
- is still mounted ONLY when `testModeEnabled()` is true at boot
  (`httpserver.go`);
- still signs through the single `s.jwtSigner` — no parallel test-only
  signer, no separate keystore, no weakened constraint;
- adds a NEW constraint, not a weakened one: when `available_tenants` is
  supplied it MUST contain `tenant_id` (the current tenant is always a
  member of available tenants — jwt.Validate's tenant-scope invariant).
  A violation is rejected with 400 rather than minting a token the
  validator would later refuse. Malformed UUIDs in the set are also 400.

No production code path can reach the multi-tenant branch that a
production binary couldn't already reach for the single-tenant branch —
the gate is identical. Verified: a production binary never mounts the
route; a misconfigured one still 404s per-request.

### D3 — Assertion target: `risks` (tenant-scoped rows), not `controls` (catalog-global anchors)

The `/controls` list renders SCF anchors, which are catalog-global; only
the per-anchor `state` cell is tenant-scoped, and a missing state renders
as `—` rather than the row vanishing. That makes "row absent" a weaker
signal. `risks` are tenant-scoped rows that render their title directly
(`data-testid="risks-row-title"`) and are governed by the slice-005 RLS
policy `current_tenant_matches(tenant_id)`. A tenant-A risk simply does
not appear in a tenant-B view — the cleanest possible "absent through
RLS" assertion. Seeded one canary risk in tenant A only.

### D4 — `super_admin: true` in the minted JWT (load-bearing, counter-intuitive)

The spec mints with `super_admin: true`. This looks wrong for an
isolation test, so the reasoning is recorded explicitly:

- `super_admin` is an **OPA-authz + token-exchange-allowlist** concept
  ONLY. It does NOT bypass PostgreSQL RLS.
- The jwtmw middleware sets `app.current_tenant` from the verified
  claim's CURRENT tenant **regardless of super_admin**
  (`internal/auth/jwtmw/middleware.go`, P0-190-3, ~line 184). The
  slice-005 `risks` RLS policy filters on that GUC. So the
  cross-tenant-leak assertion is exactly as strong with super_admin=true.
- WHY we need it: the synthetic test user has no `user_roles` rows. The
  authz input bridge (`internal/authz/input.go` `derivedRolesFor`) maps a
  non-super-admin synthetic credential whose `OwnerRoles` are non-empty
  to `control_owner` — which CANNOT read `/v1/risks`. So a
  `super_admin:false` JWT 403s at the OPA layer BEFORE RLS runs, masking
  the very thing under test.
- `super_admin:true` → `IsAdmin=true` → `RoleAdmin` → clean read path at
  authz, leaving **RLS as the sole gate on tenant visibility**. That is
  the whole point of the test.

Empirically verified end-to-end against a live stack (atlas + Postgres):

| Step                                                                 | Result                                      |
| -------------------------------------------------------------------- | ------------------------------------------- |
| mint multi-tenant JWT (A current, [A,B] available, super_admin=true) | 745-char token                              |
| `GET /v1/me/tenants`                                                 | both tenants returned with names, A current |
| `GET /v1/risks` (tenant A)                                           | `count:1`, canary present                   |
| RFC 8693 token-exchange A→B                                          | 861-char token, current_tenant=B            |
| `GET /v1/risks` (tenant B)                                           | `count:0`, canary ABSENT                    |

Also verified directly at the DB layer as the RLS-subject `atlas_app`
role: `SET app.current_tenant = A` → canary count 1; `= B` → count 0.

### D5 — Fixture seeds `tenants` identity rows (slice 144 table)

The slice-192 `GET /v1/me/tenants` handler enriches the switcher dropdown
via `SELECT id, name FROM tenants WHERE id = ANY($1)`. Without rows the
dropdown labels are empty and the `getByRole("option", { name: ... })`
selectors can't resolve. The fixture seeds two `tenants` rows (A + B)
with deterministic, visually-distinct UUIDs (`aaaa…` / `bbbb…`).

### D6 — Responsive duplicate-title disambiguation

The list shell renders each row title twice (desktop table
`list-cell-title` + `< md` card stack `list-card-cell-title`, slice 281
`mobileMode="cards"`). Both are in the DOM; a bare
`getByTestId("risks-row-title")` filter hits strict-mode (2 matches).
Scoped the canary locator to the desktop `list-cell-title` wrapper so the
presence assertion is unambiguous and the absence assertion counts the
exact same single surface. Both assertions auto-wait (no fixed sleeps).

### D7 — Does NOT replace the slice-351 mocked spec

`web/e2e/tenant-switch.spec.ts` stays as the fast flow-level gate (switch
mechanics + single-tenant hide rule, no DB needed). This spec adds the
real-RLS depth. The two coexist; their seeded row sets are disjoint
(`7777…0001` dashboard risk vs `eeee…0001` canary risk) per the
web/e2e/README.md additive-fixtures rule.

## Acceptance criteria

- **AC-1** — `/v1/test/issue-jwt` mints a multi-tenant JWT when given
  `available_tenants[]`, still `ATLAS_TEST_MODE`-gated. **PASS** — Go
  unit tests (`TestIssueTestJWT_MultiTenant`,
  `TestIssueTestJWT_SingleTenantBackwardCompat`,
  `TestIssueTestJWT_CurrentTenantNotInAvailable_400`,
  `TestIssueTestJWT_AvailableTenantsMalformed_400`) + live curl
  verification.
- **AC-2** — `web/e2e/tenant-switch-rls.spec.ts` asserts the
  cross-tenant-leak negative against real Postgres RLS. **PASS** — spec
  green against a live atlas + Postgres stack (1 passed).
- **AC-3** — decisions log records the harness-extension judgment + the
  production-safety review. **PASS** — this document (D1, D2, D4).

## Local verification environment

- Postgres 16 (docker), all forward migrations applied, base seed +
  `fixtures/e2e/tenant-switch.sql` applied.
- atlas server built from this branch, `ATLAS_TEST_MODE=1`,
  `DATABASE_URL` (BYPASSRLS migrate role) + `DATABASE_URL_APP`
  (RLS-subject `atlas_app` role) wired.
- Next.js dev server pointed at the atlas server; Playwright run with
  `--workers=1`. Spec passed.

## Spillover

None filed — scope was self-contained.

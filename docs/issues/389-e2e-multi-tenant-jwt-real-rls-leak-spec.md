# 389 — Multi-tenant JWT harness + real-RLS cross-tenant-leak e2e spec

**Cluster:** Quality / e2e + Auth
**Estimate:** 1-2d
**Type:** JUDGMENT
**Status:** `ready`

## Narrative

Surfaced during slice 351, captured per continuous-batch policy.

Slice 351 (AC-2) authored `web/e2e/tenant-switch.spec.ts` covering the
multi-tenant switch flow + the single-tenant hide rule. But it is a
`route.fulfill`-mocked spec, because the docker-compose bring-up
**cannot provision a multi-tenant user**:

- `internal/api/testissuejwt.go` (the slice-201 `POST /v1/test/issue-jwt`
  endpoint that `web/e2e/global-setup.ts` mints the harness JWT from)
  hardcodes `AvailableTenants: []uuid.UUID{tenant}` — a single-tenant
  claim set.
- The tenant-switch flow uses the RFC 8693 token-exchange grant, which
  requires the target tenant to already be in the caller's
  `available_tenants[]`. With a single-tenant JWT there is no valid
  target.

So the highest-value assertion of the tenant-switch flow — that a
tenant-A row is genuinely **absent** from a tenant-B view through real
PostgreSQL Row-Level Security (constitutional invariant #6) — cannot run
against the bring-up today. The mocked spec proves the flow + the hide
rule; it cannot prove RLS isolation.

## What

1. **Harness (JUDGMENT call):** extend `POST /v1/test/issue-jwt` to
   accept an optional `available_tenants[]` (+ per-tenant `roles`) so the
   e2e harness can mint a genuine multi-tenant JWT. The judgment is how
   to do this WITHOUT widening the production attack surface — the
   endpoint is already `ATLAS_TEST_MODE`-gated (P0-201-2), so the
   extension stays behind the same gate, but the reviewer must confirm
   no parallel signing surface or weakened constraint is introduced
   (P0-201-4). Seed the second tenant's rows via a new
   `fixtures/e2e/tenant-switch.sql`.
2. **Spec:** author the real-RLS variant —
   `web/e2e/tenant-switch-rls.spec.ts` — that:
   - mints a JWT with tenants A + B,
   - seeds a known row in tenant A (e.g. a control or risk),
   - asserts it is visible in the tenant-A view,
   - switches to tenant B via the real token-exchange,
   - asserts the tenant-A row is NOT visible in the tenant-B view.

## Scope discipline

- DOES NOT replace the slice-351 mocked `tenant-switch.spec.ts` — that
  stays as the fast flow-level gate; this adds the real-RLS depth.
- DOES NOT weaken the production gating of `/v1/test/issue-jwt`.

## Acceptance criteria

- [ ] AC-1: `/v1/test/issue-jwt` mints a multi-tenant JWT when given
      `available_tenants[]`, still `ATLAS_TEST_MODE`-gated.
- [ ] AC-2: `web/e2e/tenant-switch-rls.spec.ts` asserts the
      cross-tenant-leak negative against real Postgres RLS.
- [ ] AC-3: decisions log records the harness-extension judgment + the
      production-safety review.

## Dependencies

- #187-192 — merged. OAuth AS + tenant-switch token-exchange.
- #201 — merged. The test-JWT mint endpoint extended here.
- #351 — the audit that authored the mocked variant + filed this.

## Cross-references

- Slice 351 coverage matrix
  (`docs/audits/351-e2e-critical-flow-coverage-matrix.md`) — flow #2.
- `internal/api/testissuejwt.go` — the single-tenant limitation.

# 115 — Extend `admin-bootstrap.sql` to FULL coverage + enable assertions in `admin-bootstrap.spec.ts`

**Cluster:** Infra
**Estimate:** 0.5d
**Type:** AFK
**Status:** `not-ready`

## Narrative

Surfaced during slice 082, captured as follow-up per continuous-batch policy (orchestrator-filed per Amendment 2).

Slice 082 shipped `fixtures/e2e/admin-bootstrap.sql` at **MINIMAL** coverage. Extends to FULL (tenant + admin user + ≥1 IdP connection + ≥1 invited member + ≥1 RBAC role assignment) and enables the deferred assertions.

**Gating condition:** ≥5 clean post-082 runs of `Frontend · Playwright e2e` AND slice 111 merged.

## Acceptance criteria

- [ ] AC-1: Extend `fixtures/e2e/admin-bootstrap.sql` from MINIMAL to FULL — add a freshly-bootstrapped tenant, an admin user, ≥1 IdP connection row, ≥1 invited (not-yet-activated) member, ≥1 RBAC role assignment.
- [ ] AC-2: Audit `web/e2e/admin-bootstrap.spec.ts` for `test.skip` / `test.fixme` / commented assertions. Enumerate in PR body.
- [ ] AC-3: Enable each assertion against the FULL seed.
- [ ] AC-4: All enabled assertions pass on 3 consecutive CI runs.
- [ ] AC-5: Decisions log at `docs/audit-log/115-082-spec-uncomment-admin-bootstrap-decisions.md`.

## Dependencies

- **082** — merged
- **111** — merged
- **5 clean post-082 runs** of `Frontend · Playwright e2e`

## Anti-criteria (P0)

- Does NOT touch other specs/fixtures
- Does NOT promote the e2e job to required-checks (slice 116)
- No real customer data; neutral test-\* tokens only
- RLS context must be exercised — admin user must be created under `current_tenant_id` from the seed harness

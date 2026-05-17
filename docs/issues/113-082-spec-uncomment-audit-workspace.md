# 113 — Extend `audit-workspace.sql` to FULL coverage + enable assertions in `audit-workspace.spec.ts`

**Cluster:** Infra
**Estimate:** 0.5d
**Type:** AFK
**Status:** `not-ready`

## Narrative

Surfaced during slice 082, captured as follow-up per continuous-batch policy (orchestrator-filed per Amendment 2).

Slice 082 shipped `fixtures/e2e/audit-workspace.sql` at **MINIMAL** coverage. Extends to FULL (active audit period + ≥2 sampled controls + ≥1 evidence-by-sample + ≥1 frozen evidence row) and enables the deferred assertions.

**Gating condition:** ≥5 clean post-082 runs of `Frontend · Playwright e2e` AND slice 111 merged.

## Acceptance criteria

- [ ] AC-1: Extend `fixtures/e2e/audit-workspace.sql` from MINIMAL to FULL — add an active `AuditPeriod` with `frozen_at IS NULL`, ≥2 sampled controls, ≥1 evidence row tied to a sample, ≥1 frozen evidence row to exercise audit-period freezing.
- [ ] AC-2: Audit `web/e2e/audit-workspace.spec.ts` for `test.skip` / `test.fixme` / commented assertions. Enumerate in PR body.
- [ ] AC-3: Enable each assertion against the FULL seed.
- [ ] AC-4: All enabled assertions pass on 3 consecutive CI runs.
- [ ] AC-5: Decisions log at `docs/audit-log/113-082-spec-uncomment-audit-workspace-decisions.md`.

## Dependencies

- **082** — merged
- **111** — merged
- **5 clean post-082 runs** of `Frontend · Playwright e2e`

## Anti-criteria (P0)

- Does NOT touch other specs/fixtures
- Does NOT promote the e2e job to required-checks (slice 116)
- No real customer data; neutral test-* tokens only
- Honors constitutional invariant #10 (audit-period freezing) — frozen rows must use `observed_at ≤ frozen_at`

# 114 — Extend `risk-hierarchy.sql` to FULL coverage + enable assertions in `risk-hierarchy.spec.ts`

**Cluster:** Infra
**Estimate:** 0.5d
**Type:** AFK
**Status:** `not-ready`

## Narrative

Surfaced during slice 082, captured as follow-up per continuous-batch policy (orchestrator-filed per Amendment 2).

Slice 082 shipped `fixtures/e2e/risk-hierarchy.sql` at **MINIMAL** coverage. Extends to FULL (parent risk + ≥2 child risks at different levels + linked controls + treatments) and enables the deferred assertions.

**Gating condition:** ≥5 clean post-082 runs of `Frontend · Playwright e2e` AND slice 111 merged.

## Acceptance criteria

- [ ] AC-1: Extend `fixtures/e2e/risk-hierarchy.sql` from MINIMAL to FULL — add a parent risk, ≥2 child risks at different `parent_risk_id` depths, ≥1 control link per leaf risk, ≥1 treatment row.
- [ ] AC-2: Audit `web/e2e/risk-hierarchy.spec.ts` for `test.skip` / `test.fixme` / commented assertions. Enumerate in PR body.
- [ ] AC-3: Enable each assertion against the FULL seed.
- [ ] AC-4: All enabled assertions pass on 3 consecutive CI runs.
- [ ] AC-5: Decisions log at `docs/audit-log/114-082-spec-uncomment-risk-hierarchy-decisions.md`.

## Dependencies

- **082** — merged
- **111** — merged
- **5 clean post-082 runs** of `Frontend · Playwright e2e`

## Anti-criteria (P0)

- Does NOT touch other specs/fixtures
- Does NOT promote the e2e job to required-checks (slice 116)
- No real customer data; neutral test-\* tokens only

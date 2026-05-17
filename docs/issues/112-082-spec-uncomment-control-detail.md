# 112 — Extend `control-detail.sql` to FULL coverage + enable assertions in `control-detail.spec.ts`

**Cluster:** Infra
**Estimate:** 0.5d
**Type:** AFK
**Status:** `not-ready`

## Narrative

Surfaced during slice 082, captured as follow-up per continuous-batch policy (orchestrator-filed per Amendment 2).

Slice 082 shipped the `seedFromFixture()` harness with `fixtures/e2e/control-detail.sql` at **STUB** coverage — the spec passes today against the slice-082 `00-seed.sql`'s seeded control, but multi-anchor + out-of-scope assertions are deferred. This slice extends the fixture to FULL coverage and enables those deferred assertions.

**Gating condition:** ≥5 clean post-082-merge runs of `Frontend · Playwright e2e` AND slice 111 merged (sibling staged rollout). Maintainer flips to `ready`.

## Acceptance criteria

- [ ] AC-1: Extend `fixtures/e2e/control-detail.sql` from STUB to FULL — add a multi-anchor control + at least one out-of-scope evidence row + at least one drift snapshot for the seeded control.
- [ ] AC-2: Audit `web/e2e/control-detail.spec.ts` for any `test.skip`, `test.fixme`, or commented-out assertions. Enumerate in PR body.
- [ ] AC-3: Enable each assertion against the FULL seed. Verify locally via `seedFromFixture("control-detail")`.
- [ ] AC-4: All enabled assertions pass on 3 consecutive CI runs.
- [ ] AC-5: Decisions log at `docs/audit-log/112-082-spec-uncomment-control-detail-decisions.md`.

## Dependencies

- **082** — merged
- **111** — merged (proves the un-skip pattern with FULL seed)
- **5 clean post-082 runs** of `Frontend · Playwright e2e`

## Anti-criteria (P0)

- Does NOT touch other specs/fixtures (one slice per spec)
- Does NOT promote the e2e job to required-checks (slice 116)
- No real customer data; neutral test-* tokens only

## Notes

Sibling slices: 111 (dashboard, FULL already), 113 (audit-workspace), 114 (risk-hierarchy), 115 (admin-bootstrap). All five must land before slice 116 promotes to required-checks.

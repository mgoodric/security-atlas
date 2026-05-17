# 111 — Enable full assertions in `dashboard.spec.ts` (post-082 stabilization)

**Cluster:** Infra
**Estimate:** 0.25d
**Type:** AFK
**Status:** `not-ready`

## Narrative

Surfaced during slice 082, captured as follow-up per continuous-batch policy (orchestrator-filed per Amendment 2 — engineer surfaced this in decisions log decision 2 but did not file the slice).

Slice 082 shipped the `seedFromFixture()` harness with `fixtures/e2e/dashboard.sql` at FULL coverage (risk + 2 drift snapshots + 2 freshness rows + 1 exception expiring +14d). The `dashboard.spec.ts` invokes the harness in `test.beforeAll()` and passes today, BUT some richer assertions in the spec may still be commented-out / `test.skip`'d from the slice-079 quarantine era.

This slice enables those deeper assertions one-by-one, confirms each passes against the FULL seed coverage, and removes any remaining `.skip` annotations in `web/e2e/dashboard.spec.ts`.

**Gating condition:** ≥5 clean post-082-merge runs of `Frontend · Playwright e2e` on consecutive PRs. The condition ensures the harness is stable enough that adding more assertions won't surface harness-bugs vs. real regressions. Maintainer flips to `ready` once the run-history shows the harness is reliable.

## Acceptance criteria

- [ ] AC-1: Audit `web/e2e/dashboard.spec.ts` for any `test.skip`, `test.fixme`, or commented-out assertions from the slice-079 quarantine era. Enumerate them in the slice PR body.
- [ ] AC-2: Enable each assertion against the FULL `fixtures/e2e/dashboard.sql` coverage. Verify locally against the slice-082 `seedFromFixture("dashboard")` path.
- [ ] AC-3: If any assertion requires additional seed data, extend `fixtures/e2e/dashboard.sql` in-place (do NOT split into a sibling fixture). Document the additions in the slice decisions log.
- [ ] AC-4: All enabled assertions pass on at least 3 consecutive CI runs in the PR (re-run the workflow manually to confirm).
- [ ] AC-5: Decisions log at `docs/audit-log/111-082-spec-uncomment-dashboard-decisions.md` records the per-assertion enable trail + any new seed additions.

## Constitutional invariants honored

- **No real customer data in fixtures** (slice 082 P0-A1 carries through)
- **Neutral test-\* fixture tokens only** (slice 082 P0-A3)
- **Pre-commit + DCO sign-off** per CLAUDE.md

## Canvas references

- `Plans/mockups/dashboard.html` (the design reference for dashboard assertions)
- `web/e2e/dashboard.spec.ts` (the spec being un-skipped)
- `fixtures/e2e/dashboard.sql` (FULL coverage already shipped)
- Slice 082 decisions log (decision 2: per-spec staged un-quarantine plan)

## Dependencies

- **082** — merged (seed harness + FULL dashboard fixture)
- **5 clean post-082 runs** of `Frontend · Playwright e2e` — maintainer-verified gate

## Anti-criteria (P0)

- Does NOT touch other specs (one slice per spec for clean staged rollout)
- Does NOT touch the harness or other fixtures
- Does NOT promote the e2e job to required-checks (that's slice 116)

## Skill mix

- Playwright spec authoring (un-skip + assert)
- Reading slice-082 decisions log + per-spec seed coverage
- CI re-run discipline (3 consecutive clean runs)

## Notes

- Sibling slices for the other 4 specs: 112 (control-detail), 113 (audit-workspace), 114 (risk-hierarchy), 115 (admin-bootstrap).
- After all 5 spec slices land (111-115) + 5 clean post-merge runs each, slice 116 promotes `Frontend · Playwright e2e` to `.github/branch-protection.json` required-checks.

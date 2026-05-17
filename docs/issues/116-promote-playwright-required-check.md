# 116 — Promote `Frontend · Playwright e2e` to required-checks in branch protection

**Cluster:** Infra
**Estimate:** 0.25d
**Type:** AFK
**Status:** `not-ready`

## Narrative

Surfaced during slice 082, captured as follow-up per continuous-batch policy (orchestrator-filed per Amendment 2).

Slice 082's AC-4 removed `continue-on-error: true` from `Frontend · Playwright e2e`. AC-5 explicitly DEFERRED the branch-protection promotion until the harness was proven stable on real PR traffic — the staged rollout is:

1. **082** (merged 2026-05-16) — seed harness + remove `continue-on-error`. Job now FAILS visibly on red. Not yet a blocking required-check, so the stub-twin still satisfies branch-protection.
2. **111-115** (per-spec un-skip / FULL-seed work) — gradually expand assertion coverage to FULL across all 5 specs.
3. **116** (this slice) — flip `Frontend · Playwright e2e` to a true required-check in `.github/branch-protection.json` once all 5 specs are stable. Remove the stub-twin job (it was only there to satisfy required-check evaluation while the real job was quarantined).

## Acceptance criteria

- [ ] AC-1: All of 111, 112, 113, 114, 115 are MERGED with their full assertion sets enabled. Verify via `gh pr list --state merged` + `_STATUS.md` row inspection.
- [ ] AC-2: ≥5 consecutive PRs (any PRs, not just spec-related) show `Frontend · Playwright e2e` as PASS in CI history. Maintainer-verified via run-history inspection.
- [ ] AC-3: `.github/branch-protection.json` updated: `Frontend · Playwright e2e` added to required status checks; the stub-twin `Frontend · Playwright e2e (stub)` job removed from both `.github/workflows/ci.yml` AND from required status checks (it was only there to satisfy required-check naming during quarantine).
- [ ] AC-4: `web/e2e/README.md` updated — remove the quarantine reference, document the new required-check status, document the seed-harness contract for spec authors.
- [ ] AC-5: Decisions log at `docs/audit-log/116-promote-playwright-required-check-decisions.md` — chosen pre-flip run count, any flakiness pattern observed during stabilization, the maintainer-verified pass rate.

## Constitutional invariants honored

- **Testing discipline (CLAUDE.md):** "Frontend Playwright — CI fails on spec failure" becomes truly blocking, closing the slice-069 → 079 → 082 quarantine arc.

## Canvas references

- `Plans/canvas/09-tech-stack.md` (testing discipline)
- `.github/branch-protection.json` (the surface being modified)
- `.github/workflows/ci.yml` (stub-twin removal target)
- Slice 069, 079, 082 — the full arc from "tests exist" to "tests gate merges"

## Dependencies

- **082** — merged
- **111, 112, 113, 114, 115** — all merged
- **≥5 clean PR runs** post-115 merge

## Anti-criteria (P0)

- Does NOT flip until ALL of 111-115 are merged. Skipping spec-coverage slices breaks the "tests gate merges, AND they actually test things" property.
- Does NOT keep the stub-twin "just in case" — leave one source of truth.
- Does NOT modify spec assertions in this slice — pure branch-protection + workflow edit only.
- Does NOT flip if any of the 5 consecutive runs flaked — investigate root cause first; flaky required-check is worse than no required-check.

## Skill mix

- `.github/branch-protection.json` edit pattern (slice 069 has the template)
- Stub-twin removal pattern (slice 079 added it; reverse mechanically)
- CI run-history inspection via `gh run list --workflow=ci.yml --status=success`

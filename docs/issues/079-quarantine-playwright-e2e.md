# 079 — Quarantine `Frontend · Playwright e2e` until the seed-data harness lands

**Cluster:** Infra
**Estimate:** 0.25d
**Type:** AFK

## Narrative

Surfaced during the 2026-05-15 post-batch-29 CI-failure investigation. The `Frontend · Playwright e2e` job — wired by slice 069 as part of the verification suite — has been failing on every PR since it landed. Today's burndown saw **52 failures out of 62 total CI failures (84%)**: ~156 minutes of CI compute wasted on a single known-failing check that doesn't gate the merge queue.

**Root cause is documented and accepted upstream:** slice 069's AC-5 PARTIAL says first-run Playwright e2e green pending a seed-data harness. The five un-shimmed specs (`web/e2e/*.spec.ts`) reference fixtures that exist on disk but aren't applied to the platform under test at job startup. So every spec fails with "expected element not visible" or equivalent — predictable, not a flake. The job is not in `.github/branch-protection.json`'s required-checks list (per slice 069 commit shipping `branch-protection.json` with 10→12 added but `gh api` showing the live protection still at 10), so merges aren't blocked — only noise.

The seed-data harness IS the right long-term answer. This slice is the **interim quarantine** until that harness lands. Three options the engineer's grill picks from:

- **Path A — `continue-on-error: true`.** The job still runs, captures any _new_ failures (e.g., a spec is broken for a different reason), but its red conclusion doesn't make the PR look red. Maximum visibility, zero false alarm.
- **Path B — path-filter to `web/e2e/**` changes only.\*\* Job runs only when someone modifies the specs — catches spec-author errors immediately, idle on every other PR. Roughly 0 runs/day in the current cadence.
- **Path C — remove the job from `ci.yml` entirely until the seed-harness slice lands.** Cleanest but loses the safety net for spec-author errors.

Default recommendation in the slice doc: **Path A**. Engineer's grill confirms or switches with rationale in the decisions log.

**What this slice deliberately is NOT:**

- It is NOT the seed-data harness itself. That's a separate ~2-3d slice (079-follow-on, filed by this slice if the engineer takes Path A or B).
- It is NOT changing the spec content or the runner config. The runner from slice 069 stays exactly as-is.
- It is NOT promoting Playwright e2e to a required check (would be lying about what's gated; the job has never passed).

## Acceptance criteria

- [ ] AC-1: Engineer's grill picks Path A / B / C with rationale recorded in `docs/audit-log/079-quarantine-playwright-e2e-decisions.md`. Default A unless something material surfaces.
- [ ] AC-2: `.github/workflows/ci.yml` `Frontend · Playwright e2e` job updated per the chosen path. Inline comment cites slice 079 + the AC-5 PARTIAL link from slice 069's PR description (gh#132).
- [ ] AC-3: A follow-on slice file `docs/issues/<NNN>-playwright-seed-data-harness.md` exists in this PR, status `not-ready` (no clean dep listed — when the maintainer or a future engineer decides to staff it, they flip to `ready`). The follow-on slice's body specifies: (a) seeded test user creation, (b) seeded tenant data per spec's preconditions, (c) `web/e2e/fixtures.ts` extension, (d) flip Path-A `continue-on-error` back to fail-on-red, (e) re-evaluate whether to promote to required-checks.
- [ ] AC-4: `_STATUS.md` updated. Counts reconciled.
- [ ] AC-5: Verify the change: open a draft PR with the quarantine in place, observe one full CI cycle, confirm the `Frontend · Playwright e2e` job either (Path A) reports as a passing-with-warnings annotation, (Path B) is skipped on a docs-only diff, or (Path C) doesn't appear. No false-positive — the merge queue's UI/CLI should not show a red X for Playwright e2e specifically.
- [ ] AC-6: CONTRIBUTING.md gains one paragraph under "Test infrastructure" pointing at the quarantine status + linking the follow-on slice so contributors don't waste time debugging a known-failing job.

## Constitutional invariants honored

- **Working norms — Surgical fixes** (CLAUDE.md): smallest viable change to a CI workflow line; no spec rewrites, no runner reconfig, no test deletions.
- **AI-assist boundary**: zero AI-generated content in this slice. Workflow YAML edit + decisions log + follow-on slice spec.

## Canvas references

- _(none — operational CI hygiene; canvas doesn't speak to CI noise budgets)_

## Dependencies

- **069** (verification suite — Playwright + vitest + Go coverage gate, merged) — the slice that wired the failing job

## Anti-criteria (P0 — block merge)

- **P0-A1**: Does NOT delete the five spec files in `web/e2e/*.spec.ts`. They're the contract for the seed-harness follow-on; deleting them loses the assertion shape.
- **P0-A2**: Does NOT delete `web/playwright.config.ts`, `web/e2e/fixtures.ts`, or the npm `devDependency` on `@playwright/test`. Same reason — keep the runner intact for the harness slice.
- **P0-A3**: Does NOT add `Frontend · Playwright e2e` to `.github/branch-protection.json` required-checks. The job has never passed; promoting it is dishonest.
- **P0-A4**: Does NOT silently turn the job off without recording WHY in `ci.yml`'s inline comment. Future contributors need to find the trail.
- **P0-A5**: Does NOT add a recurring CI-noise audit (this slice's investigation is a one-shot; if noise creeps back the maintainer files another post-mortem).

## Skill mix (3–5)

- GitHub Actions YAML (the `continue-on-error` / `if:` / path-filter conditional syntax)
- `simplify` (the workflow comment + the CONTRIBUTING paragraph stay tight)
- `engineering-advanced-skills:runbook-generator` (the follow-on seed-harness slice is a runbook — clear preconditions + steps + verification)

## Notes for the implementing agent

- **The cheapest path is Path A.** A single `continue-on-error: true` line on the job. The job still runs (so a _new_ failure mode surfaces) but its red conclusion doesn't poison the PR's status checks. Estimated edit: 2 lines (the field + a comment).
- **The follow-on seed-harness slice doesn't need to be deeply specified.** Just enough that the maintainer can pick it up later: who needs to be seeded, what preconditions each spec assumes, where the fixture data lives. ~30-line slice doc, status `not-ready`, deps "Playwright spec ergonomics review."
- This slice should NOT be batched with anything else (especially not with another `.github/workflows/ci.yml`-touching slice). Solo run. ~0.25d total wall-clock.

# 119 — Fix recurring `port 3000 already in use` flake in `Frontend · Playwright e2e` CI job

**Cluster:** Infra (CI hardening)
**Estimate:** 0.5d (diagnosis-heavy; actual fix is likely 1-3 lines)
**Type:** AFK
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)

## Narrative

The `Frontend · Playwright e2e` CI job has been failing on a recurring infra race throughout the 2026-05-15 → 2026-05-16 development sessions. The exact error in the run log:

```
Error: http://localhost:3000 is already used, make sure that nothing is
running on the port/url or set reuseExistingServer:true in config.webServer.
```

Observed pattern:

- Hits **every** Dependabot PR (151, 152, 153, 154, 156, 158, 159, also pre-existing on many feature-slice PRs)
- Hits feature-slice PRs too (082's PR #253 carried this exact failure; the maintainer merged through it because the stub-twin still satisfied required-checks)
- The job IS in `.github/branch-protection.json` required_status_checks. This means: per `Plans/prompts/08-dependabot-pr-review.md` STEP 7 (auto-merge requires no failing required-check), the flake **blocks the LOW-risk auto-merge path on every Dependabot PR**, defeating the auto-merge default we just shipped
- GitHub-hosted runners are ephemeral per job, so a leftover process from an unrelated job is NOT the cause — the contention is intra-job

Likely root causes to investigate (in rough order of probability):

1. **Playwright `webServer` config has `reuseExistingServer: true` set incorrectly** in `web/playwright.config.ts` — that flag tells Playwright "if a server already responds on the URL, use it" — which is the right call locally but in CI it can mask a stale process or trip on the dev server's HMR socket
2. **The Playwright webServer command starts `next dev`, but the CI step also starts `next dev` separately** — duplicate spawn racing for :3000
3. **Slice 082's `seedFromFixture()` harness spawns a Next.js process that doesn't get torn down** between test files
4. **Two specs in series each spawn their own server** because `test.beforeAll` is per-file in Playwright (not per-project) and webServer respawn semantics interact badly with that
5. **Port 3000 is held by a leftover process from a prior test attempt** within the same job (Playwright workers fork; if a worker crashes mid-bind, the socket may not release before the next retry)

The fix is mechanical once the cause is identified, but the diagnosis requires:

- Reading `web/playwright.config.ts` for the current `webServer` shape
- Reading `.github/workflows/ci.yml` `frontend-playwright` job for the step ordering
- Reproducing locally with `CI=true npm run test:e2e` from `web/` to see whether the race fires off-CI
- Inspecting a CI log around the failure timestamp to see what's holding :3000 (the runner logs `lsof -i :3000` if anyone added a debug step; if not, this slice should ADD one as part of the diagnostic pass)

## Acceptance criteria

- [ ] AC-1: Diagnose the actual root cause. The slice PR body MUST identify which of the candidate causes (or a different one) is responsible, with evidence (CI log excerpts, local repro instructions, or `lsof`-style output captured from a debug CI run).
- [ ] AC-2: Apply the minimal fix. Avoid scope expansion — if the fix is a single config flag, that's the entire diff. Don't restructure the webServer config, don't rewrite specs, don't migrate to a different Playwright version.
- [ ] AC-3: Validate by re-running the workflow on this PR ≥3 consecutive times — all 3 runs MUST show `Frontend · Playwright e2e` PASS. Re-trigger via `gh run rerun --workflow ci.yml`.
- [ ] AC-4: Validate by re-triggering one of the currently-flaking Dependabot PRs (#151, #152, #153, #154, #156, #158, #159 — any one that's `mergeStateStatus: UNSTABLE` due to Playwright failure) by posting `@dependabot rebase` post-merge of this fix. Confirm the next run on that PR shows Playwright PASS. Document which PR was used as the canary in the decisions log.
- [ ] AC-5: Decisions log at `docs/audit-log/119-playwright-port-3000-ci-race-fix-decisions.md`. Required entries:
  - The diagnosis: which candidate cause was correct + the evidence trail that established it
  - Why the chosen fix is minimal (vs alternatives considered)
  - Any debug-instrumentation added to CI as part of the diagnosis (a temporary `lsof -i :3000 || true` step is fine; if it stays in `ci.yml` post-fix, justify it)

## Constitutional invariants honored

- **CLAUDE.md "Surgical fixes only — never add or remove components as a fix"** — this slice is exactly the kind of work that CLAUDE.md's "Surgical fixes" rule was written for. The flake has a specific root cause; the fix should be specific. Do NOT rip out the Playwright suite, do NOT migrate to a different test runner, do NOT quarantine the job again (slice 079 → 082 already wound that back).
- **Testing discipline (CLAUDE.md):** `Frontend · Playwright e2e` is a required-check per `.github/branch-protection.json`. Fixing the flake restores the gate's real signal, which is what slice 082's hard work was building toward.

## Canvas references

- `web/playwright.config.ts` (the webServer config to inspect)
- `.github/workflows/ci.yml` (the `frontend-playwright` job)
- `web/e2e/seed.ts` (slice 082's harness — investigate if it spawns servers)
- `.github/branch-protection.json` (the required_status_checks list confirming this is a real gate)
- Slice 082's decisions log at `docs/audit-log/082-playwright-seed-data-harness-decisions.md` (decision 1 documents the port-3000 issue as pre-existing infrastructure flake, not seed-harness-caused)
- `Plans/prompts/08-dependabot-pr-review.md` STEP 7 (the auto-merge rule that this flake blocks)

## Dependencies

- None — pure infra slice; the diagnosis can start immediately against the current `main`.

## Anti-criteria (P0 — block merge)

- **P0-A1**: Does NOT re-quarantine the job by re-adding `continue-on-error: true`. Slice 082 explicitly removed that. The fix must address the cause, not hide the symptom.
- **P0-A2**: Does NOT remove `Frontend · Playwright e2e` from `.github/branch-protection.json` required-checks. That's the slice 116 promotion gate; removing it would reverse the project's testing-discipline arc.
- **P0-A3**: Does NOT migrate to a different Playwright version, a different test runner, or a different webServer setup. Scope is "fix the race, not redesign the testing stack."
- **P0-A4**: Does NOT add the proposed diagnostic-instrumentation CI step as permanent infrastructure unless the slice PR body justifies why it's needed beyond diagnosis. Temporary debug steps land + get removed in the same slice.
- **P0-A5**: Does NOT close any of the currently-failing Dependabot PRs (151-159) as part of this slice. Those need independent dep-review analysis; this slice only fixes the CI prerequisite that's blocking them.

## Skill mix

- Playwright webServer config semantics (`reuseExistingServer`, `timeout`, `command`)
- GitHub Actions job-step ordering + ephemeral runner mental model
- TCP socket lifecycle (TIME_WAIT, SO_REUSEADDR) — relevant if the race is a port-release timing issue
- `lsof -i :PORT` / `ss -tlnp` for diagnostic CI step authoring
- Slice-079 → 082 historical context (why the job was quarantined and how it got un-quarantined)

## Notes for the implementing agent

- **Start with the simplest hypothesis** — read `web/playwright.config.ts` first. If `reuseExistingServer: true` is set, try setting it to `!process.env.CI` (true locally, false in CI) and see if that alone fixes it. That's the 1-line fix path.
- **If the simple fix doesn't work**, ADD a diagnostic step to `.github/workflows/ci.yml` `frontend-playwright` job RIGHT BEFORE `npm run test:e2e`:
  ```yaml
  - name: Debug — what's on port 3000
    run: |
      lsof -i :3000 || echo "(nothing on :3000)"
      ss -tlnp 2>/dev/null | grep ':3000' || echo "(no listeners on :3000)"
      ps aux | grep -E "next|node" | grep -v grep || true
  ```
  Push, re-run, read the output. Remove the debug step in the final commit (AC-4).
- **Use one of the existing failing PRs as the canary** (AC-4) rather than waiting for new flakes. PRs 154, 156, 158, 159 are all currently `UNSTABLE` due to this exact failure — re-running CI on any of them post-fix is a free validation signal.
- **The fix LIVES IN `web/`, NOT in the workflow files** (probably). Workflow-level fixes would be a sign of a deeper issue (e.g., job step duplicating server startup). Prefer config-level fixes.
- **Coordinate with slice 116 deferred promotion**: slice 116 was deferred because the Playwright job wasn't stable enough to be a real required-check. This slice REMOVES that excuse. After this slice merges + 5 clean post-merge runs, slice 116's gating condition is satisfied and a human can flip it to `ready`.

## Out-of-scope (would be separate slices)

- Slice 116 (Playwright job promotion to required-checks) — already filed; gated on this slice landing
- Auditing whether OTHER CI jobs have similar port-contention races
- Migrating the docs-site `mkdocs serve` to a different port to avoid future contention (no evidence of contention; speculative)
- Adding integration-test parallelism via Playwright projects/workers (orthogonal improvement)

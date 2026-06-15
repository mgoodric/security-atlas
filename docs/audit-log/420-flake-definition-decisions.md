# Slice 420 — flake-definition decisions log

> JUDGMENT slice. Records the build-time calls Claude made, per the
> project's JUDGMENT-slice workflow (`Plans/prompts/04-per-slice-template.md`).
> Subject: broaden the flake-budget counter's "flake" definition to catch
> rerun-cleared timing flakes on the integration surface (the slice-352
> 90-day "0 flakes" under-count that the slice-346 / PR-788 scheduler-flake
> incident contradicted).

## D1 — Scope of the broadening: integration surface ONLY

Per the slice spec (AC-6, P0-4) the broadening is keyed on the EXACT
integration check name `Go · integration (Postgres RLS)` and nothing
else. The unit / vitest / Playwright surface definitions are left exactly
as slice 352 set them (same-SHA rerun-cleared only). Rationale:

- The dominant real flake (the scheduler goroutine race,
  `internal/metrics/scheduler/integration_test.go`
  `TestRun_FiresInlineSweepAndExitsOnCancel`) lives on the integration
  surface.
- Q-16 ("no `-retry`, investigate every flake") makes a true count most
  valuable there.
- Playwright already has `retries: 1` semantics and a different flake
  profile; broadening it would change its meaning.

Implemented as a named constant `INTEGRATION_JOB_NAME` plus a guard
`is_integration_surface`, so the broadening is unambiguously scoped and
greppable (P0-4).

## D2 — THE load-bearing JUDGMENT: flake vs fix-forward (P0-3 / AC-4)

The deliberately-deferred weaker signal (`flake-budget.md` lines 56-59) is
"fail on commit A, pass on commit A+1". The danger (I-2, over-count): a
genuine fix-forward — where A+1's code actually FIXED the failing test —
looks identical at the run-conclusion level to a flake. A naive "any
A->A+1 success = flake" rule fills the dashboard with false positives and
fires the investigation trigger on real fixes.

**Rule chosen (mechanically defensible):** for the integration surface,
when the integration job is RED on push A and GREEN on the immediate next
push A+1 to the same branch, fetch A+1's changed-file set (GitHub compare
API, `repos/{repo}/compare/{A}...{A+1}` → `.files[].filename`) and:

- If the changed-file set **intersects the integration test surface**
  (regex `INTEGRATION_CODE_PATH_RE` = `internal/**/*.go`, `migrations/`,
  `internal/db/`, `cmd/**/*.go`) → classify **fix-forward** → NOT counted.
  Rationale: A+1 plausibly fixed the integration test, because it changed
  code that the integration tier actually exercises.
- Else (A+1 touched no integration-surface code — a docs-only push, a
  CHANGELOG bump, an unrelated connector, or an empty diff) → classify
  **flake** → counted. The integration job went red then green with no
  relevant code change: that is the scheduler-flake shape.

Why this rule is defensible (not perfect, but mechanical and conservative
toward NOT over-counting):

- It biases toward fix-forward (toward NOT counting). Any plausible code
  fix to the tested surface suppresses the flake count. This deliberately
  errs on the side the threat model says is dangerous (over-count is the
  cardinal sin; a missed flake is the v1 status quo we are improving, not
  worsening).
- It is computed from data GitHub gives us deterministically (compare API
  changed-files), so two runs over the same window agree.
- It is implemented as a pure function `classify_integration_transition`
  in the sourceable prefix of the script, exercised directly by the test
  harness (AC-4 + AC-5) — no live API needed to prove the JUDGMENT.

**Known limitation (accepted for this slice):** the rule cannot tell a
fix-forward that happens to live OUTSIDE the integration code paths (e.g.
a CI-yaml or env change that fixed a real integration failure) from a
true flake. Such a case would be counted as a flake (a false positive).
This is judged acceptable because (a) it is rare, (b) it still results in
a human investigation — the budget is a signal, not a gate (P0-1), and an
investigation that concludes "this was actually a config fix-forward" is a
cheap, correct outcome, not a merge block. Refinement (e.g. parsing the
failing test name from logs and checking whether A+1 touched that exact
package) is listed in the revisit section.

## D3 — Where the broadening lives in the script (Pass C)

Per the implementation note, the same-SHA detection (Pass B) was left
intact; the broadening is a new **Pass C** after the per-SHA loop:

1. During Pass A, every SHA's integration **candidate** row is appended
   to a timeline (run-level final conclusion as a cheap candidate proxy +
   run id / attempt for later confirmation).
2. Pass C sorts the timeline by branch then run-start time and walks
   adjacent pushes per branch.
3. On a red->green adjacency it makes BOUNDED API calls only: a targeted
   integration-JOB fetch on the RED run to **confirm the integration job
   itself failed** (P0-4 — a lint/sqlc red run is ignored, never
   mis-attributed), a confirm that the GREEN run's integration job truly
   succeeded (not a path-filtered skip), and the compare API for the
   changed-file set.
4. `classify_integration_transition` decides flake vs fix-forward.

API cost stays proportional to red->green adjacencies (rare), not to
total SHAs — the chunked pagination slice 352 already does is unchanged.

## D4 — AC-3: re-baseline disposition (live vs synthetic)

- **Live 7-day run (executed 2026-06-15, `mgoodric/security-atlas`):** all
  four surfaces 0 flakes / 697 attempts, status green. Importantly, Pass C
  added **zero** spurious integration flakes on the live window — the
  broadening did not over-count on real recent history (an over-count
  smoke test in itself).
- **90-day window:** the canonical scheduler-flake incident (slice 346 /
  PR #788) dates to ~2026-05-28, which IS inside a 90-day window
  (2026-03-17 → 2026-06-15). However, that incident was a same-SHA
  rerun-cleared flake on a docs-only PR that was subsequently squash-
  merged and the branch deleted; the failing-attempt history on a closed,
  deleted PR branch is not reliably reconstructable from the workflow_runs
  walk, and a full 90-day live walk is exactly the moving-window
  dependency the slice spec warns against (AC-3 explicitly permits the
  synthetic fallback rather than depending on a moving live window).
- **Disposition (per AC-3 fallback):** the durable proof of the
  under-count fix is the **synthetic GitHub-API fixture in
  `scripts/flake-counter_test.sh`** — test-7 (AC-5) asserts the scheduler-
  flake shape (integration red on A, green on A+1, docs-only/empty diff)
  IS counted; test-8 (AC-4) asserts a genuine fix-forward (A+1 touched
  the integration test package) is NOT counted. This proof is
  window-independent and runs in CI on every change to the script.

## D5 — Report-only preserved (P0-1, P0-2, P0-5, P0-6)

- The counter still only counts and reports. Pass C increments a counter
  file and appends an evidence row; it never `t.Skip`s, never quarantines,
  never edits product code, never adds `-retry`. AC-8 holds.
- The budget table thresholds in `docs/flake-budget.md` were NOT touched
  (P0-6) — only the detection of what counts. The merge-block bar is
  unchanged (P0-1): every flake still blocks its merge; the budget remains
  a signal.
- The scheduler flake itself was NOT fixed here (P0-7) — this slice makes
  the dashboard SEE it; the fix is separate.

## Detection-tier classification

- `detection_tier_actual`: none (no bug surfaced during the slice; the
  live 7-day run and the synthetic fixtures both behaved as designed).
- `detection_tier_target`: integration — the class of bug this slice
  improves observability for (rerun-cleared integration timing flakes) is
  caught at the integration tier; this slice makes the aggregate signal
  honest, it does not change where the underlying flake is caught.

## Revisit list

1. **Broaden the other three surfaces?** Deliberately NOT done (AC-6).
   Revisit after a quarter of integration-surface A->A+1 data: if the
   unit/vitest surfaces show the same under-count symptom, file a
   follow-up to extend Pass C's gating to them (vitest has no retry; unit
   is hard-fail — the A->A+1 shape is plausible there too). Playwright is
   excluded by design (`retries: 1`).
2. **Sharpen the fix-forward rule** (D2 known limitation): parse the
   failing test's package from the integration job log and gate the
   fix-forward decision on whether A+1 touched THAT package specifically,
   rather than the whole integration surface. Higher precision, higher
   cost (log parsing). Only worth it if D2's coarse rule produces
   observed false positives.
3. **90-day live attribution of the scheduler incident**: if the
   maintainer wants the historical incident to appear on the dashboard,
   a one-off `workflow_dispatch` 90-day rebuild can be run; the synthetic
   fixture already proves the detection works.

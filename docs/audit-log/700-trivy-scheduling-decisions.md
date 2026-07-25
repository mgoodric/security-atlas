# Slice 700 — Trivy image-scan scheduling — decisions log

JUDGMENT slice. Slice 700 asked one question: does the `trivy-image` job move off the
PR hot-path to a nightly `schedule:` run on `main`, or does it stay? Slice 700 and slice
694 are stated ALTERNATIVES — "cache it, or move it. Pick one." Slice 694 (buildx +
`type=gha` layer cache) merged 2026-06-15 at `5b9a959e` (gh#1421). This log records the
measured post-694 evidence and the resulting call.

**Outcome: WONTFIX.** The `trivy-image` job stays on the PR path exactly as it is. No
workflow file is changed by this slice. The slice doc explicitly sanctions this outcome
("if the maintainer values per-PR Trivy feedback, this slice is a no-op WONTFIX — do not
force it").

- detection_tier_actual: none
- detection_tier_target: none

(No bug surfaced during this slice. The deliverable is a measurement + a decision; there
is no code path to test. Two pre-existing conditions were _observed_ during measurement
and are recorded in "Findings out of scope" below — neither is a defect introduced here.)

---

## Method

All numbers below come from the GitHub Actions API over the 400 most recent `ci.yml`
runs (2026-06-11T18:42Z → 2026-07-24T16:56Z), job-level `started_at`/`completed_at`
deltas, split at the slice-694 merge boundary (`5b9a959e`, 2026-06-15T21:57:58Z).

Docs-only runs resolve `Container · Trivy scan` via the `trivy-image-stub` twin (median
0s). Every duration below is filtered to **real** scan jobs (>60s) so the stub does not
deflate the medians.

The `AGENT_BLOCKED` escape hatch in the OE ("post-694 duration data unavailable because
of the 2026-06-29 → 2026-07-24 CI-disabled gap") does **not** apply: slice 694 merged
2026-06-15, so there are 42 real post-694 Trivy runs (27 of them on PRs) in the
2026-06-15 → 06-29 window _before_ the gap, plus 3 post-revival runs on 2026-07-24. The
decision rests on measured numbers, not on the three-run July sample.

---

## E1 — Measured `Container · Trivy scan` job wall-time

Real scan jobs only (stub twin excluded).

| Window                                   |   n | min  | median   | mean | p90  | max  |
| ---------------------------------------- | --: | ---- | -------- | ---- | ---- | ---- |
| Pre-694 (06-11 → 06-15T21:57Z)           | 211 | 61s  | **112s** | 113s | 121s | 137s |
| Post-694 (06-15T21:57Z → 07-24)          |  42 | 112s | **136s** | 140s | 160s | 192s |
| Pre-694, `pull_request` only             | 139 | 61s  | 112s     | 113s | 121s | 136s |
| Post-694, `pull_request` only (06-15→29) |  27 | 112s | 139s     | 141s | 160s | 192s |
| Post-revival `pull_request` only (07-24) |   3 | 144s | 168s     | 167s | —    | 188s |

**Post-694 median cost of the job on a code PR: ~136s (2.3 min).**

Caveat on the pre/post split: the windows are unequal (n=211 vs n=42) and the post-window
spans the CI-disabled gap, so the _delta_ between them is a soft number. The **absolute**
post-694 figure (136s median) is the load-bearing input to this decision, and it is drawn
from 42 real runs.

## E2 — Where the time goes (step-level)

| Run           | Date       | Buildx setup | Build atlas image | Trivy scan | Job total |
| ------------- | ---------- | ------------ | ----------------- | ---------- | --------- |
| `27575520857` | pre-694    | — (none)     | 95s               | 15s        | ~127s     |
| `27576195758` | pre-694    | — (none)     | 89s               | 17s        | ~122s     |
| `28356764724` | 2026-06-29 | 6s           | **12s**           | 11s        | **~50s**  |
| `28374838167` | 2026-06-29 | 8s           | 95s               | 19s        | ~146s     |
| `30103166376` | 2026-07-24 | 5s           | 107s              | 12s        | ~144s     |
| `30108454513` | 2026-07-24 | 8s           | 139s              | 14s        | ~188s     |

The Trivy scan step itself is 11–19s. Everything else is the image build.

## E3 — Slice 694's cache is real but bimodal

Log evidence, not inference:

- Run `28356764724` (2026-06-29, dependabot actions-group bump — no Go source touched):
  8 `CACHED` build steps, including the `builder 6/7` step that runs the
  `go build -o /out/atlas ./cmd/atlas` compile. Build step: **12s**. Whole job: **~50s**.
- Run `30108454513` (2026-07-24, post-revival): the `importing cache manifest from gha:...`
  line is present but there are **zero** `CACHED` steps — a full miss (the GHA cache entries
  aged out across the 2026-06-29 → 07-24 CI-disabled gap), plus ~25s of
  `preparing build cache for export` / `writing layer`. Build step: **139s**.

So: 694 turns a Go-source-unchanged PR into a ~50s job (a genuine ~60% win), and buys
roughly nothing on a PR that touches Go — which is most code PRs — where it adds the
buildx setup (~5–8s) and the cache export (~10–25s) on top of an unchanged compile. That
is why the post-694 median moved the _wrong_ way (112s → 136s) even though 694 works
exactly as designed. **This is an observation, not a criticism, and this slice does not
touch, revert, or weaken 694's caching** (explicit OE boundary).

## E4 — Trivy's share of a code PR

Post-694 `pull_request` runs with a real Trivy job (n=30):

| Metric                                         | Value        |
| ---------------------------------------------- | ------------ |
| Median total runner-minutes per code-PR CI run | **99.9 min** |
| Median jobs per code-PR CI run                 | 52           |
| Median `Container · Trivy scan` runner-minutes | **2.38 min** |
| **Trivy's share of billed runner-minutes**     | **2.4%**     |

## E5 — Trivy is nowhere near the critical path

Median duration of the longest post-694 jobs on a code PR (n≥5 each):

| Median | n   | Job                                     |
| -----: | --- | --------------------------------------- |
|   573s | 49  | Go · integration (shard A)              |
|   536s | 50  | Go · integration (shard B4)             |
|   535s | 50  | Go · integration (shard B3)             |
|   450s | 50  | Go · build + test                       |
|   424s | 50  | Go · integration (shard B5)             |
|   322s | 50  | Frontend · Playwright e2e               |
|   226s | 49  | Self-host bundle · end-to-end (migrate) |
|   136s | 42  | **Container · Trivy scan**              |
|    73s | 5   | Go · govulncheck                        |

The PR critical path is the ~9.6-min integration shard tier. Trivy at 2.3 min runs
fully inside that shadow. **Removing it from the PR returns 0s of wall-clock to the
contributor.**

## E6 — Failure history

| Window                     | success | failure | cancelled |
| -------------------------- | ------: | ------: | --------: |
| Pre-694 (real jobs, n=211) |     209 |       0 |         2 |
| Post-694 (real jobs, n=42) |      39 |       3 |         0 |

All 3 failures are the 2026-07-24 post-revival runs (gh#1454 and siblings) — HIGH/CRITICAL
findings in the distroless debian 12.15 base after ~a month of CVE-database drift while CI
was disabled. None was introduced by the PR diff. Across the whole 400-run sample the job
has **never** failed on a diff-introduced finding.

## E7 — Branch-protection verification

`.github/branch-protection.json` `required_status_checks.contexts` (15 entries) does
**not** contain `Container · Trivy scan`. Trivy is advisory, as slice 089 P0-A1 intended.
This slice changes no workflow file and no branch-protection file, so **no required check
is removed and none can dangle** (OE acceptance criterion, and slice 700 AC-5).

`Go · govulncheck`, `Frontend · npm audit`, and CodeQL (`Analyze (go)`,
`Analyze (javascript-typescript)`) are likewise untouched and remain on the PR path.

---

## D1 — WONTFIX: the scan stays on the PR path

**Options considered:**

(a) Move `trivy-image` to a nightly `schedule:` job on `main`; remove or PR-gate the
PR-time job; handle the `-stub` twin; add a job-summary or auto-filed-issue surface.
(b) WONTFIX — leave the job exactly where it is, record why.

**Chosen: (b) WONTFIX.** Four reasons, in descending weight.

**1. The slice's economic premise does not survive measurement.** Slice 700 is built on
`trivy-image` being "the costliest of the advisory scanners" and on "removing a full
Docker build from every code PR" being a material win. Measured: 2.38 runner-minutes out
of 99.9 (**2.4%**), against a 9.6-minute critical path it never touches (E4, E5). Slice
694's own doc estimated the build at "~2–4 min per code PR"; the true figure sits at the
bottom of that estimate and — decisively — sits entirely in the integration tier's
shadow. The whole move buys back 2.4% of billing and **zero** contributor wall-clock.
Note this conclusion does not depend on 694: even the _pre_-694 median (112s, 1.9 min)
was immaterial against the same critical path. 694 shipping did not create this answer,
it merely confirmed it.

**2. Trivy on the PR _is_ diff-correlated for the class that matters most.** The slice's
core argument is that Trivy's findings "are driven by the CVE database, which changes
independently of the PR diff." True for the CVE-drift class — and false for the class
with the worst blast radius. The `changes.code` paths-filter includes `deploy/**`,
`Dockerfile*`, and `**/Dockerfile` (ci.yml:104–107), so `trivy-image` fires precisely
when a PR changes the Dockerfile, bumps the distroless base, or adds an OS package.
Those are the changes where a pre-merge CVE read is worth the most, and where a nightly
finding arrives ~24h late — on `main`, where the remedy is a revert rather than a review
comment. Moving the scan optimizes the noisy case (unchanged base, drifting CVE DB) by
degrading the sharp one (changed base). For a project whose v1 thesis is "the customer
diligences the diligence tool," that trade is backwards.

**3. The alternatives were genuinely alternatives, and one already shipped.** Slice 700's
Dependencies section: "694 and 700 are alternatives: cache it, or move it. Pick one."
694 merged. Its cache is real and measured (E3: a Go-source-unchanged PR drops from ~127s
to ~50s). Stacking 700 on top would re-open a settled call to chase the residual 2.4%.

**4. Moving costs more maintenance surface than it returns.** Option (a) requires a new
scheduled workflow leg, a findings-visibility surface (job summary or auto-filed issue,
per AC-4 — which is itself a new failure mode: issue spam, stale auto-issues, a token
scope), removal-or-gating of the PR job, and correct handling of the `trivy-image-stub`
twin so the check name still resolves on docs-only PRs. That is four moving parts, in the
workflow file the project has already had to repair twice for exactly this class of
mistake (slice 158's invalid-scope silent-parse-failure; the slice 116 stub-twin
misreading recorded in `branch-protection.json`). Paying that for 2.3 runner-minutes per
code PR is a bad ratio.

**Confidence: high.** The numbers are measured across 400 runs, not estimated, and the
conclusion is robust to the pre/post-694 confound noted in E1 — the decision turns on
Trivy's absolute share and its position off the critical path, both of which hold in
every window sampled.

## D2 — Why the 2026-07-24 red runs are not a counter-argument

The live signal cited in the OE is real: three consecutive code PRs on 2026-07-24 failed
`Container · Trivy scan` on unchanged distroless debian 12.15 base CVEs. It is honest
evidence that per-PR Trivy is noisy on a drifting CVE database, and it deserved weighing
rather than dismissing. It does not change the call:

- **A nightly run would be equally red.** Rescheduling does not fix a stale base image;
  it relocates the same red X to a place nobody is looking at the moment they are
  reviewing the change.
- **It blocked nothing.** Trivy is not in `required_status_checks.contexts` (E7). All
  three PRs remained mergeable. The cost of the noise is a red advisory check in the PR
  UI, not a wedged merge button.
- **The correct remedy is a base-image bump**, and secondarily a `.trivyignore` for
  accepted-and-tracked findings (deliberately not shipped by 694 — see that slice's
  decisions-log D2). Both are cheaper and more honest than moving the scanner.

The genuine residual risk in staying put is **alert fatigue**: a permanently-red advisory
check trains the reader to stop reading it, which is slice 419's threat I-1 in a different
costume. That risk is real, but it is created by the un-bumped base image, not by the
scheduling — and it is discharged by fixing the base, which is out of this slice's scope
(see below).

## D3 — What would reverse this decision

Recorded so a future reader does not have to re-derive the threshold:

1. **Trivy reaches the critical path.** If the integration-shard tier gets fast enough
   (slice 417 sharding, further work) that the ~9.6-min shadow shrinks below ~3 min,
   Trivy's 2.3 min starts costing real contributor wall-clock and the move earns its
   complexity.
2. **A `.trivyignore` + base-bump cadence fails to hold.** If the job sits red for weeks
   at a time as a steady state rather than as a post-outage artifact, the alert-fatigue
   cost (D2) overtakes the pre-merge value and moving becomes the lesser evil.
3. **Trivy is proposed for promotion to required.** A required Trivy would make
   CVE-database drift able to wedge unrelated PRs — the exact failure slice 089's P0-A1
   guarded against. In that world the nightly-on-main split becomes the right shape.
   (Slice 419, in flight, promotes long-stable advisory checks to required; `Container ·
Trivy scan` is **not** on its candidate list, which is correct.)

A future slice could also revisit the hybrid this slice deliberately did not build:
narrow the PR-time job's trigger to a Docker-only paths-filter (`deploy/**`,
`Dockerfile*`, `**/Dockerfile`) _and_ add a nightly for CVE drift. That keeps the sharp
case pre-merge and moves the noisy case off the PR. It was out of scope here — slice 700
offers "move or WONTFIX", and its boundary is explicit that the change must not be
forced — but it is the shape worth filing if reason 2 above ever materializes.

---

## Findings out of scope (observed while measuring; not acted on)

Neither is a defect of this slice, and neither is fixed here.

1. **Slice 694's cache is a net median regression on Go-touching PRs.** E3: the cache hits
   fully when Go source is unchanged (~50s job) and misses when it is not, where it adds
   buildx setup + cache export to an unchanged compile (112s → 136s median). 694 works as
   designed; the workload just isn't the one the design pays off on. Worth a follow-on
   look at `mode=max` vs `mode=min` export cost, or at scoping the cache to the `builder`
   stage. **Not touched here** — the OE boundary forbids reverting or weakening 694, and
   this slice does neither.
2. **The distroless base image needs a bump.** Every code PR since CI revival shows
   `Container · Trivy scan` red on HIGH/CRITICAL findings in distroless debian 12.15
   (E6). This is the actual live problem behind the 2026-07-24 signal, it is independent
   of scheduling, and it is the thing most worth fixing next.

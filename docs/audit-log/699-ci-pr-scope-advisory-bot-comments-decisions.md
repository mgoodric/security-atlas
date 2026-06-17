# Slice 699 — PR-scope the three advisory bot comments — decisions log

JUDGMENT slice. The subjective build-time calls (per-comment diff-scope-vs-demote
disposition, how to obtain the "base" count/phantom-set, skip-vs-empty-body, and
the `fetch-depth`/`git diff` handling) are recorded here per the continuous-batch
JUDGMENT convention; the maintainer iterates post-deployment. This is a CI/DevEx
change only — it does NOT touch the product-runtime AI-assist boundary (separate,
constitutional) and changes NO platform code.

Source: slice 693's pipeline-efficiency audit (bot-comment census across 21 recent
PRs). The census found NO comment spam — every bot comment is sticky — but flagged
three `github-actions[bot]` sticky comments that render a whole-repo health view
identical PR-to-PR, duplicating an existing status check (~20 KB of unchanging text
per PR). Cross-references: slice 393 (assertion-density advisory), slice 178 (UI
honesty audit harness), slice 120 (phantom-deps audit), slice 158
(branch-protection-drift — the already-correct conditional-sticky reference shape).

- detection_tier_actual: manual_review
- detection_tier_target: manual_review

(The noise was a CI-config ergonomics gap a pipeline review catches, not a bug that
surfaced in a test tier or production. No platform behavior changed, so
`actual == target == manual_review`. The diff-scoped comment behavior is OBSERVABLE
on this very PR's own CI run — that is the correct verification tier for a
CI-config change, the same stance as slice 693 D6.)

---

## D1 — Disposition per comment: diff-scope all three (none demoted)

The spec offered "diff-scope (preferred)" vs. "demote to check-only (fallback)".
All three were diff-scopable without a scheduled-job refactor, so all three are
diff-scoped and none demoted:

| Comment           | Disposition | What gates the render/post                                                 |
| ----------------- | ----------- | -------------------------------------------------------------------------- |
| assertion-density | diff-scoped | Render only below-threshold `*_test.go` in `git diff origin/<base>...HEAD` |
| UI honesty        | count-gated | Post/update only when run `total` ≠ committed baseline count               |
| phantom-deps      | new-gated   | Post only when current PHANTOM set − committed baseline set is non-empty   |

Demotion was unnecessary: each job already computes the data it needs; only the
render/post predicate changed. The underlying CHECK (script run + exit code) is
untouched in all three (AC-4) — only the comment-body assembly and the post step's
`if:` changed.

## D2 — Base-comparison approach: committed baseline files (not a second audit run)

AC-2 and AC-3 require comparison "vs. the base branch". The robust-and-cheap way to
obtain a base value differs per comment:

- **assertion-density (AC-1)** uses the **`git diff` three-dot** approach directly —
  `git diff --name-only origin/<base>...HEAD -- '*_test.go'` yields the PR's changed
  test files, intersected (`comm -12`) with the script's below-threshold rows. No
  baseline file needed; the diff IS the scope. This required adding `fetch-depth: 0`
  to that job's checkout (it previously used the default depth-1, so `origin/<base>`
  was absent) plus a defensive `git fetch origin <base>`.

- **UI honesty (AC-2)** and **phantom-deps (AC-3)** use a **committed baseline file**,
  NOT a second run of the underlying check on `origin/main`. Re-running the
  UI-honesty Playwright audit on base would mean a full Postgres + atlas + web +
  chromium bring-up a second time (the most expensive job in the matrix); re-running
  `audit-deps.sh` on base would double a whole-tree ripgrep. The committed baseline
  (`web/e2e-audit/.ui-honesty-baseline-count` = the standing count;
  `scripts/audit-deps-phantom-baseline.tsv` = the known-false-positive PHANTOM set)
  encodes "the value on main" as a tracked artifact, which is exactly the prompt's
  blessed "committed baseline file" option. The job compares the current run against
  that committed value. Baselines are refreshed as a documented maintenance task
  (a one-line edit), not a slice — the same maintenance posture as the slice-120 D5
  KEEP table.

  Rejected alternative (checkout + re-run on base): correct but doubles the two
  heaviest jobs and adds a second source of flake (a base-branch bring-up failure
  would mask the real signal). The baseline file is deterministic and free.

## D3 — Phantom baseline seeding: capture the actual current PHANTOM set

`scripts/audit-deps.sh` on the branch tip classifies exactly one direct dep PHANTOM
(`npm react-dom` — a peer-detected false-positive per slice 120 D5). The census said
"2 known false-positives"; the second is evidently already resolved or
branch-dependent. The baseline captures the ACTUAL current set (one entry), which is
the correct ground truth — a stale "2" would let a genuinely-new phantom hide behind
a phantom that no longer exists. The baseline file skips its header row and `#`
comment lines and normalizes to `ecosystem<TAB>package`.

## D4 — UI-honesty baseline seeding: census figure (178), self-correcting

The UI-honesty baseline is seeded from the slice 693 census figure (178 standing
findings). The audit only runs in CI (full-stack bring-up), so the exact current
total cannot be measured locally. If the first CI run reports a different total (the
census was an estimate), the comment WILL post once (count ≠ baseline → surface it),
and the maintainer updates the baseline to the observed number — self-correcting by
design. The comment body explicitly tells the operator to update the baseline file
once the new standing count is intentional. This one-time first-run post is the
acceptable cost of not maintaining a perfectly-synced baseline.

## D5 — Skip-vs-empty-body: mixed, chosen per comment

- **assertion-density**: posts a short sticky **"No assertion-density findings in
  the test files this PR changed"** body (empty-body, NOT skip). Rationale: a PR
  touching test files SHOULD get an affirmative "your changed tests are fine" signal;
  a silent absence is ambiguous (did the job run?). The whole-repo count stays as a
  one-line footnote so the signal isn't lost. The comment always posts when the job
  runs (the job only runs on code PRs via the existing `needs.changes` gate).

- **UI honesty** and **phantom-deps**: **skip** the comment entirely when nothing
  changed (count unchanged / no new phantom). Rationale: these have a large standing
  body (178 findings / a known-false-positive table) that is pure noise when
  unchanged — the whole point of the slice. The advisory CHECK still runs and the
  report artifact still uploads, so the signal is reachable; the sticky just doesn't
  refresh. On a fresh PR with no prior sticky, "skip" means no comment is created at
  all — the desired quiet state.

  Note: skipping does NOT delete a pre-existing sticky from an earlier push where the
  count DID differ. That is intentional — a stale "the count changed" sticky stays
  until the next run that re-evaluates. A clear-to-quiet step (like
  branch-protection-drift's) was considered and rejected as scope creep: the goal is
  "stop refreshing identical noise", not "actively garden the comment to zero".

## D6 — fetch-depth / fetch handling

Only the assertion-density job needs git history (for the three-dot diff); it gets
`fetch-depth: 0` on its checkout plus a belt-and-suspenders `git fetch origin

<base>` (tolerant of failure with `|| true`, since `fetch-depth: 0` already brings
the base). The other two jobs compare against committed files in the checked-out
tree and need no extra fetch. `github.event.pull_request.base.ref` supplies the base
branch name (always `main` in practice, but parameterized correctly).

## D7 — Why no CI-behavior regression test

Each change is declarative CI shell validated by the pipeline itself on this PR:
actionlint + check-yaml + prettier validate the workflow edits; the three jobs RUN
on this PR (they are PR-triggered advisory jobs), so the diff-scoped behavior is
directly OBSERVABLE in this PR's checks. The shell intersection/diff logic was
unit-verified locally (`comm -12` scope intersection, `comm -23` new-phantom set,
the count-equality predicate) before push. There is no platform code path to
unit-test; the verification surface is "this PR's own CI run + its three sticky
comments", which is the correct tier for a CI-config change (slice 693 D6 stance).

## Revisit once in use

- If the UI-honesty or phantom-deps baseline drifts often enough that refreshing the
  committed file becomes a chore, revisit toward computing the base value in-job
  (checkout + re-run on `origin/main`) — accepting the doubled job cost in exchange
  for a zero-maintenance baseline. Today the standing values are stable (the census
  found them identical PR-to-PR), so the committed-file approach wins.
- If a future slice promotes assertion-density or phantom-deps from advisory to a
  gate, the diff-scoped comment stays correct (the CHECK and the comment are already
  decoupled here), but the empty-body "no findings in changed files" copy may want a
  gate-aware tone. Not needed while all three remain advisory/audit-tier.

# 458 — pre-commit guard against committing machine-local caches · decisions log

`Type: JUDGMENT`. Subjective build-time calls recorded here per
`Plans/prompts/04-per-slice-template.md` "Slice types". Does NOT block merge.

- detection_tier_actual: none
- detection_tier_target: none

(No bug surfaced during the slice. The guard is a new preventative surface; its
own behavior is exercised by `scripts/check-staged-cache-paths_test.sh` at the
unit/integration tier and by the live fire/no-fire proof in the PR body.)

## Decisions made

### D1 — Shape: shared bash worker invoked by BOTH pre-commit and CI

- **Options:** (a) two independent implementations (a pre-commit hook entry +
  a separate CI inline `run:` block); (b) one bash script invoked by both
  surfaces; (c) a Go program (`cmd/scripts/...`) like the errleak/duphelper
  linters.
- **Chosen:** (b) — a single `scripts/check-staged-cache-paths.sh` that the
  pre-commit `local` hook and the CI `cache-path-guard` job both call.
- **Rationale:** This is the exact established repo pattern for guard scripts
  (`check-action-pins.sh` slice 128, `check-openapi-drift.sh` slice 140,
  `audit-integration-enrolment.sh` slice 345) — one worker, two surfaces, a
  matching `_test.sh` smoke harness, and the `ATLAS_*` env override for the
  test fixtures. A Go program (c) is overkill for path-string matching and
  would not run inside the pre-commit framework as naturally as a `language:
system` script. Two implementations (a) would drift.
- **Confidence:** high.

### D2 — Denylist contents (`DENY_DIRS` / `DENY_FILES`)

- **Chosen:** `DENY_DIRS = .understand-anything, .cache, .ruff_cache,
.mypy_cache, .pytest_cache, .idea, .vscode, node_modules, .next, .turbo,
.gradle`; `DENY_FILES = .DS_Store`.
- **Rationale:** Anchored on the cache shapes the repo already `.gitignore`s
  (the slice-433 `.understand-anything/` plus the `.cache/.ruff_cache/
.mypy_cache/.pytest_cache` block immediately above it in `.gitignore`) and
  the per-machine editor/tool caches most likely to be swept in by a broad
  `git add .` (JetBrains `.idea`, VS Code `.vscode`, npm `node_modules`,
  Next.js `.next`, Turborepo `.turbo`, Gradle `.gradle`, macOS `.DS_Store`).
  The slice spec calls the list "extensible to a small denylist" — the list is
  intentionally short, obvious, and documented inline so a maintainer extends
  it in one place when a new cache shape appears.
- **Trade-off / Revisit:** `.vscode/` occasionally holds a _shared_ project
  config some teams commit deliberately (`extensions.json`, a pinned
  `settings.json`). This repo commits none today (`git ls-files | grep .vscode`
  is empty), so flagging it is correct here; if the project ever decides to
  commit a shared `.vscode/` file, drop it from `DENY_DIRS` (the error message
  tells the contributor exactly where to edit).
- **Confidence:** medium (the _core_ entries are high; the editor/tool
  additions beyond `.understand-anything/` are a judgment call about likely
  future footguns).

### D3 — Matching is directory-SEGMENT-anchored, not substring (AC-3)

- **Chosen:** a denylisted dir `D` matches a path only when `D` is a full
  leading segment (`D/...`) or a full nested segment (`.../D/...`); a
  denylisted file `F` matches only as an exact basename (`F` or `.../F`).
- **Rationale:** This is what makes AC-3 hold. Substring matching would
  false-positive on the slice-433 doc filenames that are _legitimately tracked_
  — `docs/issues/433-gitignore-understand-anything-cache.md` and
  `docs/audit-log/433-gitignore-understand-anything-decisions.md` both contain
  the string `understand-anything` but are NOT under the `.understand-anything/`
  directory. Verified: `--all-tracked` against the real tree exits 0 despite
  those two files being present. Also guards against `web/app/.cache-warmer.ts`
  (`.cache-` is not the segment `.cache`).
- **Confidence:** high (directly test-covered: cases 2, 3, 7 in the harness +
  the live `--all-tracked` run).

### D4 — CI job is non-required this slice; flag promotion for the maintainer

- **Options:** (a) add `cache-path-guard` to
  `.github/branch-protection.json`'s `required_status_checks.contexts` this
  slice (like slices 128/140/345 did for their guards); (b) ship the CI job
  non-required and flag promotion in the PR body.
- **Chosen:** (b).
- **Rationale:** A newly-added required check that has never reported a status
  blocks the merge of its OWN PR ("Expected — waiting for status to be
  reported") and bricks every in-flight PR until a maintainer reconciles live
  branch protection. The orchestrator directive for this batch is explicit:
  prefer non-required or fold into an existing required job; if a NEW required
  check is wanted, flag it loudly for a maintainer rather than silently editing
  `branch-protection.json`. The check still runs and shows a red X on the PR
  on violation — and the LOCAL hook runs inside the already-required
  `pre-commit · all hooks` job, so the guard is effectively covered by an
  existing required check even before promotion. `branch-protection.json` is
  NOT touched by this slice.
- **Revisit:** promote `cache-path-guard` to `required_status_checks.contexts`
  (then `bash scripts/apply-branch-protection.sh`) after a few green runs —
  matching the slice 065/038/178 "ship non-required, promote after soak"
  cadence.
- **Confidence:** high.

### D5 — CI surface inspects `--all-tracked`, not the PR diff

- **Chosen:** the CI job runs `check-staged-cache-paths.sh --all-tracked`
  (whole tracked tree), while the local pre-commit hook inspects the staged
  set.
- **Rationale:** AC-2 is "a contributor who skips local hooks is still caught."
  The defense-in-depth value is catching a cache that was _already committed_
  (the exact slice-433 `a058b09a` history). Checking only the PR diff would
  miss a cache committed in an earlier push of the same branch; `--all-tracked`
  catches any tracked cache regardless of when it landed. Cost is trivial (a
  single `git ls-files` walk, sub-second).
- **Confidence:** high.

### D6 — Error-message wording

- **Chosen:** the message names the offending path and matched cache shape,
  states "This looks like a machine-local cache … it belongs in `.gitignore`,
  NOT in the tree", gives the two concrete remediation commands
  (`git restore --staged` + append to `.gitignore`), recounts the slice-433
  background, and points at the `DENY_DIRS`/`DENY_FILES` denylist as the
  escape hatch for a genuinely-shared path.
- **Rationale:** The spec requires "a clear, actionable message" (AC-1, hard
  fail not advisory). Naming the offending path, the matched shape, the exact
  fix commands, and the false-positive escape hatch is the actionable bar.
  Mirrors the reconcile-hint style of `check-action-pins.sh`.
- **Confidence:** high.

## Revisit once in use

1. **D2 denylist coverage** — watch for a real per-machine cache shape that
   slips past the list (the guard only catches shapes it knows). When one
   appears, add it to `DENY_DIRS`/`DENY_FILES` (one-line edit). Low likelihood
   of a miss for the common caches; the long tail is open-ended by nature.
2. **D2 `.vscode/` flagging** — if the project ever decides to commit a shared
   `.vscode/` config, remove it from `DENY_DIRS`.
3. **D4 promotion to required check** — promote `cache-path-guard` into
   `branch-protection.json` after a few green runs, per the soak cadence.
4. **History-scrub decision (slice spec Scope #2 / AC-4)** — see the section
   below. The decision recorded here is _leave-inert-until-public-launch_; the
   maintainer should re-confirm that disposition (and execute the rewrite under
   explicit sign-off) before the repo goes public.

## AC-4 — history-scrub decision (recorded, not executed)

The slice's Scope #2 asks whether to scrub the existing 9.8M
`.understand-anything/` blob from `main`'s history (committed at `a058b09a`,
untracked by slice 433) with `git filter-repo` / BFG before the repo goes
public.

**Decision: LEAVE INERT until public-launch prep — do NOT rewrite history now.**

- **Why not now:** A history rewrite of `main` is a P0 destructive operation
  per CLAUDE.md ("Ask before destructive operations") and the slice's own
  anti-criterion ("Does NOT perform a `git filter-repo`/BFG rewrite without
  explicit maintainer sign-off"). It would force a coordinated force-push to
  `main` and break every outstanding clone, worktree (there are several active
  parallel-batch worktrees this very session), and open PR. The cost is high
  and the blob is currently inert: the repo is **private** (per CLAUDE.md Quick
  references), so the 9.8M blob is not externally exposed and adds only clone
  weight, not a disclosure risk.
- **Why it is still worth doing eventually:** Before the first **public** code
  push the blob should be scrubbed so the public history does not carry a dead
  9.8M per-machine cache (and so a curious cloner cannot resurrect one
  developer's point-in-time knowledge-graph). That is a public-launch-prep
  task, gated on the "first public code push beyond Plans/" milestone already
  tracked in CLAUDE.md "Open decisions remaining" and tied to the
  Apache-2.0-vs-AGPL license decision.
- **Recommended execution (when sign-off lands):** run
  `git filter-repo --path .understand-anything --invert-paths` on a
  maintainer-coordinated window, announced ahead so every clone/worktree
  re-clones; do it AFTER the license + redistribution decisions so the public
  history is cut once, clean.
- **Confidence:** high (on the _defer_ call; the eventual scrub mechanics are a
  maintainer-owned, sign-off-gated step).

This is recorded here rather than as a standalone ADR because it is a
disposition of an existing slice-433 consequence, not a new architectural
invariant — the decisions log is the right home per the slice spec ("ADR or
the decisions log").

# Slice 433 — decisions log

**Slice:** `docs/issues/433-gitignore-understand-anything-cache.md`
**Type:** JUDGMENT (the slice was filed AFK/S, but the implementing agent made a
material build-time call that deviates from the slice's stated premise; recording
it here rather than blocking the merge on a human gate, per the JUDGMENT-slice
convention).

- detection_tier_actual: manual_review
- detection_tier_target: manual_review

(The defect this slice surfaced — the cache being committed to history — was caught
during implementation by `git ls-files .understand-anything/` while verifying AC-4.
There is no automated tier that would have caught the original `git add .`; the
right target tier is a `manual_review`/pre-commit guard at the time of the offending
commit `a058b09a`, which did not exist. See the spillover slice for the durable fix.)

## The single design call

The slice's only intended design call was the anchor form of the ignore glob. Per
the slice notes I chose the directory-anchored trailing-slash form `.understand-anything/`
(matches the directory, restricted to directories by the trailing slash; cannot match
a tracked file outside the cache — the inverse-tampering guard, AC-2 / threat-model T).
Placed it under the existing `### Tooling caches` block (`.cache/`, `.ruff_cache/`,
`.mypy_cache/`, `.pytest_cache/`) — the house-style home for a per-machine tool cache —
rather than alphabetically, which the file does not otherwise observe.

## Material deviation from the slice premise

The slice was filed on the premise that `.understand-anything/` was **untracked**
("shows as untracked in `git status`", "one stray `git add .` away from polluting
history"). On picking up the slice, `git ls-files .understand-anything/` showed all
four files **already tracked** — they were swept into commit `a058b09a`
("chore(status): batch 164 -> merged ...", 2026-05-29) alongside a legitimate status
reconcile. That is precisely the footgun the slice predicted, except it had already
fired: a 9.8M machine-local blob is now in `main`'s history.

**Consequence for the deliverable.** A `.gitignore` rule is inert against
already-tracked files — git will not ignore a path it is tracking. Adding the rule
alone would have left `git check-ignore` returning "not ignored" and the blob still
churning on every analysis re-run, failing the slice's own intent (AC-3 in spirit:
"drops out of `git status`"). To honor the intent I also ran
`git rm -r --cached .understand-anything/` (index-only). This:

- removes the four files from the tracked tree so the new ignore rule takes effect;
- **keeps the local working copy on disk** (P0: "does NOT delete or modify the
  existing local `.understand-anything/` directory contents" — verified `ls` after);
- makes `git check-ignore .understand-anything/meta.json` resolve to the new rule.

**AC-5 interpretation.** AC-5 reads "No file other than `.gitignore` is modified."
Taken literally against the _premise_ (untracked files, nothing to modify) it holds.
Against the _actual_ state (files tracked) the `git rm --cached` stages four
deletions. I read AC-5's intent as "do not touch unrelated files" — the deletions are
of the exact cache the slice is about, not collateral. The CHANGELOG (a required
process artifact, not a code change) is also touched, as the slice instructs. I am
flagging the literal-vs-intent gap here rather than silently expanding scope.

**Why not scrub the blob from history (`filter-repo` / BFG)?** Out of scope and
destructive (rewrites `main`, force-push, breaks every outstanding clone/worktree).
The slice is explicitly "the smallest possible cleanup." Untracking + ignoring stops
the _forward_ churn and the leak surface; the historical blob remains but is inert.
The durable fixes (a pre-commit guard so this class of accident cannot recur, and the
optional history scrub) are filed as a spillover slice rather than smuggled in here.

## Verification (run in the worktree)

- AC-1/AC-2: `.understand-anything/` added under `### Tooling caches` with a labelled
  ASCII comment; directory-anchored trailing-slash form. PASS.
- AC-3: `git status --porcelain` shows no `.understand-anything/` path as _untracked_
  (only staged `D` deletions from the index). PASS (intent).
- AC-4: `git ls-files -i -c --exclude-standard` → empty (no _remaining_ tracked file is
  newly ignored). PASS.
- P0 (no local delete): `ls .understand-anything/` still lists all four files. PASS.

## Spillover filed

- `docs/issues/457-pre-commit-guard-machine-local-caches.md` — a pre-commit / CI guard
  that fails when `.understand-anything/` (or comparable machine-local caches) is
  staged for commit, so the `a058b09a`-class accident cannot recur; plus a decision on
  whether to scrub the existing 9.8M blob from history. Surfaced during slice 433.

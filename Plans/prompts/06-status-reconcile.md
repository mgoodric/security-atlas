# 06 — Status Reconcile

Reconcile `docs/issues/_STATUS.md` against actual repo state. Run when:

- A PR was merged outside the prompt flow (manual `gh pr merge` without status update)
- A worktree was abandoned without an `abandoned` status transition
- An open question was resolved/raised, changing the blocked set
- Weekly hygiene to backfill dates and catch drift
- After any out-of-band manual edit to `_STATUS.md`

## Prompt

```
Reconcile docs/issues/_STATUS.md against actual repo state.

Verification per row:

1. For each row currently `merged`: confirm `git log --oneline main` shows a feature-commit referencing the issue number (look for "(#NNN)" in commit messages). Backfill `Merged` date from `git log -1 --format=%cs <hash>`.

2. For each row currently `in-review`: run `gh pr list --state open --json number,title,headRefName`. Confirm the row's PR is still open and matches the listed branch. If the PR has been merged outside the prompt flow → flip the row to `merged` and record the merge date. If the PR was closed without merging → flip to `abandoned` (or `ready` if the branch was deleted) and flag in Notes.

3. For each row currently `in-progress`: run `git worktree list` and `git branch -a`. Confirm a worktree OR branch exists for the row's `Branch` field. If the branch was deleted with no PR → flip to `ready` (or `abandoned` if explicitly killed) and flag. If the branch exists but has had no commits in 7+ days → flag as "stale" in Notes without changing status.

4. For each row currently `not-ready`: re-check dependencies in docs/issues/_INDEX.md. If all deps are now `merged`, transition to `ready` (or `blocked` if step 5 catches a blocker first).

5. Cross-reference Plans/canvas/11-open-questions.md. For each currently-unresolved open question, identify which slices it affects (per the canvas's "blocks" annotations or _INDEX.md's "Open questions tracked" section). Flip those slices to `blocked` and record the question id in Notes. For previously-blocked slices whose blocking question is now resolved → flip back to `ready` (or `not-ready` if deps unmet).

6. Backfill any missing `Started` dates: for `in-progress` and later rows, derive from the first commit on the slice's branch (`git log <branch> --reverse --format=%cs | head -1`).

7. Recompute the counts table at the top of _STATUS.md.

8. Recompute the "Ready set right now" section listing all slices that are now `ready`.

9. Recompute the "In-flight" section listing all `in-progress` and `in-review` rows.

Report-back BEFORE writing _STATUS.md:
- Drift detected (row · old state · new state · evidence)
- Newly ready (rows that transitioned `not-ready` → `ready`)
- Newly blocked (rows that transitioned to `blocked` and why)
- Newly merged (rows backfilled from git log)
- Stale work flagged (worktrees / branches with no activity 7+ days)
- Counts delta (e.g., merged +2, in-review -1, ready +3)

On approval, write the reconciled _STATUS.md AND prepend a "Drift detected — <date>" section listing every transition (keep the last 5 reconcile reports inline; archive older ones to docs/issues/_STATUS_HISTORY.md if it grows past 5).

Use Algorithm mode. Initialize a PRD (id: status-reconcile-<timestamp>).
```

## What to expect back

- A "drift report" before any file changes, listing every transition with evidence
- After approval: an updated `_STATUS.md` with current state, accurate counts, and dates backfilled
- A "Drift detected — <date>" section at the top of the file recording what changed
- Re-derived `Ready set` and `In-flight` sections

## When to run

| Trigger | Cadence |
|---|---|
| Out-of-band PR merge (manual `gh pr merge`) | Within 24h |
| Open question resolved or newly raised | Same day |
| Worktree abandoned without status update | Same week |
| Routine hygiene + date backfill | Weekly |
| Before kicking off a new parallel batch | If `_STATUS.md` hasn't been touched in 3+ days |

## What it does NOT do

- Auto-merge PRs (this is a state-tracker reconcile, not a release tool)
- Re-decompose slices or edit acceptance criteria (that's prompt 01's job)
- Delete branches or worktrees (only flags them as stale)
- Modify `_INDEX.md` (that's the static spec — only edit it via the backlog-review follow-up flow)

## Notes

- The report-back gate is important here too. If reality has diverged significantly (e.g., 5 PRs merged out-of-band), you want to see the proposed transitions before they get written.
- "Stale" flagging is informational. A branch idle for 7+ days might be paused intentionally (waiting on a dep) or genuinely abandoned. The prompt doesn't guess — it flags and you decide.
- Dates use ISO format (YYYY-MM-DD). The prompt extracts them with `git log --format=%cs`.
- If `_STATUS.md` doesn't exist (e.g., wiped accidentally), this prompt seeds it from scratch by walking `git log main` + `gh pr list` + `git worktree list` + `_INDEX.md` deps.

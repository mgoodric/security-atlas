# 699 — PR-scope (or demote) the three advisory bot comments

**Cluster:** CI / DevEx
**Estimate:** S–M
**Type:** JUDGMENT
**Status:** `ready`
**Priority:** P2
**Spillover from:** slice 693 (pipeline-efficiency audit — bot-comment census).

## Narrative

The bot-comment census across 21 recent PRs found NO comment spam — every bot comment is
sticky (edited in place), only `github-actions[bot]` (3/PR) and `codecov[bot]` (1/PR) ever
comment, and security scanners run as status checks, not comments. The one real cleanup:
**three `github-actions[bot]` sticky comments are not PR-scoped and each duplicates an
existing status check**, so they dump ~20KB of unchanging repo-health text onto every PR.

| Comment (marker)                                     | Problem                                                                    | Fix                                                                                                                         |
| ---------------------------------------------------- | -------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------- |
| Assertion density (`atlas-assertion-density-marker`) | Lists ALL ~74 repo files below threshold regardless of what the PR touched | Filter to `git diff --name-only origin/main...HEAD`; post "no findings in changed files" (or skip) when the PR touches none |
| UI honesty (`atlas-ui-honesty-marker`)               | 178 standing findings, identical PR-to-PR                                  | Post only when the count changes vs. base branch, else rely on the existing advisory check                                  |
| Phantom deps (`atlas-phantom-deps-marker`)           | Re-surfaces the same 2 known false-positives every PR                      | Post only on NEW phantoms vs. base branch                                                                                   |

Preferred disposition: make each comment **diff-scoped** (keep the comment, but only show
what the PR changed). Acceptable fallback: **demote to check-only** (the status check already
conveys pass/fail; move the repo-wide census to a weekly repo-health job). Do NOT delete the
underlying CHECK — only the per-PR comment noise changes. The branch-protection-drift comment
is already correctly conditional (`if: exit_code != '0'`) — leave it.

## Acceptance criteria

- [ ] **AC-1.** Assertion-density comment is scoped to files changed in the PR (or demoted to
      check-only with the census moved to a scheduled job).
- [ ] **AC-2.** UI-honesty comment posts only when the finding count changes vs. base (or is
      demoted to the existing advisory check).
- [ ] **AC-3.** Phantom-deps comment posts only when the PR introduces a NEW phantom dep.
- [ ] **AC-4.** All three underlying status checks still run and still gate/report exactly as
      before — only the comment behavior changes.
- [ ] **AC-5.** Each comment step stays STICKY (find-by-marker → update), never new-per-run.
- [ ] **AC-6.** The branch-protection-drift conditional sticky comment is unchanged.

## Anti-criteria

- Does NOT remove any of the three checks (assertion-density gate, UI-honesty advisory,
  phantom-deps audit).
- Does NOT touch the codecov comment (it is the most useful one; slice 693 already added
  `require_changes`).
- Does NOT convert any sticky comment into per-run new comments.

## Dependencies

- Independent. The comment-posting logic lives in `.github/workflows/ci.yml`
  (assertion-density ~3086–3120, UI-honesty ~1962–2012, phantom-deps ~2266–2300; line numbers
  shift with slice 693's timeout inserts — locate by marker).

## Notes

Source: slice 693 audit (bot-comment census). The three comments are already 90% set up to be
diff-scoped (they compute the data; they just render the whole-repo view).
</content>

# 2366 — changelog.sh follow-ups: decisions log

Issue: mgoodric/security-atlas#2366, follow-ups from the review of #2361
(#2357, changelog fragments).

## Detection-tier classification

- **detection_tier_actual:** manual_review. The review of #2361 found that
  `assemble` deleted every fragment even when `check` failed, because
  `body="$(cmd_render)"` suppresses `set -e` inside the substitution.
- **detection_tier_target:** unit. `scripts/changelog_test.sh` now has the
  invalid-fragment case; it fails against the #2361 script.

## Decisions

### D1 — fail closed with explicit status checks, not only `inherit_errexit`

`cmd_assemble` runs `cmd_check || return 1` first, then captures the file
list and the rendered body with `|| return 1` each, and deletes only the
captured list. `shopt -s inherit_errexit` is set too, but guarded with
`|| true`: macOS `/bin/bash` is 3.2 and lacks it, so the explicit checks are
what carry the guarantee. The self-test passes under both bash 3.2 and 5.

### D2 — `require` reads `<status>\t<path>`

The CI step now emits the PR-files `status` beside each filename. Only
`added` or `modified` counts as a fragment, and only at depth 1 with a
name matching `NAME_RE`. A `renamed` fragment does not count, so moving an
old fragment cannot satisfy the gate. A line with no tab reads as
`modified`, which keeps a hand-typed list working locally. A removed code
file still counts as code.

### D3 — `isa/` exemption narrowed to Markdown

The `isa/*` docs exemption is dropped. `isa/*.md` stays exempt through the
existing `*.md` rule; the probe scripts and runners are code.

### D4 — release-please hand-off documented, not automated

Running `assemble` in `release-please.yml` would need the workflow to push
to the release PR branch, which means `contents: write` on a job that
re-runs on every push to `main`. That is a new write permission, so the
hand-off is a documented manual step in `changelog.d/README.md`.
Follow-up if it proves error-prone: a `workflow_dispatch` job that runs
`assemble` on the release branch, reviewed for its permissions.

### D5 — stray-numbered fragments renamed after their slice

The 103 migrated fragments named `1`, `2`, `3`, `6`, `7`, `8`, `10`, `12`
and `16` (and their `-N` suffixes) carried a stray PR number. Each was
renamed `slice-<N>.<type>.md` from the slice number on its first line; no
name collided. This changes their position in `render` (slice names sort
after numbered ones), which is cosmetic.

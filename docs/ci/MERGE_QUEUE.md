# GitHub merge queue

This repo is prepared for GitHub's native merge queue on `main`.

The CI workflow includes the `merge_group` event alongside `push` and
`pull_request`. GitHub requires this trigger for Actions checks that are
required by the target branch; without it, a queued PR can wait for required
checks that never report on the merge-group ref.

## Path filtering on merge groups

The `changes` job treats every `merge_group` event as `code: true`.

That is deliberately conservative. On normal PRs, `dorny/paths-filter`
classifies a docs-only change as `code: false`, so expensive build/test/lint
jobs skip and their same-name stub twins satisfy branch protection quickly.
On merge-queue refs, the diff base is GitHub's temporary queue ref rather
than the PR's usual base branch. A wrong `code: false` result there would let
the stub twins satisfy required checks for code that is about to land on
`main`.

For that reason, `dorny/paths-filter` is skipped on `merge_group` events and
the `changes.code` output is forced to `true`. The docs-only fast path remains
available on `pull_request`, where contributors iterate. The final queue gate
always runs the real code jobs.

## Concurrency

`merge_group` runs use the same CI workflow but do not allow
`cancel-in-progress`. A queue-created validation ref must finish or fail on
its own result; it should not be cancelled because a later push or PR event
uses the same workflow.

## Enabling the queue

The reviewable source-of-truth note lives in
`.github/branch-protection.json` under `$merge_queue_from_slice_415`.
GitHub's protected-branch REST payload used by
`scripts/apply-branch-protection.sh` does not expose the UI toggle for
`Require merge queue`, so the maintainer must enable it in GitHub after the
PR is reviewed and merged.

Exact setting:

GitHub repo -> Settings -> Branches -> the `main` branch protection rule ->
enable `Require merge queue`.

Equivalent UI path:

Settings -> General/Rules -> Merge queue for `main`.

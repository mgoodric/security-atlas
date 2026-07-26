# Slice 415 / OE-479 merge queue decisions

Date: 2026-07-25

Scope: prepare `main` for GitHub's native merge queue. This slice ships the
reviewable CI and branch-protection source changes only. End-to-end queue proof
is deferred to 366b because it requires this PR to merge, the GitHub UI queue
toggle to be enabled, and `main` to be green after OE-455.

## Decisions

### D1: `merge_group` triggers CI

Decision: add `merge_group:` to `.github/workflows/ci.yml` next to `push:` and
`pull_request:`.

Rationale: GitHub's merge queue validates temporary merge-group refs. Required
GitHub Actions checks must run on the `merge_group` event or the queue can wait
for required contexts that never report.

### D2: merge-group path filtering is fail-closed

Decision: force `changes.code` to `true` for `github.event_name ==
'merge_group'` and skip `dorny/paths-filter` for that event.

Rationale: the slice-061 stub-twin pattern is safe only when `code=false` is a
trusted docs-only signal. On merge-group refs, a bad diff base could misclassify
a code change as docs-only. The conservative result is to run the real queue
gate for every merge-group validation. The PR-time docs-only fast path remains
unchanged.

Rejected alternative: pass an explicit `base:` to `dorny/paths-filter` on
`merge_group`. That may be viable later, but this slice has no live queue yet
and therefore cannot prove the temporary-ref base behavior. Forced `code=true`
matches the threat model's fail-closed preference.

### D3: merge-group runs are not auto-cancelled

Decision: scope CI concurrency by event and ref, and set
`cancel-in-progress: ${{ github.event_name != 'merge_group' }}`.

Rationale: PR and push runs can keep their current cancellation behavior. A
queue-created validation run should finish or fail based on the merge-group
content, not be cancelled by unrelated workflow activity.

### D4: required status check contexts are unchanged

Decision: reuse `.github/branch-protection.json`
`required_status_checks.contexts` verbatim. No check is added or removed.

Rationale: this slice changes how required checks are consumed by the merge
queue, not which surfaces are required. Promotion or retirement of advisory
checks remains separate work.

Note: the file currently has `required_status_checks.strict: false`. This is
left unchanged because the merge queue itself validates queued PRs against the
latest protected branch state, and this slice does not need to reintroduce the
manual update-branch requirement.

### D5: branch protection records the queue requirement, UI applies it

Decision: add a `$merge_queue_from_slice_415` annotation to
`.github/branch-protection.json` documenting that `main` must require the merge
queue.

Rationale: this repo's apply script PUTs the classic protected-branch REST
payload after stripping `$` annotation keys. The live GitHub setting named
`Require merge queue` is a branch-protection UI setting for the `main` rule and
is not present in the REST payload this script manages. The annotation keeps
the intended state reviewable without sending an unknown field to the API.

Maintainer setting: GitHub repo -> Settings -> Branches -> the `main` branch
protection rule -> enable `Require merge queue`. Equivalent path: Settings ->
General/Rules -> Merge queue for `main`.

### D6: queue tuning defaults

Decision: use the following UI settings when enabling the queue:

| Setting                              | Default                                                       |
| ------------------------------------ | ------------------------------------------------------------- |
| Merge method                         | Squash                                                        |
| Build concurrency                    | 1                                                             |
| Minimum pull requests to merge       | 1                                                             |
| Maximum pull requests to merge       | 1                                                             |
| Wait time                            | 0 minutes                                                     |
| Only merge non-failing pull requests | Enabled                                                       |
| On failure                           | Remove the failing entry from the queue; requeue after fixing |

Rationale: this is a solo-maintainer repo optimizing away the update-branch
re-CI cascade, not trying to batch many independent PRs into one merge group.
Single-entry groups preserve today's squash-merge audit shape and make the
first live proof easier to inspect. Build concurrency `1` avoids multiplying
the current ~13-minute CI cost while the queue behavior is being proven.

## Revisit list

- 366b: after this PR merges, Matt enables `Require merge queue` in the GitHub
  UI, and OE-455 greens `main`, drive one code PR through the queue and confirm
  the real jobs ran on the `merge_group` run.
- 366b: drive one docs-only PR through the queue and record the observed cost;
  the current policy intentionally runs real code jobs on merge-group refs.
- Consider raising build concurrency above `1` only after several clean queue
  merges and no queue-head flake pattern.
- Consider maximum group size above `1` only if PR arrival rate rises enough to
  justify larger speculative groups.
- Revisit the forced `code=true` merge-group policy only if a future slice can
  prove a correct explicit `dorny/paths-filter` base on live merge-group refs.

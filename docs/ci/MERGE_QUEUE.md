# GitHub merge queue on `main` (slice 415)

Companion to [`PATH_FILTERING.md`](PATH_FILTERING.md). Read that one
first — the path filter is the part of the merge queue that can bite.

## Why the queue exists

`.github/branch-protection.json` sets `required_status_checks.strict: true`
alongside `required_linear_history: true`. Every PR must be up to date with
`main` **and** merge linearly, so whichever PR lands first invalidates the
strict check on all the others. Between "PR goes green" and "maintainer
clicks merge", any intervening merge forces a `gh pr update-branch` rebase
that re-triggers the entire ~13-minute suite from scratch.

For a solo maintainer merging sequentially that roughly doubles the CI
minutes per PR and serializes throughput: PR N+1 cannot start its re-run
until PR N has merged. The cost compounds linearly with queue depth.

GitHub's merge queue answers the "is this still green against current
`main`?" question **once**, by speculatively building each queued PR on top
of the latest `main` and the entries ahead of it. The PR's own branch run is
the gate to _enter_ the queue; the queue's `merge_group` run is the gate to
_land_. The manual update-branch step, and the re-CI cascade it triggers,
go away.

## The event

When an entry is queued, GitHub creates a temporary ref
`refs/heads/gh-readonly-queue/main/pr-<n>-<sha>` containing `main` plus the
entries ahead of this one plus this one, and dispatches a `merge_group`
event against it. On that event:

| Context                             | Value on `merge_group`                           |
| ----------------------------------- | ------------------------------------------------ |
| `github.ref`                        | `refs/heads/gh-readonly-queue/main/pr-<n>-<sha>` |
| `github.sha`                        | the queue entry's speculative head commit        |
| `github.event.merge_group.head_ref` | same as `github.ref`                             |
| `github.event.merge_group.base_ref` | `refs/heads/main`                                |
| `github.event.merge_group.base_sha` | the `main` commit the group is built on          |
| `github.event.pull_request`         | **absent** — there is no PR context              |

That last row is the source of every sharp edge below.

## Every required check must report on `merge_group`

The queue evaluates `required_status_checks.contexts` against the
merge-group commit. A required check that never reports there leaves the
entry pending until the check-response timeout evicts it — the queue wedges.

So `merge_group:` is on the `on:` block of **both** workflows that produce
required contexts:

- `.github/workflows/ci.yml` — 13 of the 15 required contexts
- `.github/workflows/codeql.yml` — `Analyze (go)`,
  `Analyze (javascript-typescript)`

Adding the trigger changes no check in the required set (slice 415 P0-1); it
only lets the existing ones report on the new event.

**Rule for any future required check:** if you add a name to
`required_status_checks.contexts`, the workflow that produces it needs a
`merge_group:` trigger in the same PR.

### The two app-produced contexts

`GitGuardian Security Checks` and `DCO` are produced by GitHub Apps, not by
workflow files in this repo, so this repo cannot add a `merge_group` trigger
for them. Whether they post a status on the merge-group commit is the App's
behaviour, not ours. **This is the first thing to watch when the queue is
enabled** — see the "first-merge watch list" in
[`../audit-log/415-merge-queue-decisions.md`](../audit-log/415-merge-queue-decisions.md).
If either stays pending on a merge group, the break-glass path is to merge
manually (`enforce_admins: true` means the maintainer still cannot bypass
the required checks, so the real fix is an App upgrade or a scoped
follow-up slice — not a bypass).

## The path filter on `merge_group` — the load-bearing bit

The slice-061 pattern fires a **real** job or a **stub-twin** under the
_same_ required-check name, gated on a `changes.code` boolean from
`dorny/paths-filter`. If `code` resolves `false` for a code-touching change,
the stub runs, the required check reports green in ~30 s, and unbuilt,
untested code lands on `main`. On `pull_request` the diff base is
unambiguous (the PR base). On `merge_group` there is no PR context, and
paths-filter would otherwise fall back to a base it _infers_ rather than one
the event states.

The `changes` job handles this in two layers.

### Layer 1 — an explicit diff base

```yaml
- uses: dorny/paths-filter@... # v4
  id: filter
  with:
    base: ${{ github.event_name == 'merge_group' && github.event.merge_group.base_ref || '' }}
```

On `pull_request` and `push` the expression yields `''`, so paths-filter
keeps its native behaviour — slice-061 semantics are untouched. On
`merge_group` it yields `refs/heads/main`, the base the event itself
carries, so the diff is

```
merge-base(main, <queue head>) .. <queue head>
```

which is exactly the union of the changes the entry is about to land.

Two deliberate choices:

- **`base_ref` (a branch ref), not `base_sha`.** A ref name goes through
  paths-filter's normal fetch/merge-base path. Since `main` can only move
  forward (`allow_force_pushes: false`), `merge-base(main, queue-head)`
  is the group's base commit whether or not `main` advanced after the
  group formed.
- **The diff covers the whole group, not just one PR.** If _any_ entry in
  the group touches code, `code=true` for the whole group. That is the safe
  direction.

### Layer 2 — fail closed

A `resolve` step, not the raw filter output, produces the job's `code`
output. On `merge_group` it forces `code=true` when either:

- the event carried no `base_ref` (nothing trustworthy to diff against), or
- paths-filter emitted no `code` value at all.

A universal empty-value backstop covers the same case on any event. The
failure mode of the guard is "spent CI minutes", never "shipped untested
code" (slice 415 P0-2).

The step writes a table to the run's job summary — event, base ref, head
ref, raw filter value, resolved value, and the basis for the decision — so
you can confirm which side of the branch a given merge-group run took
without reading step logs.

### What this means in practice

| Queue entry                           | `code`          | What runs on `merge_group`                                                      |
| ------------------------------------- | --------------- | ------------------------------------------------------------------------------- |
| Touches Go / TS / SQL / workflows / … | `true`          | The real `Go · build + test`, `Go · integration (Postgres RLS)`, `Go · lint`, … |
| Docs / `Plans/` / markdown only       | `false`         | The slice-061 stub-twins — required checks resolve in < 30 s                    |
| Anything, with an untrustworthy base  | `true` (forced) | The real jobs                                                                   |

The slice-061 cost optimization survives the queue: a docs-only entry still
takes the fast path.

## Concurrency

Both workflows use:

```yaml
concurrency:
  group: ci-${{ github.ref }}
  cancel-in-progress: ${{ github.event_name != 'merge_group' }}
```

`github.ref` on `merge_group` is the unique per-entry gh-readonly-queue ref,
so a queue run never shares a group with a PR run or a `main` push run.
`cancel-in-progress` is nonetheless forced off for `merge_group`: a
cancelled run reports its required checks as `cancelled`, which the queue
treats as a failure and evicts the entry on. Queue runs are short-lived and
GitHub tears them down itself when the group is re-formed, so nothing needs
`cancel-in-progress` to reclaim them.

## PR-only jobs

Several advisory jobs are gated `if: github.event_name == 'pull_request'`
because they post sticky PR comments and there is no PR to comment on in a
merge group (`frontend-ui-honesty`, `phantom-deps`, `assertion-density`,
`schema-removal-age`, `branch-protection-drift-validate`). Their stub
siblings carry `if: github.event_name != 'pull_request' || ...`, so they
still post their (non-required) check names on `merge_group`. No required
context depends on a PR-only job.

## Enabling and tuning

The queue configuration is declared in `.github/branch-protection.json`
under `$merge_queue`, with the tuning rationale in
[`../audit-log/415-merge-queue-decisions.md`](../audit-log/415-merge-queue-decisions.md).

It is **not** applied by `scripts/apply-branch-protection.sh`: the classic
branch-protection REST surface that script PUTs to has no merge-queue field,
and neither does the GraphQL `updateBranchProtectionRule` mutation (verified
by introspection, 2026-07-24). The apply surface is the branch-protection UI
checkbox or a repository ruleset. The `$merge_queue.$apply` key in the file
holds the exact maintainer steps.

## Verification

A code-touching entry through the queue should show, in the `merge_group`
run:

- `Detect changed paths` job summary: `resolved code=true`, basis
  `paths-filter diffed against refs/heads/main`.
- The real `Go · build + test`, `Go · integration (Postgres RLS)`,
  `Go · lint`, `Proto · lint + generate diff`, `Frontend · install + build`,
  `Frontend · Playwright e2e`, `Python · ruff` jobs — **not** their
  `*-stub` siblings.
- `CI · merge-gate` green (it fails closed if any required leg is
  `failure` / `cancelled` / `skipped` while `code == 'true'`).

A docs-only entry should show `resolved code=false` and the stub-twins
posting the required check names in well under a minute.

The resulting squash commit on `main` retains the PR author, the DCO
`Signed-off-by` trailer, and the Conventional-Commit subject — the queue
merges with `merge_method: SQUASH` using the PR's own squash message, so
there is no audit-trail regression.

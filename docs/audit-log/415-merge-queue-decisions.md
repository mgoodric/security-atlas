# 415 — Adopt GitHub merge queue to kill the update-branch re-CI cascade — decisions log

- detection_tier_actual: none
- detection_tier_target: none

No bug surfaced during this slice. The work is CI-configuration only: no
product code path changed, so there is no defect to classify. The slice's
own load-bearing risk (T-1, the path filter mis-computing `code` on a
`merge_group` event) is a _prospective_ hazard closed by design (AC-4's
fail-closed guard), not a defect that was caught late.

Two of the slice's acceptance criteria could not be discharged from this
change — see "Undischarged acceptance criteria" at the bottom. They are
maintainer actions, not skipped work.

---

## D1 — The `merge_group` diff base: explicit `base_ref`, with a fail-closed guard (AC-4)

**The threat (T-1).** The slice-061 pattern fires a real job or a stub-twin
under the SAME required-check name, gated on `changes.outputs.code` from
`dorny/paths-filter`. On `pull_request` the diff base is the PR base and is
unambiguous. On `merge_group` there is no PR context: `github.ref` is the
temporary `refs/heads/gh-readonly-queue/main/pr-<n>-<sha>` ref, and
paths-filter falls back to a base it _infers_ (the repository default
branch) rather than one the event states. If it resolves `code=false` for a
code-touching entry, the stub runs, the required check reports green in
~30 s, and unbuilt/untested code lands on `main`. Integrity failure, and
the reason this slice is JUDGMENT rather than AFK.

**AC-4 offered two options.** Either pass an explicit `base:` appropriate to
the queue ref, or unconditionally resolve `code=true` on `merge_group`. The
slice text names unconditional `code=true` as the conservative default.

**Chosen: BOTH, layered.** Explicit base as the primary mechanism, forced
`code=true` as the fallback whenever that base is not trustworthy.

```yaml
base: ${{ github.event_name == 'merge_group' && github.event.merge_group.base_ref || '' }}
```

plus a `resolve` step that produces the job's `code` output and forces
`true` when (a) the event carried no `base_ref`, or (b) paths-filter emitted
no value, or (c) the value is empty on any event.

**Why not the blunt conservative default alone.** It is safe, but it fails
AC-6 outright: with unconditional `code=true`, a docs-only entry pays the
full ~13-minute suite inside the queue, and the slice-061 cost optimization
— the thing AC-6 exists to protect — is broken precisely where queue depth
makes it most expensive. The slice's own narrative says the queue is being
adopted to _reduce_ CI minutes; a rule that puts every markdown PR through
the full matrix at merge time works against that. The blunt default is the
right answer only if there is no clean base available, and there is one: the
`merge_group` payload states its base explicitly.

**Why `base_ref` and not `base_sha`.** Both are in the payload. `base_ref`
(`refs/heads/main`) is a branch ref, which goes through paths-filter's
ordinary fetch + `merge-base` path — the well-trodden code path, the same
one every `pull_request` run uses. `base_sha` would exercise
fetch-a-bare-commit behaviour that is less certain across paths-filter
versions, and a failure there fails the `changes` job and wedges the queue
entry. Correctness is unaffected by the choice: `main` can only move
forward (`allow_force_pushes: false`), so `merge-base(main, queue-head)` is
the group's base commit whether or not `main` advanced after the group
formed.

**The diff covers the whole group, not just one PR.** The queue head
contains `main` + the entries ahead + this entry, so `code=true` if _any_
entry in the group touches code. That is the safe direction: an over-broad
`true` costs minutes, an over-narrow `false` costs correctness.

**The guard's failure mode is money, never integrity.** Every branch of the
`resolve` step that is uncertain resolves to `code=true`. There is no path
through it where an unknown produces `code=false`. That is what makes P0-2
("the stub-twin MUST NOT run in place of a real job on a `merge_group`
event for a code-touching change") structural rather than aspirational.

**Observability.** The `resolve` step writes a job-summary table — event,
`base_ref`, `head_ref`, raw filter value, resolved value, and the basis for
the decision. AC-5 and AC-6 are verified by reading that table on a real
merge-group run plus the run's job list.

## D2 — `merge_group:` added to `codeql.yml` as well as `ci.yml`

`ci.yml` produces 13 of the 15 required contexts; `codeql.yml` produces
`Analyze (go)` and `Analyze (javascript-typescript)`, which are also
required. GitHub's merge queue evaluates `required_status_checks.contexts`
against the merge-group commit, so a required check whose workflow lacks a
`merge_group:` trigger never reports there and the entry sits pending until
the check-response timeout evicts it. Without this, the queue would be
wedged from the moment it was enabled.

The slice's "Do" list names only `ci.yml`. Extending to `codeql.yml` is not
scope creep — it is the same change applied to the second workflow that
produces required contexts, and without it the deliverable does not
function. It adds no check to and removes none from the required set
(P0-1); it only lets existing required checks report on the new event.

`codeql.yml`'s `concurrency` block got the same `cancel-in-progress` fix as
`ci.yml` (D4).

## D3 — Queue tuning defaults (AC-9)

Declared in `.github/branch-protection.json` under
`$merge_queue.configuration`.

| Setting                                    | Value      | Rationale                                                                                                                                                                                                                                                                                                                                           | Revisit when                                                                                 |
| ------------------------------------------ | ---------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------- |
| `merge_method`                             | `SQUASH`   | Matches the repo's squash-merge-to-main convention (CLAUDE.md "Branching") and is the only method compatible with `required_linear_history: true`. Also what preserves AC-7: the squash commit carries the PR author, the DCO `Signed-off-by` trailer, and the Conventional-Commit subject.                                                         | Never, while linear history is required.                                                     |
| `minimum_entries_to_merge`                 | `1`        | Solo maintainer. A larger minimum would make a lone PR wait for a batch that will never form.                                                                                                                                                                                                                                                       | Contributor base grows past ~3 active committers.                                            |
| `minimum_entries_to_merge_wait_minutes`    | `5`        | The window in which a second PR can join the batch before the queue merges a batch of one. Five minutes is short enough not to feel like a stall and long enough to catch a back-to-back queueing.                                                                                                                                                  | If batching never happens in practice — drop toward 0.                                       |
| `maximum_entries_to_merge`                 | `5`        | Bounds how much lands in a single merge. With a ~13-minute suite, a bad head in a batch of 5 invalidates at most 5 entries' speculation.                                                                                                                                                                                                            | Queue depth routinely exceeds ~10.                                                           |
| `build_concurrency` (max entries to build) | `5`        | Bounds the speculative fan-out — the number of merge-group runs in flight at once. Five keeps the runner spend bounded on a personal repo while still pipelining.                                                                                                                                                                                   | GitHub Actions minutes become the binding constraint, or throughput does.                    |
| `grouping_strategy`                        | `ALLGREEN` | Every entry in the group must be green before the group merges. `HEADGREEN` merges the whole batch on the _head_ entry's result — with the slice-061 path filter in play that is precisely the shape of threat T-1 (a green signal that did not validate what it appears to have validated). Not a cost/latency trade worth taking on a merge gate. | Never, while the stub-twin pattern exists.                                                   |
| `check_response_timeout_minutes`           | `60`       | The observed suite is ~13 minutes; CodeQL's `Analyze` legs are the long pole. 60 minutes leaves ~4x headroom so a slow-but-healthy run is not evicted as a phantom failure.                                                                                                                                                                         | If evictions-by-timeout start showing up, investigate the slow leg rather than raising this. |
| `only_merge_non_failing_pull_requests`     | `true`     | An entry whose own branch run is red should not enter the queue and burn speculative builds.                                                                                                                                                                                                                                                        | Never.                                                                                       |

**On-failure behaviour (D-1, the queue-wedge threat).** GitHub's default
stands: an entry whose merge-group run fails is removed from the queue, the
entries behind it are re-grouped and rebuilt, and the author requeues after
fixing. The merge queue does not _create_ the flake risk the slice's threat
model names — it surfaces it, because a flake on a merge-group run evicts an
otherwise-good PR. The existing controls carry over unchanged: the
integration tier's no-retry policy (CLAUDE.md Q-16) means a flake is
investigated rather than papered over, and the flake-budget dashboard
(slice 352) tracks the aggregate rate. The maintainer retains manual merge
as the break-glass path, subject to `enforce_admins: true`.

## D4 — `cancel-in-progress` scoped off for `merge_group` (the slice's "sharp edge")

Before: `concurrency: { group: ci-${{ github.ref }}, cancel-in-progress: true }`.

`github.ref` on a `merge_group` event is the unique per-entry
gh-readonly-queue ref, so a queue run cannot collide with a PR run or a
`main` push run — the group key already isolates it. The change is therefore
belt-and-braces rather than a bug fix, but it is a cheap one and the failure
it prevents is expensive: a cancelled run reports its required checks as
`cancelled`, and the queue treats `cancelled` as failure and evicts the
entry. Queue runs are short-lived and GitHub tears them down itself when the
group is re-formed, so nothing needs `cancel-in-progress` to reclaim them.

Now: `cancel-in-progress: ${{ github.event_name != 'merge_group' }}` on both
`ci.yml` and `codeql.yml`. `push` and `pull_request` behaviour is byte-for-byte
unchanged.

Note the interaction with slice 631's masking analysis: cancellation of a
`main` run was one of the two mechanisms that hid a RED shard for three
weeks. Not cancelling queue runs is the same lesson applied to the new event.

## D5 — `strict: true` retained; the queue's interaction with it documented (AC-3)

`required_status_checks.strict: true` is left as-is by this slice.

The tension is real and worth stating plainly: `strict` is _the_ cause of
the update-branch cascade this slice exists to kill, and GitHub treats
"require branches to be up to date before merging" and the merge queue as
mutually exclusive — the queue subsumes the guarantee, and the UI is
expected to disable `strict` when the queue is turned on.

Retained anyway, for two reasons. First, flipping the merge bar for every
future PR is the maintainer's call under P0-5, and this slice's whole
posture is that the maintainer reviews the branch-protection change before
it goes live. Second, retaining is the fail-safe direction: if the queue is
never enabled, `strict: true` still prevents a stale PR from merging, where
`strict: false` shipped ahead of the queue would silently lower the bar.

**Expected consequence, and the reconciliation.** When the maintainer
enables the queue, live branch protection may report `strict` as off. The
file will then say `true` where live says `false`, and
`branch-protection-drift-live` will surface it on the next push to `main`.
That is expected, not a defect. Reconcile by setting `strict` to `false` in
`.github/branch-protection.json` in a follow-up commit with a note that the
queue now owns the up-to-date guarantee. On the revisit list below.

`required_linear_history: true` and `enforce_admins: true` are both retained
untouched (P0-4). The merge queue is compatible with linear history — it is
why `merge_method` is `SQUASH` — and inherits `enforce_admins`, so the
maintainer cannot bypass the queue's required checks any more than they can
bypass branch protection today.

## D6 — The merge queue is declared under a `$`-prefixed key, not applied by `apply-branch-protection.sh` (AC-2 / AC-10)

`.github/branch-protection.json` is the file-as-source-of-truth for the
merge bar on `main`, and `scripts/apply-branch-protection.sh` PUTs it to the
classic branch-protection REST endpoint after stripping every `$`-prefixed
top-level key (the repo's annotation convention, slice 127).

**The merge queue is not expressible through that surface.** Verified
against the live API on 2026-07-24:

- `GET /repos/mgoodric/security-atlas/branches/main/protection` returns no
  merge-queue field.
- GraphQL introspection of `UpdateBranchProtectionRuleInput` returns no
  merge-queue input — the only field matching `/merge|queue/` is
  `lockAllowsFetchAndMerge`.
- `GET /repos/mgoodric/security-atlas/rulesets` returns `[]` (no rulesets),
  and `repository.mergeQueue(branch: "main")` is `null` (queue not enabled).

Merge queue is configured through the branch-protection UI checkbox or a
repository ruleset (`merge_queue` rule type). Adding a bare `merge_queue`
key to this file would therefore be shipped straight into the PUT payload,
where at best it is ignored and at worst it fails the apply — breaking
AC-10 for a field the endpoint cannot accept.

**Chosen:** declare the full configuration under `$merge_queue`, with a
`$why_this_key_is_dollar_prefixed` note recording the API verification, a
`$apply` key holding the exact maintainer UI steps, and a `$strict_interaction`
note carrying D5. The file remains the single place the merge bar is
described; the apply script keeps working unchanged.

**AC-10 status.** `DRY_RUN=1 bash scripts/apply-branch-protection.sh`
succeeds against the updated file and emits a payload whose top-level keys
are identical to the pre-change payload — the `$merge_queue` block is
stripped as designed. The live apply was **not** run from this slice, for
two reasons: (1) it cannot apply the merge queue anyway (above), so it
discharges nothing this slice needs; (2) it would flip one unrelated live
field. The file declares `allow_fork_syncing: true` where live currently
reports `false` — pre-existing drift, not introduced here, and not this
slice's to resolve. The maintainer-apply invocation is documented in the PR
body per AC-10's second branch.

## D7 — Advisory PR-only jobs need no change

Several jobs are gated `if: github.event_name == 'pull_request'` because
they post sticky PR comments and there is no PR in a merge group
(`frontend-ui-honesty`, `phantom-deps`, `assertion-density`,
`schema-removal-age`, `branch-protection-drift-validate`). Their stub
siblings already carry `if: github.event_name != 'pull_request' || ...`, so
on `merge_group` the stub runs and posts the (non-required) check name. No
required context depends on a PR-only job, so nothing here needed touching.

`CI · merge-gate` (slice 631) works on `merge_group` unmodified: with
`code == 'true'` it requires every required leg to be `success` and fails
closed on `failure`/`cancelled`/`skipped`. It is currently advisory (not in
the contexts list) and stays that way — promoting it is a separate slice.

## First-merge watch list (for the maintainer, at queue-enable time)

1. **`DCO` and `GitGuardian Security Checks`.** Both are produced by GitHub
   Apps, not by workflows in this repo, so this slice cannot give them a
   `merge_group` trigger. Whether they post a status on the merge-group
   commit is the App's behaviour. If either stays pending on the first merge
   group, the entry will be evicted at the 60-minute timeout. This is the
   single most likely first-run failure and the reason to watch the first
   merge group rather than queue five PRs and walk away.
2. **`strict`.** Confirm whether GitHub disabled it (D5) and reconcile the
   file if so.
3. **The `Detect changed paths` job summary on the first code-touching merge
   group.** Confirm `resolved code=true` with basis
   `paths-filter diffed against refs/heads/main`, and that the job list shows
   the real `Go · build + test` / `Go · integration (Postgres RLS)` rather
   than their `*-stub` siblings. This is the AC-5 evidence.
4. **The first docs-only merge group.** Confirm `resolved code=false` and
   sub-minute stub resolution. This is the AC-6 evidence.

## Revisit list

- **`strict: true` → `false`** once the queue is live and GitHub reports the
  strict requirement off (D5). Follow-up commit to
  `.github/branch-protection.json`, not a slice.
- **Teach `apply-branch-protection.sh` to reconcile the merge queue via the
  rulesets API** so `$merge_queue` becomes applied config rather than
  declared config (D6). Worth doing only if the tuning is changed more than
  once; a UI checkbox set once does not justify the machinery.
- **Extend `check-branch-protection-drift.sh` beyond the contexts list.**
  It compares only `required_status_checks.contexts` today (its own v1
  scope note). The merge queue adds a second class of setting that can drift
  silently, and D6's `allow_fork_syncing` finding shows non-context drift is
  already real.
- **Batch tuning.** `minimum_entries_to_merge: 1` /
  `minimum_entries_to_merge_wait_minutes: 5` are guesses calibrated for a
  solo maintainer. Once the queue has run for a few weeks, check whether any
  batch of >1 ever forms; if not, drop the wait to 0 and stop paying it.
- **Promote `CI · merge-gate` to a required context.** Out of scope here
  (P0-1 forbids changing the required set); it is the natural companion to
  the queue because it collapses skipped/cancelled/failed into one
  deterministic signal.
- **`Frontend · vitest` promotion** remains an unrelated open follow-on
  (slice 069 deviations note) — listed only so it is not confused with the
  queue work.

## Undischarged acceptance criteria

**AC-5** (code-touching change through the queue runs the real jobs),
**AC-6** (docs-only change through the queue takes the stub fast path), and
**AC-7** (queue-merged squash commit retains author + DCO trailer +
Conventional-Commit subject) all require the merge queue to be **enabled on
`main`**, and AC-5/AC-6/AC-7 additionally require merging real PRs to `main`
through it.

Enabling the queue changes the merge bar for every future PR the instant it
is switched on. That is exactly the change P0-5 reserves for maintainer
review ("does NOT auto-merge its own PR; the maintainer reviews the
branch-protection change before it goes live"), and the slice's Boundaries
repeat it. Turning the queue on in order to prove the ACs would go live with
the change ahead of the review it is meant to receive.

So this slice ships the configuration, the fail-closed guard that makes the
configuration safe, and the docs — and hands the maintainer the enable step
plus the watch list above. AC-5/AC-6/AC-7 are discharged on the first two
merge groups after enablement; the run URLs belong in the PR body and on the
tracking issue at that point.

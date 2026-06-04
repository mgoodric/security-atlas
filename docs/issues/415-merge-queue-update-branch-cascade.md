# 415 — Adopt GitHub merge queue to kill the update-branch re-CI cascade

**Cluster:** Infra
**Estimate:** 1-2d
**Type:** JUDGMENT
**Status:** `ready` (deps merged — slice 061 path-filter pattern + slice 069 four-surface gate both on main)

## Narrative

**WHY.** `.github/branch-protection.json` sets `required_status_checks.strict: true`
together with `required_linear_history: true`. The combination means every PR must be
up-to-date with `main` AND merge as a linear (rebase/squash) commit. The practical
consequence: between a PR going green and the maintainer clicking merge, any other PR
that lands first invalidates the strict check, forcing a `gh pr update-branch` (rebase
onto new `main`) — which RE-TRIGGERS the entire ~13-minute CI suite from scratch. With a
solo maintainer merging PRs sequentially, this roughly **doubles** the CI minutes spent
per PR (one run to go green, a second run after the mandatory update-branch) and
**serializes** throughput: PR N+1 cannot start its update-branch re-run until PR N has
merged. The cost compounds linearly with queue depth.

**WHAT.** Adopt GitHub's native **merge queue**. A merge queue speculatively builds the
result of merging each queued PR onto the latest `main` (and onto the PRs ahead of it in
the queue), so the "is this PR still green against current main?" question is answered
ONCE by the queue rather than re-answered by a manual update-branch per PR. The PR
author's branch CI run is the gate to _enter_ the queue; the queue's `merge_group` run is
the gate to _land_. This deletes the manual update-branch step and the re-CI cascade it
triggers.

Concretely the slice: (1) adds a `merge_group:` trigger to `.github/workflows/ci.yml`
alongside the existing `push:`/`pull_request:` triggers; (2) configures the merge queue
in branch protection (the `required_status_checks` already enumerate the gating checks;
the queue reuses that contexts list); (3) validates that the slice-061 `changes`
path-filter + stub-twin pattern behaves correctly on `merge_group` events — this is the
real sharp edge (see Threat model T-1 + AC-4/AC-5).

**SCOPE DISCIPLINE.** This slice does NOT change the _set_ of required checks (that is
slice 419's job), does NOT remove `required_linear_history` (merge queue is compatible
with and in fact reinforces linear history), and does NOT alter the path-filter's
docs-only fast-path _intent_ — it only ensures the fast-path resolves correctly under the
new `merge_group` event. Merge-queue settings that are pure tuning (batch size, min/max
entries, wait time) are picked with documented defaults in the decisions log, not
litigated here.

## Threat model

STRIDE pass. A merge queue is a supply-chain / merge-gate control surface: a
misconfiguration can let code reach `main` that was never validated against the `main` it
actually lands on, or can wedge the merge button. Both are real, not rubber-stamp.

**S — Spoofing.** No new authenticated endpoint. The `merge_group` event is GitHub-internal
and runs with the same `GITHUB_TOKEN` scoping (`permissions: contents: read`) as the
existing triggers. No new identity surface.

**T — Tampering (integrity — the load-bearing threat).** **T-1 (path-filter masks a
required check on `merge_group`).** The slice-061 pattern fires a real job vs. a stub-twin
under the _same check name_, gated on a `changes.code` boolean from `dorny/paths-filter`.
On `pull_request` events the filter diffs against the PR base; on `merge_group` events the
diff base is different (the temporary merge-queue ref), and `paths-filter` can mis-compute
`code` — if it wrongly resolves `code=false` for a code PR in the queue, the **stub** runs,
the required check reports green in ~30s, and unbuilt/untested code lands on `main`. This
is an Integrity threat (a required gate silently passes without doing its work) AND an
Information-disclosure-adjacent failure (the green check _misrepresents_ the validation
state). Mitigation: AC-4 forces the `changes` job's `paths-filter` to use an explicit base
on `merge_group` (or unconditionally resolve `code=true` for `merge_group` events — the
conservative default), and AC-5 proves it with a code-touching PR driven through the queue.
P0-2 forbids the stub from ever running on a `merge_group` event for a code change.

**R — Repudiation.** Merge-queue merges are attributed to the queue but the squash commit
still carries the PR author + DCO sign-off + Conventional-Commit message. No audit-trail
regression. AC-7 asserts the merged commit retains the DCO trailer.

**I — Information disclosure.** No tenant data in scope (CI infra). The `merge_group` run
uses the same secret-scoping as today; no new secret is exposed to the queue context.
Anti-criterion P0-3 forbids widening `permissions:` for the new trigger.

**D — Denial of service.** **D-1 (queue wedge).** A flaky required check makes the queue
worse than today: a flake on a `merge_group` run can evict an otherwise-good PR and stall
the queue head. This is why the slice pairs with the integration flake work (slice 417 /
the scheduler-flake fix) — but the merge queue does not _create_ the flake, it surfaces it.
Mitigation: the queue's default "remove from queue on failure, requeue" behavior is
documented; the maintainer retains manual merge as the break-glass path. No unbounded
input is introduced.

**E — Elevation of privilege.** No new role check. The merge queue inherits
`enforce_admins: true` — the maintainer cannot bypass the queue's required checks any more
than they can bypass branch protection today. No privilege boundary moves.

**Verdict: has-mitigations.** The single load-bearing threat is T-1 (path-filter on
`merge_group`); it is mitigated by AC-4 + AC-5 + P0-2 and is the explicit reason this slice
is JUDGMENT, not AFK.

## Acceptance criteria

- [ ] **AC-1.** `.github/workflows/ci.yml` gains a `merge_group:` entry under `on:`
      alongside the existing `push:` and `pull_request:` triggers.
- [ ] **AC-2.** `.github/branch-protection.json` is updated to enable the merge queue for
      `main` (the queue's required checks reuse the existing `required_status_checks.contexts`
      list — no check is added or removed by this slice).
- [ ] **AC-3.** `required_linear_history: true` is RETAINED (merge queue is compatible with
      it); `strict: true` is retained or its behavior under the queue is documented in the
      decisions log (the queue subsumes the strict-update requirement).
- [ ] **AC-4.** The `changes` job's `dorny/paths-filter` is made correct on `merge_group`
      events: either it uses an explicit `base:` ref appropriate to the queue ref, or it
      unconditionally resolves `code=true` for `event_name == 'merge_group'`. The chosen
      approach is recorded in the decisions log with rationale.
- [ ] **AC-5.** A code-touching change driven through the merge queue runs the REAL
      build/test/lint jobs (not the stub-twins) — verified by inspecting the `merge_group`
      run's job list (the real `Go · build + test`, `Go · integration (Postgres RLS)`, etc.
      executed, not their `*-stub` siblings).
- [ ] **AC-6.** A docs-only change driven through the merge queue still resolves the
      required checks via the fast-path (stub-twins) and lands quickly — the slice-061
      cost-optimization is preserved, not broken.
- [ ] **AC-7.** A PR merged via the queue produces a squash commit on `main` that retains
      the PR author, the DCO `Signed-off-by` trailer, and a Conventional-Commit subject.
- [ ] **AC-8.** `docs/ci/PATH_FILTERING.md` (and/or a new short `docs/ci/MERGE_QUEUE.md`)
      documents the `merge_group` behavior of the path-filter so the interaction is not
      rediscovered.
- [ ] **AC-9.** Decisions log at `docs/audit-log/415-merge-queue-decisions.md` records the
      queue tuning defaults chosen (batch size, min/max entries, wait time, on-failure
      behavior) with rationale + a revisit list.
- [ ] **AC-10.** `bash scripts/apply-branch-protection.sh` succeeds against the updated
      `branch-protection.json` (or the maintainer-apply step is documented in the PR body if
      the token scope is not available in the slice's environment).

## Constitutional invariants honored

- No runtime-product behavior changes — this is dev-process / CI infrastructure only; the
  hard AI-assist boundary and tenant-isolation invariants are untouched.
- The four-surface testing discipline (CLAUDE.md "Testing discipline") is PRESERVED: the
  merge queue gates on the same four named checks; it changes _when_ they run, not _whether_.
- Supply-chain pinning discipline (slice 128 `actions-pin-check`) is honored — any new
  `uses:` introduced is SHA-pinned to a 40-char commit.

## Canvas references

- `Plans/canvas/09-tech-stack.md` §9.6 (CI path-filtering pattern — the slice-061 anchor
  this slice must keep correct under `merge_group`).
- CLAUDE.md "Testing discipline (four enforced surfaces)" — the required-check contexts the
  queue gates on.

## Dependencies

- **#061** — `merged` (CI path-filter + stub-twin pattern; this slice must keep it correct
  under the new `merge_group` event).
- **#069** — `merged` (four-surface required-check gate; the queue reuses its contexts list).
- **#116** / **#128** / **#140** / **#159** — `merged` (the required-checks contexts the
  queue inherits).

## Anti-criteria (P0 — block merge)

- **P0-1.** Does NOT change the _set_ of required checks (no check added to or removed from
  `required_status_checks.contexts`). Promotion/retirement of advisory checks is slice 419.
- **P0-2 (security — T-1).** The slice-061 stub-twin MUST NOT run in place of a real job on
  a `merge_group` event for a code-touching change. A green required check on `merge_group`
  must mean the real job ran. Verified by AC-5.
- **P0-3 (security — I).** Does NOT widen the workflow `permissions:` block for the
  `merge_group` trigger beyond the current `contents: read` (+ any scope an existing job
  already declares).
- **P0-4.** Does NOT remove `required_linear_history` or `enforce_admins`.
- **P0-5.** Does NOT auto-merge its own PR; the maintainer reviews the branch-protection
  change before it goes live.
- **P0-6.** Does NOT re-litigate the `-p 1` integration-serialization decision (slice 334)
  or the path-filter's docs-only _intent_ (slice 061) — only its `merge_group` correctness.

## Skill mix (3-5)

`ci-cd-pipeline-builder` · `grill-with-docs` · `Security` (threat-model verification pass) ·
`simplify` · `database-designer` (n/a — no migration).

## Notes for the implementing agent

**Grill output (Phase 2):**

- _Terminology._ "Merge queue" is the GitHub-native feature (`merge_group` event); do not
  confuse with the project's internal "continuous-batch loop" (an orchestration concept in
  `Plans/prompts/07-continuous-batch-loop.md`). They are unrelated — one is a GitHub merge
  primitive, the other is the slice-dispatch workflow.
- _Scope._ The idea as briefed bundled "validate the path-filter behaves on merge_group" —
  that is IN scope (it is the load-bearing risk), not a separate slice. The check-set
  promotion (419) and the integration shard (417) are correctly separate.
- _Already-built check._ `rg -l "merge_group" docs/issues/` returns only incidental
  references (079/089/081/078 mention "merge queue" colloquially); no dedicated slice
  exists. This is the first.

**Threat-model context (Phase 3).** The one threat that survives review is T-1: the
slice-061 path-filter computing `code=false` for a code PR on `merge_group`, letting the
stub satisfy a required check. The fix is conservative — treat `merge_group` as
always-code (`code=true`) unless a correct explicit base is wired and proven. Prefer the
conservative default; the docs-only fast-path matters most on `pull_request` (where humans
iterate), far less on `merge_group` (where the PR already passed its branch run).

**Sharp edge.** Confirm whether the existing `concurrency.cancel-in-progress: true` block
interacts badly with `merge_group` runs (you do NOT want a queued merge-group run cancelled
by a later push). Scope the concurrency group so `merge_group` runs are not cancelled by
unrelated `push`/`pull_request` events. Record the chosen `concurrency` shape in the
decisions log.

**Provenance.** Filed 2026-06-03 as part of the CI-backlog batch (slices 415-420) surfaced
from the ci.yml structural review. Pairs with 417 (sharding) + 419 (promote advisory) +
420 (flake-def) as the CI-velocity cluster.

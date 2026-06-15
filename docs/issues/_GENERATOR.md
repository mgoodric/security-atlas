# Slice status — generated, not authored

`_STATUS.md` is **produced by `scripts/gen-status.sh` (`just status`)**, not
edited by hand. Slice status is a pure function of ground truth, so it is derived rather
than maintained. This replaces the manual reconcile flow
(`Plans/prompts/06-status-reconcile.md`) and the append-only hand-curated `_STATUS.md`.

**The per-merge reconcile PR is retired (slice 741) — you never open a `chore(status)`
reconcile PR.** The in-tree `_STATUS.md` is regenerated **on demand** (`just status`) and is
allowed to **lag** ground truth; it is **non-gating** (slice 741 removed the `status-drift`
gate). git history + `_events.jsonl` are always authoritative; the committed table is a
browsable cache, not a source of truth.

> **No CI auto-push.** Slice 741 attempted a CI job that regenerated `_STATUS.md` and pushed it
> back to `main` automatically. On this **personal** (non-org) repo that push cannot land:
> protected `main` requires status checks that a freshly-created bot commit cannot satisfy, and
> the GitHub Actions integration cannot be added as a ruleset bypass actor on a non-org repo
> (verified 2026-06-12). Slice 744 accepts that staleness (path c) and **removed** the dead
> `status-autoregen` job. Run `just status` whenever you want a fresh browsable copy. See
> [the freshness section](#staying-fresh-regenerate-on-demand-slice-744) below.

## How a slice's state is decided

| State         | Derived from                                                                                                                                                      |
| ------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `merged`      | a commit on `main` matching `type(scope): slice NNN — …` (or explicit `closes/fixes NNN`), excluding `chore(status):` and `docs(issues): add/file` filing commits |
| `in-review`   | an open PR on a branch `feat\|fix/NNN-*`                                                                                                                          |
| `in-progress` | a branch `feat\|fix/NNN-*` with no open PR and not yet merged                                                                                                     |
| _event_       | the last `_events.jsonl` entry for the slice — for states git cannot prove (`deferred`, `blocked`, `not-ready`, `abandoned`, `not-a-code-bug`)                    |
| `ready`       | filed, not merged, not in-flight, no blocking event (default)                                                                                                     |

**Precedence:** `merged > in-review > in-progress > event > ready`. **git-merged is always
authoritative** — it is ground truth.

## Daily use

```
just status          # regenerate _STATUS.md on demand (the in-repo copy may lag; non-gating)
just status-preview  # print to stdout, write nothing
just ready           # list the ready set (pickable slices)
just event 683 not-ready "edge migration-lag; blocked on maintainer access"
```

Run `just status` whenever you want a fresh browsable copy of the tracker. The in-repo
`_STATUS.md` is allowed to lag and is non-gating, so refreshing it is optional housekeeping —
nothing breaks if it is stale.

## Staying fresh — regenerate on demand (slice 744)

The committed `_STATUS.md` is a derived cache. It is regenerated **on demand**, not by CI:

- **How:** run `just status` (or `bash scripts/gen-status.sh`) locally. The output is a pure
  function of `git log` + `_events.jsonl`, so it is reproducible from any checkout.
- **It may lag.** Between refreshes the in-repo copy can be behind `main`. That is expected and
  harmless: the file is **non-gating** (slice 741 removed the `status-drift` gate), and git
  history + `_events.jsonl` are the authoritative source — see
  [How a slice's state is decided](#how-a-slices-state-is-decided) above.
- **No CI auto-push.** Slice 741 added a `status-autoregen` CI job that regenerated the file and
  pushed it back to `main`. On this **personal** (non-org) repo no CI job can push to protected
  `main`: the `GITHUB_TOKEN` cannot satisfy the required status checks, and the GitHub Actions
  integration cannot be added as a ruleset bypass actor on a non-org repo (both verified
  2026-06-12). Slice 744 chose **accept-staleness** (path c) and **removed** that dead job rather
  than keep a perpetually fail-softing push. See
  [`docs/audit-log/744-flake-counter-failsoft-decisions.md`](../audit-log/744-flake-counter-failsoft-decisions.md).

To confirm the in-repo copy is current, run `just status` and check `git status` — a clean tree
means `docs/issues/_STATUS.md` already matches ground truth.

The old informational `status-drift` CI job (which only DETECTED staleness) was removed by slice
741 — the file is non-gating, so there is nothing to detect or block on.

## The event log (`_events.jsonl`)

Append-only, one JSON object per line: `{"slice":N,"to":"<state>","ts":"YYYY-MM-DD","ref":"","note":""}`.
Appending is **conflict-free across parallel worktrees** — each agent writes its own line;
git auto-merges distinct lines. This is the parallel-coordination fix: agents no longer edit
a shared markdown table (the old `_STATUS.md` claim-stake/reconcile churn → merge conflicts).

States that git can prove (merged / in-review / in-progress / ready) do **not** need events —
merge a PR, open a PR, push a branch, or let it default. Events are only for the rest.

## Why this exists

The old `_STATUS.md` was a ~70 KB append-only log of 250+ reconcile batches that an LLM
recomputed from `git log` + `gh pr list` whenever drift was suspected — i.e. a derivable
cache maintained by hand. Two-thirds of `main`'s commit history was `chore(status)`
bookkeeping. Generating status erases that entire class of toil and makes drift impossible
(the file is regenerated, never patched).

## Limitations

- **Ready ≠ dependency-gated.** Post-v1 slices have no machine-readable `deps:` field, so
  `just ready` lists every filed-but-unstarted slice, not a topologically-unblocked subset.
  Add a `deps:` convention to the per-slice template (`Plans/prompts/04-…`) to enable
  dependency-aware ready computation later.
- **Pre-convention commits.** Very early merges that don't follow `type(scope): slice NNN`
  may show as `ready`; record a one-off `merged` event or rely on a `closes NNN` in a
  follow-up. The tool surfaces hygiene gaps rather than hiding them in a hand-edited file.

## Migration status (completed 2026-06-10)

`_STATUS.md` is now the **generated** live tracker. The prior hand-authored log was
preserved as [`_STATUS_HISTORY.md`](./_STATUS_HISTORY.md) (250+ reconcile batches, kept
for the audit trail). The v1 backlog (slices 001–058), which predates the
`type(scope): slice NNN` commit convention, was backfilled as `merged` events
(`_events.jsonl`, dated to the slice-071 audit, 2026-05-15).

Wiring:

- `Plans/prompts/05-parallel-batch.md` / `07-continuous-batch-loop.md` claim slices by pushing
  the `feat/NNN-…` branch; the merge of the slice PR is the terminal action. They no longer run
  `just status` or open a reconcile PR — the in-repo tracker is regenerated on demand and is
  allowed to lag (slice 741 retired the reconcile PR; slice 744 confirmed accept-staleness).
- `Plans/prompts/06-status-reconcile.md` is **retired** (slice 741) — there is no reconcile step,
  manual or otherwise.
- There is **no** status CI job. Slice 741 added a `status-autoregen` job that tried to push the
  regenerated `_STATUS.md` to `main`; that push cannot land on this personal repo's protected
  `main` (see the [freshness section](#staying-fresh-regenerate-on-demand-slice-744)), so slice
  744 removed it. The earlier informational `status-drift` drift check
  (`scripts/check-status-drift.sh` / `just status-check`) was already removed by slice 741 — the
  file is non-gating, so nothing detects or blocks on staleness. Refresh on demand with
  `just status`.

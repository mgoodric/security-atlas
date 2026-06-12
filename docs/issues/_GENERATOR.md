# Slice status — generated, not authored

`_STATUS.md` is **produced by `scripts/gen-status.sh` (`just status`)**, not
edited by hand. Slice status is a pure function of ground truth, so it is derived rather
than maintained. This replaces the manual reconcile flow
(`Plans/prompts/06-status-reconcile.md`) and the append-only hand-curated `_STATUS.md`.

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
just status          # regenerate _STATUS.md
just status-preview  # print to stdout, write nothing
just ready           # list the ready set (pickable slices)
just status-check    # CI gate: fail if committed status is stale vs git/PRs
just event 683 not-ready "edge migration-lag; blocked on maintainer access"
```

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

- `Plans/prompts/05-parallel-batch.md` / `07-continuous-batch-loop.md` claim slices with
  `just event <slice> in-progress` and refresh with `just status` instead of hand-editing.
- `Plans/prompts/06-status-reconcile.md` is now **verification-only** — `just status` does
  the reconcile deterministically; 06 only investigates genuine anomalies.
- CI runs `just status-check` (deterministic merged-set gate).

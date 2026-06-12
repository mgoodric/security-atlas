# 06 — Status Reconcile (verification-only)

> **Superseded 2026-06-10.** `docs/issues/_STATUS.md` is now **generated** by
> `scripts/gen-status.sh` (`just status`), not hand-reconciled. Slice status is derived
> from `git log` + open PRs + branches + `docs/issues/_events.jsonl`. The old 9-step
> manual reconcile below is retired. See [`docs/issues/_GENERATOR.md`](../../docs/issues/_GENERATOR.md).

## The reconcile is now one command

```
just status        # regenerate docs/issues/_STATUS.md from current ground truth
```

There is no drift to hand-fix: the file is regenerated, never patched. What used to be the
trigger list (out-of-band merge, abandoned worktree, resolved open question) is handled
automatically because state is derived:

| Old trigger                            | Now handled by                                                           |
| -------------------------------------- | ------------------------------------------------------------------------ |
| PR merged outside the flow             | `git log` shows `type(scope): slice NNN` → `just status` marks it merged |
| Worktree/branch abandoned              | branch gone + no merge → falls back to `ready` automatically             |
| Open question blocks a slice           | `just event <slice> blocked "<q-id>"` (event overlay)                    |
| Deploy-note / not-a-code-bug spillover | `just event <slice> not-ready "<why>"`                                   |
| Weekly hygiene / date backfill         | n/a — dates derive from the merge commit                                 |

## What this prompt is now FOR (verification only)

Run it to investigate a **genuine anomaly** — a slice whose derived state looks wrong:

```
A slice's generated status looks wrong. Investigate WITHOUT editing _STATUS.md by hand.

1. Run `just status` and re-read the row.
2. If a merge isn't reflected: confirm the merge commit follows `type(scope): slice NNN`
   (the convention gen-status keys on). If an early/pre-convention commit doesn't, record
   an authoritative `just event <slice> merged "<evidence>"` rather than editing the table.
3. If an in-progress slice is actually abandoned: delete its branch; `just status` will
   reclassify it `ready`. If it should stay parked, `just event <slice> deferred "<why>"`.
4. If a blocked/deferred slice is now unblocked: `just event <slice> ready "<why>"`
   (or just let derivation take over once a branch/PR appears).
5. Run `just status-check` — it is the deterministic CI gate (compares the git+events
   merged-set against _STATUS.md). A green check means the tracker is current.

Never hand-edit docs/issues/_STATUS.md — it carries a GENERATED banner and will be
overwritten on the next `just status`.
```

## CI

`just status-check` runs in CI and fails the build if a slice merged on `main` is not
reflected in `_STATUS.md` — i.e. someone merged without running `just status`. The fix is
always `just status` + commit the regenerated file.

## Historical record

The prior hand-authored tracker (250+ reconcile batches) is preserved at
[`docs/issues/_STATUS_HISTORY.md`](../../docs/issues/_STATUS_HISTORY.md).

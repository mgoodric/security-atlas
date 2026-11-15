# #1775 — SCF remap queue: decisions log

detection_tier_actual: none
detection_tier_target: none

Importing a new SCF release now queues one remap proposal per non-pinned control left on an older release's anchor (`scf_remap_proposals`), and `GET /v1/controls/remap-proposals` lists them, filterable by `class` and `status`.

## D1 — The queue records the #1971 D3 re-bind; it does not undo it

The ticket says "nothing moves". It was written before #1999, which landed Matt's D3 rule (2026-11-14, `1971-scf-iac-remap-decisions.md`): `catalog import-scf` re-binds a control in place when its code keeps the same title. The dispatcher's direction was to keep D3 and have the queue record it, not to revert it.

So the queue keeps two things apart:

- **`class`** is what SCF's legacy IDs say about the anchor's lineage (`unchanged`, `renamed`, `split`, `reused_id`, `no_successor`).
- **`status`** is what the D3 upgrade did with the control. It is `rebound` when the control was moved in place and `open` when it still waits for a person.

The proposal itself moves nothing. The only control that moves is one D3 moves, and its proposal says so. The integration test checks that every `open` control keeps its release-A anchor.

## D2 — How successors are read from legacy IDs

A current anchor continues a legacy anchor `X` when:

- SCF lists `X` as its legacy ID; or
- it has code `X` and lists no legacy ID of its own.

A current anchor with code `X` whose legacy ID names a different code is a reused ID (`reused_id`). Its proposed successors are the anchors that list `X` as their legacy ID, if any. When there is exactly one successor, a matching title makes it `unchanged` and a different one makes it `renamed`. Several successors make it `split`, and none makes it `no_successor`.

A release with no legacy IDs at all gives no lineage, so every control is `no_successor`. That includes a control D3 re-bound because its code kept its title. That control's proposal is `no_successor` / `rebound`. The class reports what the catalog can prove, and the status records the move.

## D3 — Pinned controls get no proposal

A control pinned to its release (#1770: a declared `scf_release`, or an anchor named by UUID) is bound there on purpose. D3 leaves it alone and so does the queue. A control whose stored manifest does not parse is flagged by D3 and gets an `open` proposal.

## D4 — Re-runs and storage

- There is one proposal per control per target release (`UNIQUE (control_id, target_framework_version_id)`). A repeated import leaves an existing row alone.
- The table is written by the migrate role inside the re-bind transaction. The app role has `SELECT` only, under a `tenant_read` RLS policy with FORCE on. Accepting or dismissing a proposal is not in this ticket, so no write grant exists yet.
- Rows cascade with their control, legacy anchor and target release, so the catalog reset helpers used in tests keep working.
- The import summary gains one line: `remap_proposals unchanged=N renamed=N split=N reused_id=N no_successor=N`. It counts the proposals of this run.

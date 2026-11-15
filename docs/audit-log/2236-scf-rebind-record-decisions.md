# #2236 — SCF re-bind: a durable record of each anchor move and flag

detection_tier_actual: manual_review (the #1999 review)
detection_tier_target: integration

Every SCF re-bind now leaves a row in `scf_remap_proposals`, in the transaction that moves or flags the control, from both paths: the boot-time `RebindControls` and the upload path's in-place re-bind (`control.Store.Upload`).

## D1 — Extend `scf_remap_proposals`, no second table

#1775 (#2293) already writes one row per non-pinned control per target release from `RebindControls`, in its transaction, with the tenant, control, legacy anchor, both releases and `status` (`rebound` or `open`). That is the record the ticket asks for, short of three things, which migration `20261124000000` adds:

| Gap                                  | Fix                                                                                           |
| ------------------------------------ | --------------------------------------------------------------------------------------------- |
| The anchor the control moved to      | `target_anchor_id`, set on `rebound` rows and NULL on `open` rows (CHECK, `NOT VALID`)        |
| Which path did it                    | `source` (`boot` or `upload`, CHECK); existing rows backfill to `boot`, the only writer then  |
| The upload path wrote nothing at all | The app role may `INSERT` (and `UPDATE`) its own tenant's rows; RLS `tenant_insert`/`_update` |

A control-revision row (`control_manifest_revisions`) was the other option the ticket named. It records manifest text, which a re-bind does not change, so it would carry the anchor ids in a table built for something else. An audit event in `audit_log` would duplicate what the proposals row already holds.

`UPDATE` is granted on `status`, `target_anchor_id` and `source` only, because #2321 turns the upsert into `ON CONFLICT ... DO UPDATE` (open to rebound), which needs it once the upload path shares the query.

Once the app role can write, the plain `control_id` FK is not enough: tenant T could insert a row naming tenant U's control, take U's `(control_id, target)` slot, and U's real record would be dropped by `ON CONFLICT`. The migration adds the composite FK `(tenant_id, control_id) REFERENCES controls (tenant_id, id)`, as `control_evaluations` has. `TestRebindRecord_AppRoleCannotWriteAnotherTenantsRecord` checks both refusals (RLS for `tenant_id = U`, the FK for T naming U's control) and fails with the FK dropped. (#2336 review B1/B2.)

## D2 — The backfill of `target_anchor_id`

A `rebound` row written before this change has no target anchor. Its control was moved in place, so the control's current `scf_anchor_id` is the target unless someone re-anchored it since. The migration backfills from that, and the status/target CHECK is `NOT VALID` so a row it cannot backfill does not block the migration. New rows must satisfy it.

## D3 — Flags are records too; "a flagged control writes none"

The ticket's test line says a pinned or flagged control writes no record, and its second checkbox says to record the flags. Read together: a flagged control writes no _re-bind_ record (no `rebound` row, no target anchor) and one `open` row, the flag. A pinned control writes nothing. The tests assert exactly that.

## D4 — The upload path classifies with the boot path's reader

The upload path reads the control's row of `ListControlsOnLegacySCFAnchors` before it moves anything and records it through `scfimport.RecordRemapProposal`, the same function `RebindControls` uses, so the `class` and successor codes cannot disagree between paths. It filters the tenant's legacy-anchor list in Go rather than adding a control filter to that query; the list is only read when an upload actually re-binds or flags, which is rare. If the control has no row there (not expected, since its bound anchor is outside the current release), the upload logs a warning and goes ahead without the record rather than failing.

## D5 — History across imports

`UNIQUE (control_id, target_framework_version_id)` keeps one row per control per target release. A control that moves at boot is no longer on a legacy anchor, so it is never recorded twice for the same release; a later release gets its own row. A re-upload of a flagged control finds its row and leaves it, so the first flag's time is the one kept.

Tests: `TestRebindRecord_BootWritesOneRecordPerMovedControlAndNoneForPinned` and `TestRebindRecord_UploadInPlaceRebindAndFlagAreRecorded` (`internal/control`). The upload test fails with the upload-path writes removed.

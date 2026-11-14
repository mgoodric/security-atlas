# #1768 — freeze SCF anchors; refuse same-label re-imports with different content — decisions log

**Ticket:** https://github.com/mgoodric/security-atlas/issues/1768 (parent #1718, release-aware SCF anchors)

## Detection tier

The defect: `scfimport.Import` updated anchor content in place when a catalog
was re-imported under a release label that already existed, so a changed
catalog silently rewrote the meaning of every control and crosswalk edge bound
to those anchors. `TestImport_DetectsContentUpdates` asserted that behaviour as
intended.

- **`detection_tier_actual`: `manual_review`.** Found in the #1718 triage, not
  by a test.
- **`detection_tier_target`: `integration`.** The refusal and the trigger are
  both real-Postgres behaviour; the new integration tests cover them.

## Decisions

### D1 — Added and missing anchors are differences too. **Confidence: HIGH.**

The ticket says "any difference is an error listing the differing anchors".
An incoming catalog that adds an anchor, or drops one, under an existing label
is a different release, so both are refused. `ReleaseConflictError` lists them
separately (`Changed`, `Added`, `Missing`) so the operator sees which kind.

### D2 — The comparison runs before any write. **Confidence: HIGH.**

The same-label check runs first in the transaction, before the framework
upsert, so an identical re-import touches no row at all (the test compares
`xmin` on `scf_anchors`, `framework_versions` and `frameworks`). A side effect:
re-importing an older label no longer re-marks it `current` beside the real
current release, which the previous upsert path did.

### D3 — A version row with no anchors falls through to a normal load. **Confidence: MEDIUM.**

If a `framework_versions` row exists for the label but holds no anchors, there
is nothing frozen to compare against, so the importer loads it. Refusing would
leave that release permanently unloadable.

### D4 — The trigger guards content columns only. **Confidence: HIGH.**

`trg_scf_anchor_immutability` rejects an UPDATE only when `scf_id`, `family`,
`title`, `description`, `subtopics` or `framework_version_id` changes. Other
updates pass, so #1769 can add and backfill a legacy-SCF-ID column without
lifting the freeze. DELETE stays unguarded, matching the
`framework_requirements` trigger (catalog GC and test teardown CASCADE through
it).

### D5 — The app role is refused by its GRANT, not the trigger. **Confidence: HIGH.**

`atlas_app` holds only SELECT on `scf_anchors`, and Postgres checks privileges
before BEFORE triggers fire, so an app-role UPDATE fails with "permission
denied". The test asserts the app role is refused, and asserts the trigger's
own message for the migrate role, the one role the GRANT cannot stop.

### D6 — Subtopics compare by value. **Confidence: HIGH.**

The old byte comparison of stored JSONB against `json.Marshal` output only
matched when there were no subtopics, because Postgres re-serializes JSONB.
That was harmless while a mismatch meant an extra UPDATE; with mismatch now an
error, it would have refused every identical re-import of a catalog with
subtopics. Subtopics now unmarshal and compare by value, null equal to empty.

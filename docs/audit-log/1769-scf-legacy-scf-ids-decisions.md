# Issue 1769: SCF catalogs carry legacy SCF IDs: decisions log

**Issue:** https://github.com/mgoodric/security-atlas/issues/1769

- **`detection_tier_actual`: `none`.** No bug surfaced; this adds a field.
- **`detection_tier_target`: `none`.**

## The change

The JSON catalog accepted by `atlas-cli catalog import-scf` gains an optional per-control `legacy_scf_id`. Migration `20260926000000` adds a nullable `scf_anchors.legacy_scf_id`; the importer stores it, and `GET /v1/anchors` (plain and `include=state`, current and `framework_version_id` reads) and `GET /v1/anchors/{id}` return it. Reading it from the workbook's `Legacy SCF #` column is #1719's follow-up.

## Decisions

### D1: NULL, not empty string, when the catalog does not say. **Confidence: HIGH.**

An absent value and "this control had no previous code" are both "we don't know a legacy code". NULL keeps that one state, and the wire field is omitted (`omitempty`) rather than sent as `""`, so the response for a catalog without the field is byte-identical to before.

### D2: a same-label re-import fills NULLs and refuses a different value. **Confidence: MEDIUM.**

#1768 froze imported anchors but kept `legacy_scf_id` out of the trigger's guarded columns so it could be backfilled. Without a backfill path, a release imported before this change could only gain legacy IDs by importing under a new release label. So a re-import of the same `release_version` sets `legacy_scf_id` on anchors stored without one (`Report.Updated` counts them). A stored value is never rewritten: a re-import that names a different one is a `ReleaseConflictError` listing the anchor under `Changed`, consistent with the rest of the freeze. A re-import that omits the field leaves stored values alone, so an older converter re-run never erases them.

### D3: the anchor catalog export is unchanged. **Confidence: MEDIUM.**

The ticket asks for "anchor reads". The export (`/v1/anchors/export`) has fixed CSV/XLSX column headers pinned by tests; adding a column there is a format change to make on request, not as a side effect of this one.

# Issue #1908 — description-only control re-uploads update in place

Build-time calls made fixing
[#1908](https://github.com/mgoodric/security-atlas/issues/1908), raised by the
#1901 review. `control.Store.Upload` treated any byte change to `control.yaml`
as a new version. Superseding mints a new control id, and every row keyed on
the old id stayed behind on the superseded row: owners, evidence (with
freshness and drift), evaluations, exceptions, risk / decision / action-plan /
change / incident links, walkthroughs, populations and checklists. A
description reflow therefore made a manual or attested control read as
missing or failing until someone re-uploaded its evidence.

`detection_tier_actual = manual_review` (the #1901 review) ·
`detection_tier_target = integration` (now covered by
`TestUpload_DescriptionOnlyChangeUpdatesInPlace`).

## D1 — A change confined to `description` updates the active row in place

**Decision.** When the incoming manifest's hash differs from the active
version's, `Upload` decodes both manifests to generic YAML maps, deletes the
top-level `description` key from each, and compares what is left. If they
are equal, it runs `UpdateControlDescriptionInPlace` in the same transaction
and under the same tenant RLS context as the rest of the upload. That query
sets `description`, `bundle_manifest_yaml`, `bundle_manifest_hash`,
`bundle_uploaded_at`, `bundle_uploaded_by` and `updated_at` on the active row.
The control id and version are unchanged and nothing is superseded. Because
the hash moves with the YAML, uploading the same bytes again is still a no-op.

Any other difference supersedes exactly as before, including a `title`
change and a nested `evidence_queries[].description` change (that one is
part of a query's definition, not the control's prose).

**What counts as description-only.** The comparison is on decoded values, so
rewording, reflowing (quoted string to block scalar), adding or removing the
`description` key, reordering keys and editing comments all count as prose.
`description.md` is outside `control.yaml` and outside the hash, so it never
triggered a version bump and still does not. When `control.yaml` changes in
place, the stored description is rebuilt from both, as on insert.

**This bends slice 009 AC-6** ("re-uploading the same bundle id is a version
bump"). The byte-identical no-op (slice 068) was the first exception; this is
the second. `docs/spec/control-bundle.md` "Versioning + supersession" now
states both.

**Trade-off.** The prior description text and manifest bytes are overwritten,
not kept as a version. Auditors lose byte-exact reproducibility of the old
prose, which the spec promised for every version. We accept that because the
prose does not affect evaluation, applicability, scope or framework mapping,
and keeping it as a version currently costs every linked record.

**Rejected: copy-forward (option B in the #1901 review).** Keep versioning on
every change, and copy or re-point every control-keyed row to the new id in
the upload transaction. It keeps every prose version, but it has to touch the
dozen-plus tables listed above, and every new control-keyed table must
remember to join that list. A missed table fails silently in exactly the way
this issue describes. Evidence also lives in an append-only ledger
(constitutional invariant 2), so it could only be re-pointed by a read-time
lineage join, not moved. Option B remains open if a prose history turns out to
be required. It would supersede this decision rather than extend it.

## D2 — The upload response does not change shape

An in-place update returns `200` with the existing `control_id` and
`version`, `is_new_bundle=false` and no `superseded_id`, which is the same
shape as the byte-identical no-op. `UploadResult.UpdatedInPlace` tells the
two apart for in-process callers. The HTTP body has no new field, so the
OpenAPI document does not change.

## D3 — Evidence pushed to a superseded control id: out of scope, follow-up

Ingest (`internal/evidence`) does not check `controls.superseded_by`, so a
connector configured with an old control id keeps appending evidence to the
superseded row with no warning. This change makes that rarer, because prose
edits no longer mint new ids, but it does not fix it. Real content changes
still supersede. Whether ingest should refuse such a push or redirect it to
the bundle's active version is filed as
[#1910](https://github.com/mgoodric/security-atlas/issues/1910).

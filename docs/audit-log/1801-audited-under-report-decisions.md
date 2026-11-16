# #1801 — the "audited under" report (API and CSV)

Parent: #1735 (tenant overlay edges). Consumes #1737 (tested-under
external references), #1736 (`anchor_fit`) and #1798 (overlay edges in
`effective_fw_to_scf_edges`).

detection_tier_actual: none
detection_tier_target: none

## What changed

- `GET /v1/mapping-overlays/audited-under` (`?framework=<slug>`): one row
  per active control and tested-under criterion that the control's anchor
  does not reach through catalog plus the tenant's active overlay edges.
  Each row carries the control, the criterion, the anchor, `anchor_fit`
  (and its note), the tested-under reference's note, and candidate anchors.
- `GET /v1/mapping-overlays/audited-under/export`: the same rows as CSV.
- Two sqlc queries (`internal/db/queries/audited_under.sql`), a store
  method (`mappingoverlay.Store.AuditedUnder`) and the handlers. No
  migration.

## Decisions

**D1 — "Reaches" is coverage's rule, read from the same view.** A pair is
reached when `effective_fw_to_scf_edges` has a live edge (not
`no_relationship`, not `rejected`) from the criterion to the control's own
anchor. That view already unions the caller's active overlay edges (#1798),
so a new overlay edge closes a row on the next read and a retired one
reopens it. Nothing is cached.

**D2 — The control's anchor is matched exactly, not by SCF code.** A
control anchored to a legacy-release anchor is reached only through edges
to that anchor row, which is how coverage credits it. Matching by code
across releases would show a criterion as reached that coverage doesn't
credit.

**D3 — Candidate anchors come from catalog edges in the current SCF
release.** This matches coverage's default release (#2203). The response
names the release (`candidate_scf_release`, also a CSV column). When no
SCF release is current (an empty catalog), candidates come from every
release and the field is null. Overlay edges are never candidates: they are
the organisation's own assertion, not a re-anchoring suggestion.

**D4 — Tested-under references are resolved in the framework's current
version.** `system` is the framework slug and `id` the requirement code,
looked up in the global catalog's current version. A reference that
resolves to no requirement there is not a row: it names no criterion an
overlay edge could close. Surfacing unresolvable references (typos,
withdrawn codes) is a separate concern and is not in this report.

**D5 — An unanchored control lists every criterion it was tested under.**
It reaches nothing, so each tested-under criterion is a gap, with
`anchor_id` null. Such a control is usually `anchor_fit: none`, which the
row shows.

**D6 — `anchor_fit` is always on the row.** Every control has a value
(`exact` by default), so the field is always present. `anchor_fit_note`
is omitted when empty.

**D7 — Read access matches the overlay list.** Any credential of the
tenant may read the report (it is under `/v1/mapping-overlays`, like the
list and export), and RLS binds controls and overlay edges. There is no
write.

**D8 — CSV cells go through the shared encoder.** `export.NewCSVExporter`
neutralises cells starting with `=`, `+`, `-`, `@`, tab or CR. Control
titles and tested-under notes are organisation-entered text.
`TestAuditedUnder_CSVNeutralisesFormulas` pins it.

## Not in this change

- The web page and the "add overlay edge" row action (#1735 user story 20).
  The row carries what that action needs: the requirement, the control's
  anchor, and the tested-under note to pre-fill as the source.
- A CLI subcommand.

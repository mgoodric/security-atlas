# #1802 — the "audited under" report page with add-overlay-edge

Parent: #1735 (tenant overlay edges). Consumes #1801 (the report API and
CSV) and #1797 (the overlay POST).

detection_tier_actual: none
detection_tier_target: none

## What changed

- `/catalog/audited-under` ("Audited under", Library group in the
  sidebar): the report's rows grouped by control or by framework, each
  showing the unreached criterion, the control's anchor and fit, the fit
  note, the tested-under note and the candidate anchors. Filter by
  framework; Export CSV.
- An **Add overlay edge** row action: an inline form that posts the edge
  criterion -> the control's anchor through `POST /v1/mapping-overlays`.
- BFF routes: `GET /api/mapping-overlays/audited-under`,
  `GET /api/mapping-overlays/audited-under/export` (streamed CSV) and
  `POST /api/mapping-overlays` (allow-listed body). All `no-store`. No new
  write path on the platform.

## Decisions

**D1 — The page lives under `/catalog`.** The report is about how the
organisation's controls sit on the SCF catalog, next to the SCF catalog
page, and the breadcrumb already names `/catalog/*` "Catalog". Its title is
a `PAGE_TITLES` key, so it is in the sidebar (Library group) and the
route sweep.

**D2 — One read, client-side filter and grouping.** The page reads the
whole report once and filters by framework in the browser; the export link
carries the same `?framework` so the CSV matches what is shown. The BFF
still forwards `?framework` for callers that want the server filter.

**D3 — The form pre-fills the source and leaves the rationale empty.**
The ticket says the edge is "pre-filled with the control's tested-under
source; the user confirms the rationale". The source is the tested-under
note, or "Tested under <framework> <code>" when the reference has no
note. The rationale is the organisation's assertion about why the mapping
holds, so the page never writes it: the submit stays disabled until it is
non-empty.

**D4 — Relationship defaults to `intersects_with`, strength to 0.5, both
editable.** The overlay POST requires both. A tested-under reference says
the auditor accepted the control for the criterion, not that the two are
equal or nested, so the weakest STRM claim is the honest default; the user
can tighten it. 0.5 is the midpoint of the 0-1 range the store accepts.

**D5 — An inline form, not `ConfirmInline`.** `ConfirmInline` is the
confirm step for irreversible actions and renders its confirm button as
destructive. Adding an overlay edge is neither destructive nor
irreversible (it can be retired), and it needs four inputs, so the row
opens its own inline form with the same Cancel / Esc behaviour and a
primary button.

**D6 — The row closes only after the platform confirms.** On 201 the row
is removed from the cached report and the report is re-read, because the
same edge reaches every control on that anchor tested under that
criterion. A refusal (403 role, 409 duplicate, 400 invalid) shows as a
toast and inline beside the form (the toast convention: errors are never
only a toast), and the row stays.

**D7 — An unanchored control has no row action.** The edge needs an
anchor to point at; the row says "Anchor the control first" instead.
Re-anchoring onto a candidate is a control change, not an overlay edge,
and is out of scope here.

**D8 — Labels.** `anchor_fit` uses the controls list's `anchorFitLabel`
and relationship types use `strmStyle(...).label`, the existing display
helpers. The shared enum-label module (#2330) was not on `main` when this
landed; switching to it is that ticket's sweep.

## Not in this change

- A CLI subcommand, unresolvable tested-under references, a cap on the
  report and duplicate handling (#2324's remaining items).

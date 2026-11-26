# 2848 — Risk import web upload with the column-mapping step: decisions log

Issue: mgoodric/security-atlas#2848 (spec #2812). After the review page
(#2847) and value mapping (#2844).

## Detection-tier classification

- **detection_tier_actual:** none. No product bug surfaced.
- **detection_tier_target:** unit (the mapping helper and the preview parse
  are pure; the HTTP seam is pinned by the integration tier).

## Decisions

### D1 — headers are read by the platform, not the browser

The mapping step needs the detected headers and the field each maps to
before anything is staged. The alias table, the header-row search (title
and notes rows above the header) and the bounded XLSX reader all live in
`internal/risk/riskimport`; a browser-side parse would need an XLSX library
and a second copy of the aliases that drifts. So a new
`POST /v1/risks/imports/preview` takes the upload's own fields and returns
the header row, each column's field, the methodology's field list and the
first 5 rows. It reuses `newHeaderMatcher`, `findHeaderRow` and
`mapColumns`, applies the upload's caps and error statuses, is a write on
risks like the upload (a viewer is refused), and stores nothing.

### D2 — a file with no recognised header offers its fullest early row

When no row in the 5-row search window names both a title and a category
column, the upload fails with `ErrNoHeaderRow`. The preview instead offers
the window's row with the most filled cells (first of a tie) and says so
(`header_detected: false`), so the user can map it. Once title and category
are mapped explicitly, the upload's own header search finds that row
through the column map. A title row above the header has fewer filled
cells than the header, so it is passed over.

### D3 — `ignore_column` makes "Not imported" honest

An explicit `column_map` entry already wins over alias detection, but there
was no way to leave an alias-matched column unmapped: the alias would map
it again at upload. `ParseOptions.Ignore` (multipart `ignore_column`, on
both the upload and the preview) keeps a header unmapped whatever its
alias; a header both mapped and ignored is a 400. The web sends a
`column_map` entry for every mapped column and an `ignore_column` for every
other mappable one, so the staged batch maps exactly what the step showed,
including when a second alias column of a field would otherwise take it.

### D4 — blank and repeated headers are shown but not mappable

A column map names a column by its header text, so a blank header (kept as
"column N") or a header repeated from an earlier column cannot be named.
The preview marks them `mappable: false`; the step shows them with their
detected field and a disabled select.

### D5 — value mapping follows staging, and is skipped when nothing is unmapped

The ticket lists value mapping among the steps before the review page, but
category and theme values are mapped per batch (`PUT
/v1/risks/imports/{id}/values`, #2844), so the batch must exist first.
Staging writes no risk, so mapping values after it still precedes every
approval. The step is shown while any value maps to nothing; a batch whose
values all matched at staging goes straight to the review page. Creating a
category or theme is offered only to an approver (the platform refuses
anyone else with 403); a non-approver maps to existing ones.

### D6 — the shared component takes its fields as input

`components/import/column-mapping.tsx` and `lib/import/column-mapping.ts`
know nothing about risks: the caller passes `MappingField[]` and the
detected columns. The risk import's labels and field order live in
`lib/api/risk-import-upload.ts`. The helper's unit test uses a field list
that is not the risk import's. #2836 adopts both for the controls import.

### D7 — the Playwright flow is hermetic

Following the controls import review spec (slice 594), every BFF the flow
touches is route-mocked to the production wire shape. Header detection,
staging, the column map and ignore handling against real Postgres are
pinned by `TestRiskImport_PreviewMapsWithoutStaging`.

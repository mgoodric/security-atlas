# 2836 — Controls import adopts the column-mapping step: decisions log

Issue: mgoodric/security-atlas#2836 (follow-up to spec #2812, Q19). Builds
on the risk import's web upload (#2848, decisions in
`2848-risk-import-web-upload-decisions.md`).

## Detection-tier classification

- **detection_tier_actual:** none. No product bug surfaced. The first run
  of the preview integration test let a viewer preview and upload; the
  cause was the package's shared `testServer`, which attaches no authz
  engine. With the engine attached (as the risk import's harness does),
  both are 403.
- **detection_tier_target:** unit (the preview parse and the field mapping
  are pure; the HTTP seam and authz are pinned by the integration tier).

## Decisions

### D1 — the same preview endpoint shape as the risk import

`POST /v1/controls/imports/preview` returns the same JSON shape as
`/v1/risks/imports/preview` (header row, detected flag, columns with
field and mappable, fields with required, first 5 rows, data row count),
minus the methodology, which controls do not have. The web's shared
`ColumnMapping` component and `lib/import/column-mapping.ts` helper read
it unchanged. The preview reuses `newHeaderMatcher` and `findHeaderRow`,
so what it shows is what the upload will read, and it is a write on
controls at the authz layer like the upload.

### D2 — the preview takes no framework

The framework only resolves tested-under codes at staging. Header reading
does not depend on it, so the preview does not require it. The page still
asks for it on the first step, so the upload never needs a step back.

### D3 — "not detected" means fewer than two mapped headers

The controls import's header search already takes the first row of the
first five with two cells that map to a field. When none does, the
preview offers the row with the most filled cells (the risk import's
rule), and the user maps it by hand. Mapping id, title and description is
three explicit matches, so the upload then finds that row as the header.

### D4 — `ignore_column`, and a header mapped to two fields is a 400

Carried over from #2848 so "Not imported" on an alias-matched column is
expressible: the step sends every unmapped column as `ignore_column`. A
header that is both mapped and ignored is a 400. The column-map parser
also now refuses one header mapped to two fields, as the risk import
does. Before, one of the two won, chosen by Go map iteration order.

### D5 — the 200-column cap now covers CSV too (review fold-in)

The review asked that the preview enforce the upload's `MaxColumns` (200)
with the same 400. The preview reads through the upload's own readers, so
it does. But the cap was enforced only by the XLSX reader (`xlsxbound`).
A CSV had no column cap on either path. `readCSVRows` now refuses a row
past the cap, as `riskimport.readCSVRows` does, so upload and preview
return the same 400 for both formats. It is a behaviour change for a CSV
upload with a row past column 200, recorded in `changelog.d/2836-2.changed.md`.

# #1799 — overlay edges: bulk import and export (decisions log)

Builds on #1797 (create, list, retire). `POST /v1/mapping-overlays/import`
takes a CSV or YAML file of overlay edges; `GET /v1/mapping-overlays/export`
returns the tenant's edges as CSV. CLI: `atlas-cli mappings overlay import |
export`. No migration.

- detection_tier_actual: none
- detection_tier_target: none

No defect surfaced while building.

## D1 — one write path: every row goes through `create`

The body of `Store.Create` moved into an unexported `create(ctx, q, tenant,
edge, actor)`, which validates, resolves the requirement and the anchor
(release-aware), refuses a catalog duplicate, inserts and writes the audit
row. `Create` and each import row call it, so there is one set of rules and
one audit row per edge.

## D2 — one transaction, a savepoint per row

The import runs in one transaction. Each row runs under its own savepoint, so
a row refused by the database (the active-pair unique index, 23505) aborts
only that row and the rest of the file is still checked. Every row is tried,
so the report lists every bad row, not just the first. At the end:

| Outcome                                   | Commit? | Status |
| ----------------------------------------- | ------- | ------ |
| no bad rows                               | yes     | 201    |
| bad rows, no `partial`                    | no      | 422    |
| bad rows, `partial=true`                  | yes     | 201    |
| no valid rows (with or without `partial`) | no      | 422    |

A repeated row inside one file is refused as `overlay_edge_exists`, because
the earlier row was inserted under a released savepoint.

## D3 — reasons are fixed text, never cell content

`mappingoverlay.Classify` maps every store refusal to a stable code and a
fixed reason. Row reports carry row number, file line, code and that reason.
They never repeat a cell back. The single-create error body is unchanged
(it still carries `err.Error()`). It now reads its code from `Classify`
too, so the two surfaces cannot drift apart. A file-level refusal names one
of our column names at most. An unknown column is reported as "not one of
<the allowed list>" and its own name is never echoed.

## D4 — file shape

- Columns: `framework, framework_version, requirement_code, scf_release,
anchor_code, relationship_type, strength, rationale, source_ref`. The
  ticket's list has no framework slug, but a requirement code names nothing
  without one, so `framework` is included. `framework_version` and
  `scf_release` may be blank or left out: blank means the current version
  or release, as on single create.
- A `tenant` or `tenant_id` column (any case) refuses the whole file: the
  tenant comes from the credential.
- An unknown or repeated column refuses the whole file. Silently ignoring a
  column would hide a typo such as `strenght`.
- The export carries more columns than the import accepts (id, status,
  author, dates), so an export does not re-import as-is. Re-importing a
  retired edge as active would be wrong anyway.
- YAML is one document holding a list of mappings with the same keys. A
  second document refuses the file rather than being dropped. A non-scalar
  value is a row error, and `~` counts as blank. Aliases are never
  expanded: an aliased row or value is a row error, and a `<<` merge key
  is an unknown column.
- Cells are trimmed. A UTF-8 BOM on the CSV header is dropped.
- `strength` must parse as a finite float. `NaN` would pass the `0 <= s <= 1`
  check (every comparison with NaN is false), so the parser refuses it
  before `Validate` runs.

## D5 — bounds

- Body: 4 MiB via `http.MaxBytesReader` (413), the same cap as the
  applicability import. The CLI refuses a larger file before sending it.
- Rows: 5,000 (`MaxImportRows`), a file-level 400.
- Export: 100,000 rows (`internal/export` leaves the cap to the caller). A
  tenant's overlay edges are far fewer than that.

## D6 — format selection

`?format=csv|yaml` wins; otherwise the Content-Type (`text/csv`,
`application/yaml`, `application/x-yaml`, `text/yaml`, `text/x-yaml`).
Anything else is 415. The CLI infers the format from the extension (`.csv`,
`.yaml`, `.yml`), `--format` overrides it, and it always sends `?format=`.

## D7 — export goes through the shared CSV encoder

`export.NewCSVExporter()` neutralises formula-leading cells (OWASP), which
matters because rationale and source are organisation-entered text. The
filename comes from `export.BuildFilename`, so it carries no tenant name.
The list filters (`framework`, `requirement`, `anchor`, `status`) apply, and
with no `status` both active and retired edges are exported. Reading the
export needs a tenant credential, as list does. Importing needs admin or
grc_engineer, as create does. Both paths stay under the `mapping-overlays`
OPA resource.

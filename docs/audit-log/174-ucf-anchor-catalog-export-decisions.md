# Slice 174 — UCF anchor catalog export decisions

Slice 174 (`docs/issues/174-ucf-anchor-catalog-export.md`) ships
`GET /v1/anchors/export?format=<csv|json|xlsx>` — the SCF anchor
catalog (anchor metadata + framework satisfactions inline) as a
downloadable artifact in three native projections.

This slice's D1 is **pre-locked by the maintainer 2026-05-20** — all
three formats ship, each with its native projection. The engineer
records the impl-time refinements below.

## D1 (maintainer-locked) — Three formats, three projections

**Decision (locked):**

- `format=json` → **nested JSON**. One object per anchor; framework
  satisfactions live in a `framework_satisfactions` array field on the
  anchor object.
- `format=xlsx` → **two-sheet workbook**. Sheet 1 = `Anchors`
  (one row per anchor, anchor metadata only); Sheet 2 = `Edges`
  (one row per anchor → framework requirement edge, pivot-table
  friendly).
- `format=csv` → **flat-nested fallback**. One row per anchor;
  framework satisfactions serialized as a JSON-string column. Single
  file; grep / awk friendly.

**Maintainer rationale (recorded in the slice spec):** preserves the
"right tool per consumer" property — auditors get the XLSX in the
format they already open, programmers get the natural graph shape in
JSON, the command-line crowd gets a single CSV file. The cost of
supporting all three is small because the slice 135 export library
already handles the wire-format dispatch; the only new code is the
two-sheet writer and the nested JSON projection. Picking one would
have made each non-native consumer's workflow worse without any
implementation saving worth the friction.

**Why the slice 137 D1 rejection of (B) and (C) does NOT apply here:**

- Slice 137 D1 rejected nested (B) for tenant CONTROLS because the
  primary entity in that slice was the control, not the anchor —
  nesting at the anchor level inverted the model. Here the anchor IS
  the row, so the nesting is congruent with the entity.
- Slice 137 D1 rejected two-sheet XLSX (C) on "format is a
  serialization detail, not a data shape" grounds — the CSV consumer
  would have lost the edges sheet. Here we ship all three formats
  with their native projections; the CSV consumer DOES get the
  edges, just JSON-stringified into a column. The data shape is
  consistent across formats; the serialization differs.
- The SCF catalog is public-domain and tenant-agnostic, so the
  duplication-cost argument that drove slice 137 to flat doesn't
  apply: the global catalog is small (~1,400 anchors × ~3–8 edges
  per anchor = ~10K edges total), well within the streaming-memory
  budget even with anchor metadata fanned inline.

## D2 — Meta-audit action value (`anchors_export`)

**Decision:** New action value `anchors_export` added to the
`me_audit_log.action` CHECK constraint via migration
`20260520010000_anchors_export_meta_audit.sql`.

**Why plural:** matches the slice 137 / 138 / 139 plural convention
(`controls_export`, `evidence_export`, `vendors_export`). Slice 136's
singular `risk_export` (one register per tenant) remains the only
outlier; anchors are many-per-catalog, so plural is correct.

**Why distinct:** a forensic query of the form
`WHERE action = 'anchors_export'` cleanly enumerates SCF catalog
extraction events distinct from controls-export and other entity
extracts. Different downstream consumer (auditor-handoff bundles,
vendor-due-diligence packs) than the slice 137 controls export.

## D3 — Row cap (50,000) + streaming-memory budget (200 MB)

**Decision:** Row cap is **50,000 anchors** (well above the realistic
~1,400 anchors in a current SCF release; 35x headroom for future SCF
growth). Streaming-memory test asserts heap delta stays under
**200 MB** across all three encoders.

**Why 50K not 100K (slice 145 default):** the SCF catalog is a
bounded set — even doubling every SCF release for the next decade
would not reach 50K. The cap is a defensive ceiling, not an
operational target. Setting it at 50K rather than the slice 145
default of 100K signals to operators that anchor exports are
expected to be much smaller than other ledger-entity exports.

**Why 200 MB (matches slice 137's lifted budget):** the anchors
export fans framework-satisfaction edges inline (in JSON +
two-sheet XLSX), which raises per-row allocation above a flat row.
Matching slice 137's 200 MB (4x the slice 135 baseline of 50 MB)
gives the same posture without re-litigating the budget.

## D4 — Role gate (any authenticated user)

**Decision:** No defense-in-depth role gate. The endpoint is admitted
for any authenticated user (same admit set as the existing
`/v1/anchors` read endpoint), gated only by the upstream OPA
middleware via `defaults.rego`'s `catalog_resources` allow rule.

**Why:** the SCF anchor catalog is global, public-domain, and
already exposed without a role gate at `/v1/anchors`. The export is
a serialization of the same read; admitting any narrower role set on
the export would silently restrict access relative to the underlying
read — a slice 135 P0-A9 violation (admit-set parity). The
`internal/authz/slice174_test.go` admit-set parity test pins this
contract at the rego layer.

**Why no `controls/program-read`-style gate (slice 137 pattern):**
slice 137 added that gate because the export carries
`applicability_expr` (tenant-private DSL). Slice 174 explicitly
excludes any tenant-private field (see P0-A-174-1); the export
contains only public SCF + STRM crosswalk metadata, so the same
gate would be theatre.

## D5 — Column / shape projection per format

**Anchor metadata (every format):** `id`, `scf_id`, `family`,
`title`, `description`, `framework_version_id`, `framework_version`,
`framework_slug`, `created_at`, `updated_at`.

**Framework satisfaction per edge:** `edge_id`,
`framework_requirement_id`, `framework_requirement_code`,
`framework_slug`, `framework_version`, `relationship_type`,
`strength`, `source_attribution`, `rationale`.

**JSON shape:** anchor object with a `framework_satisfactions: []`
array field carrying one object per edge. Native graph
representation; programmers consume it directly.

**XLSX shape (two-sheet):**

- Sheet 1 `Anchors`: anchor metadata columns only (10 columns).
- Sheet 2 `Edges`: one row per edge; the leading column is
  `anchor_id` (the join key into Sheet 1) and `anchor_scf_id`
  (human-readable). The remaining columns are the edge metadata.
  Auditors join the two sheets with `VLOOKUP(anchor_id, Anchors!A:J, 2)`
  or its `XLOOKUP` equivalent.

**CSV shape (flat-nested):** anchor metadata columns + a
`framework_satisfactions` column whose cell is a JSON-stringified
array of satisfaction objects (same shape as the JSON projection).
The CSV-injection sanitizer is applied; the JSON cell starts with
`[` which is not a formula introducer.

**`applicability_expr` is NOT exported.** P0-A-174-1 forbids
tenant-private data; the catalog export contains only public-domain
SCF + STRM crosswalk metadata. Operators who need controls with
applicability_expr use the slice 137 controls export instead.

## D6 — Two-sheet XLSX writer lives in the handler package, not the library

**Decision:** the two-sheet XLSX writer is implemented in
`internal/api/anchors/export.go` (or a sibling file within the
`anchors` package) using `archive/zip` + `encoding/xml` directly,
NOT by extending the slice 135 `internal/export/` library's
single-sheet writer.

**Why:** the slice spec is explicit — "Reuse slice 135's
`internal/export/` library — consume only; do not duplicate." The
generic library exposes a single-sheet `XLSXExporter`; extending it
to multi-sheet would require either a new exporter interface
(library API churn) or a multi-sheet method on the existing
`Exporter` (incoherent — most exports are single-sheet). The local
writer is ~80 LOC, statically guaranteed (by construction) to emit
no charts / no named ranges / no VBA, and has zero external
dependencies. The library's CSV + JSON exporters are still
consumed for the flat-CSV fallback case.

**Why this satisfies P0-A-174-2:** the local writer literally cannot
emit a chart object, a named range, or VBA — the code paths for
those zip members do not exist. Test pins the exact zip-member list
(7 files: `[Content_Types].xml`, `_rels/.rels`, `xl/workbook.xml`,
`xl/_rels/workbook.xml.rels`, `xl/worksheets/sheet1.xml`,
`xl/worksheets/sheet2.xml`, plus no `xl/charts/` / no
`xl/definedNames` block / no `xl/vbaProject.bin`).

## D7 — Cross-tenant isolation test (semantic)

**Decision:** because the catalog is global, the cross-tenant isolation
test asserts the export body is BIT-FOR-BIT IDENTICAL between two
distinct tenants (modulo the timestamp in the filename which the
integration test does not check). This is the slice 174 AC-4 contract.

**Why:** the slice 135 cross-tenant isolation invariant is "tenant A
cannot see tenant B's data"; for a global catalog this is the
trivially-true direction. The interesting test is the dual:
"the same export from tenant A and tenant B must match" — proves the
endpoint does NOT accidentally filter by tenant context, which would
fragment what should be a single source of truth.

## D8 — Sheet ordering and column ordering

**Sheet order:** Anchors first (Sheet 1), Edges second (Sheet 2). Locks
the visual reading order an auditor expects (overview first, detail
second). The order is reflected in the rels file and the workbook XML.

**Column ordering within Anchors sheet:** identity → topology →
catalog provenance → audit. Identity (`id`, `scf_id`, `family`),
narrative (`title`, `description`), topology (`framework_version_id`,
`framework_version`, `framework_slug`), audit (`created_at`,
`updated_at`).

**Column ordering within Edges sheet:** join keys first
(`anchor_id`, `anchor_scf_id`) — so VLOOKUP / pivot-table workflows
land on column A — then the edge metadata
(`framework_requirement_id`, `framework_requirement_code`,
`framework_slug`, `framework_version`), then the STRM payload
(`relationship_type`, `strength`, `source_attribution`,
`rationale`).

## D9 — sqlc regeneration scope

**Decision:** two new queries (`ListAllSCFAnchorsForExport`,
`ListAllFwToScfEdgesForExport`) land in
`internal/db/queries/scf_anchors.sql`; they are added to the sqlc-
generated code at `internal/db/dbx/scf_anchors.sql.go`. The sqlc
regeneration is bounded to the new queries — no other dbx file
churns. Confirmed via `just sqlc-gen` diff in the slice PR.

**Why:** the slice 159 toolchain pin (sqlc v1.31.1 in `justfile`)
guarantees deterministic codegen; any unexpected dbx churn flags a
regeneration bug, not a slice 174 design issue.

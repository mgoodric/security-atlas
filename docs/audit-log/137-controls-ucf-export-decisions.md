# Slice 137 — Controls UCF graph data-export decisions

Slice 137 (`docs/issues/137-controls-ucf-export.md`) wires the slice 135
data-export library + slice 145 concurrency cap into the controls / UCF
surface.

The slice is typed **JUDGMENT** because the UCF spans a graph (anchors ×
framework satisfactions × applicability) rather than a flat record set, so
the column-set / projection shape is the load-bearing build-time call (D1).
The remaining decisions are mostly the standard mechanical wire-up — same
pattern as slices 136 / 139, with two slice-specific differences (row cap
lifted to 500K per the slice doc, 200 MB streaming-memory budget rather
than the 50 MB the library's unit suite asserts).

## D1 — Graph projection shape

**Decision:** **Option A — flat, one row per active tenant control.**
Each row carries enough graph topology (`scf_anchor_id` UUID + the public
`scf_id` code) for downstream tools to reconstruct the
`control → SCF anchor → fw_to_scf_edge → framework_requirement` join
against the public SCF catalog. The export does NOT embed
framework-satisfaction edges or anchor metadata inline.

**Why:**

- **Per the slice 137 prompt note: flat is the v1 default for three concrete
  reasons.** CSV / JSON / XLSX all support the shape uniformly without
  format-specific divergence; cross-tenant isolation testing is a
  per-row property (no nested objects to walk); downstream tools can
  reconstruct topology client-side via the join keys we DO export.
- **The SCF catalog is public and cacheable.** A downstream consumer
  that wants the full graph can ingest the SCF anchors + STRM edges
  once (they're tenant-agnostic global state) and join on the
  `scf_anchor_id` column. Inlining anchor metadata on every control
  row would inflate every export by ~1 KB/row × 500K rows = 500 MB of
  duplicated catalog text — fighting the streaming-memory budget for
  no operator benefit.
- **The framework-satisfaction edges (`fw_to_scf_edges`) are
  tenant-agnostic too.** Joining them inline would be the same
  duplication problem at higher fan-out (one anchor can satisfy 10+
  framework requirements). Operators who need the full graph want the
  SCF anchor catalog as a separate export (future slice — see "spillovers"
  below).
- **The slice 136 risk-export pattern is the closest precedent.** Risks
  are also a "flat list with graph-pointer columns" shape (org_unit_id
  is a graph pointer; we export the UUID and leave the hierarchy walk
  to the consumer). The slice 137 export inherits the same posture: we
  export the row, we export the foreign-key columns, we don't fan
  edges inline.

**Rejected:**

- **Option B — nested (one row per anchor + `requirements: [...]`).**
  Two structural problems: (1) CSV cannot express it without lossy
  flattening — a column like `framework_satisfactions_json` carrying
  a stringified JSON array is half of (B) without its benefit; (2) the
  primary entity in v1 is the tenant CONTROL, not the SCF anchor —
  nesting at the anchor level inverts the model. The UCF graph belongs
  in a separate "UCF anchor catalog export" slice (queued as a
  spillover; see below) where the anchor IS the row.
- **Option C — two-sheet XLSX (controls sheet + edges sheet).**
  Asymmetric across formats — CSV / JSON wouldn't have the edges sheet
  at all (CSV is single-sheet by definition), so the XLSX consumer
  gets the graph but the CSV consumer doesn't. This violates the
  "format is a serialization detail, not a data shape" invariant. Plus
  the slice 135 D1 handcrafted XLSX writer is single-sheet by design;
  going two-sheet would require either expanding that writer (extra
  surface to maintain) or pulling in an XLSX dep (rejected by slice
  135 D1). The cost-to-benefit is wrong at the 137 row count.

**Provisional follow-on (spillover):** A future "UCF anchor catalog
export" slice can ship Option B (nested anchor + framework
satisfactions) when there's operator demand. Filed as slice 174 below.

## D2 — Canonical column set + ordering

**Decision:** Fifteen columns in the following order:

```
id, bundle_id, version, title, control_family,
scf_id, scf_anchor_id,
implementation_type, owner_role, lifecycle_state,
applicability_expr,
freshness_class, bundle_manifest_hash,
created_at, updated_at
```

**Why:**

- **Identity → topology → posture → operations → audit.** The ordering
  mirrors slice 136's "identify → classify → ownership → posture →
  audit" pattern, adapted to controls: identify the row (id,
  bundle_id, version, title, control_family); pin the graph location
  (scf_id, scf_anchor_id); see implementation posture
  (implementation_type, owner_role, lifecycle_state); see the
  tenant-private applicability (applicability_expr); see operational
  metadata (freshness_class, bundle_manifest_hash); audit (created_at,
  updated_at).
- **`updated_at` is included** even though the `ListActiveControls`
  query doesn't return it today. The slice 137 export adds an internal
  `ListActiveControlsForExport` query that selects `updated_at`
  alongside the other columns. The cost is one new sqlc query; the
  benefit is consumers can compute "rows changed since last export"
  without a separate API call.
- **`bundle_manifest_hash` is included** because it's the integrity
  marker for the bundle that defines the control — operators
  reconciling exports against a future control-bundle archive (slice
  069+ territory) need this column to verify bytes-on-disk match
  bytes-as-exported.
- **`applicability_expr` is exported as raw text** (the
  slice 017 DSL, e.g. `BU=Eng AND env=prod`). The slice 137 threat
  model calls out that applicability_expr is the only tenant-private
  cell on the row; RLS enforces tenant isolation on the underlying
  read so the export only contains the caller's tenant's expressions.

**Rejected:**

- **Excluding `applicability_expr` to be conservative.** Would gut the
  primary operator workflow (an export consumer cataloguing scope
  cells across the program). The whole point of bundling this column
  is that operators NEED the tenant-private expression text; the
  protection is RLS, not column omission.
- **Including `superseded_by` / `superseded_at`.** The export filters
  to `superseded_by IS NULL` (active rows only) — the column would be
  always-NULL noise. A future "control bundle history export" slice
  can carry these columns when there's demand.
- **Fanning the SCF anchor metadata inline** (anchor title, anchor
  description, anchor family). Duplicates per-row data that's
  trivially joinable via `scf_anchor_id` against the public SCF
  catalog. See D1 rejected (B) for the cost analysis.
- **Including an inline framework-satisfaction count column** like
  `linked_framework_requirement_count`. Forces a second query for
  any consumer that wants the actual ids; adds friction without
  buying anything. Same posture slice 136 took on
  `linked_control_count`.

## D3 — Row cap (500,000) + streaming-memory budget (200 MB)

**Decision:** Default and maximum row cap is **500,000** active
controls (5× the slice 135 / 136 default of 50–100K). Streaming-memory
test asserts heap delta stays under **200 MB** across all three
encoders (4× slice 135's 50 MB unit-suite budget).

**Why:**

- **The slice doc lifts the cap explicitly.** Slice 137 narrative §1
  calls out that the UCF graph spans ~1,400 SCF anchors plus the
  per-tenant control bundles plus the `applicability_expr` text;
  realistic large-tenant control sets are O(10³–10⁴), well below
  500K, but the lifted cap leaves headroom for the largest tenants
  (multi-product orgs with O(10²) bundle modules × O(10²)
  framework-satisfactions × O(10²) scope cells). Setting the cap at
  the projected ceiling × ~5 buys headroom against future scope
  expansion (PCI / FedRAMP modules add O(10²) controls each).
- **200 MB is the streaming-memory contract for a 500K-row export.**
  The slice 135 export library's per-row working set is bounded
  (encoders are pull-style; per-row allocation is O(len(header) ×
  cell bytes) ~= a few KB). 500K rows × ~400 bytes/row encoded =
  ~200 MB of streamed bytes total — but the LIVE heap at any moment
  should sit at the buffered-writer high-water mark plus the current
  row's working buffer, not the cumulative total. The 200 MB budget
  is the live-heap ceiling, NOT the total-bytes-emitted figure.
- **The slice 137 P0-A-UCF-3 anti-criterion is the merge gate.** The
  integration test that asserts this is the streaming-memory
  load-bearing evidence. The test generates 500K synthetic control
  rows in-process, runs all three encoders through `discardWriter`,
  and asserts `runtime.MemStats.HeapAlloc` delta stays under
  200 MB after `runtime.GC()`.

**Rejected:**

- **A higher cap (1M+).** No operator workflow justifies it at v1; the
  500K ceiling already gives 5x headroom. A future cap lift can ship
  if a real tenant ever hits the ceiling.
- **A per-tenant configurable cap.** YAGNI at v1; the global cap is
  the simplest knob to reason about. If a specific tenant ever needs
  more, a future PR can add a tenant-scoped override.

## D4 — Cross-package vs in-package endpoint placement

**Decision:** **In-package** — the export endpoint lives at
`internal/api/controls/export.go`, alongside the slice-009 upload
handler and slice-151 list handler.

**Why:**

- **Slice 136 / 139 / 145 set the precedent.** Each per-entity export
  lives in the same package as the entity's other handlers
  (`internal/api/risks/export.go`, `internal/api/adminvendors/export.go`,
  `internal/api/adminauditperiods/export.go`). Keeping slice 137 in
  `internal/api/controls/` matches the established convention.
- **The dbx query that backs the export is controls-specific.** A new
  `ListActiveControlsForExport` sqlc query joins on `superseded_by IS
NULL` and selects the slice 137 canonical column projection.
  Co-locating it with the other controls queries keeps the package
  cohesion intact.
- **No need for a sibling `internal/api/controlsexport/` package.**
  Would fragment the controls surface without buying anything; the
  slice 145 concurrency limiter is already a shared singleton from
  `internal/export/`, so there's no cross-package coupling to break.

**Rejected:** No alternative seriously considered — the precedent is
load-bearing and there's no design pressure to deviate.

## D5 — Defensive DELETE in down migration

**Decision:** The down migration `20260520000000_controls_export_meta_audit.down.sql`
**DOES** include `DELETE FROM me_audit_log WHERE action =
'controls_export'` before the CHECK-constraint swap, even though
`internal/api/controls/` is NOT in the current CI integration-test
list (`.github/workflows/ci.yml` line 289–310).

**Why:**

- **Slice 136's migration round-trip failed THREE times in a row.**
  The third failure was because `internal/api/risks/` IS in the CI
  integration-test list — integration tests INSERT `risk_export` rows
  into `me_audit_log`, and the down migration's CHECK-constraint
  re-add then fails against the newly-disallowed action value. The
  slice 137 prompt explicitly directs: "even if NOT in the list, ADD
  THE DELETE ANYWAY as defensive programming — future additions to
  the integration-test list could surface this bug."
- **`internal/control/` IS in the integration-test list today** (CI
  line 310). The slice 137 handler doesn't run in that package's
  tests, but a future test refactor (collapsing `internal/api/controls`
  - `internal/control` into a single integration suite) could surface
    the same bug. Defensive DELETE is cheap insurance.
- **The DELETE is prod-destructive** in a real rollback context (a
  prod operator rolling back loses forensic evidence). The migration
  comment + CHANGELOG entry both flag this; operators who want the
  rows archived must do so out-of-band before applying the down.

**Rejected:** Omitting the DELETE on the grounds that controls/
isn't in CI today. The cost of the line is one SQL statement; the
cost of regression-finding when it bites is hours of orchestrator
rebase work. Accept the cheap insurance.

## D6 — Meta-audit action name (`controls_export`)

**Decision:** Meta-audit `action` value is `controls_export` (plural
controls, singular export). Matches the slice 139 plural convention
(`audit_periods_export` + `vendors_export`).

**Why:**

- **The slice 137 prompt directs the spelling explicitly** —
  pluralization mistakes have already cost slice 136 a CI round
  (`vendor_export` vs `vendors_export`). The slice 137 prompt notes
  the precedent action strings:
  `20260518000010_audit_log_export.sql → audit_log_export`;
  `20260519000000_audit_periods_vendors_export.sql →
audit_periods_export + vendors_export`;
  `20260519000010_risk_export_meta_audit.sql → risk_export`.
- **The risk-register slice diverged with the singular `risk_export`**
  (one risk = the register as a whole). Slice 137 exports the
  catalogue OF controls, plural — `controls_export` aligns with
  slices 139 + the audit-periods plural convention.
- **Forensic distinguishability.** A query like `WHERE action =
'controls_export'` cleanly enumerates UCF-graph extractions
  separately from other export actions, so a security operator
  triaging "who pulled the control catalog this quarter" gets a
  precise filter.

## D7 — `ControlsExportHandler` constructor surface

**Decision:** Mirror the slice 136 risk handler exactly:
`NewExportHandler(pool *pgxpool.Pool) → *ExportHandler` with optional
`WithSource(exportSource)` + `WithLimiter(*export.Limiter)` accessors
for tests.

**Why:**

- **One precedent, copied verbatim, narrows the surface.** Every
  slice that follows this pattern should look like the prior slice
  with only the entity-specific bits swapped. Slice 138 (ledger
  entities) and slice 174+ (anchor-catalog export) will lean on the
  same shape.
- **The optional limiter override** is the slice 145 hook the
  integration tests use to pin a small, deterministic cap. Production
  callers leave it nil; the handler resolves `export.DefaultLimiter()`
  lazily on every request.

## D8 — UI button placement (Export CSV / JSON / XLSX as link group)

**Decision:** Replace the existing disabled `Export CSV` placeholder
button on `/controls` with a three-link group (`Export CSV` / `Export
JSON` / `Export XLSX`), mirroring slice 136's risk-list pattern. Each
link is `<a download>` to honour the backend's Content-Disposition.

**Why:**

- **The slice 098 controls page already has a `<Button disabled>Export
CSV</Button>`** sitting in the action bar — it's an explicit
  placeholder for this slice. The minimum-friction replacement is
  literally to swap the disabled button for the three real links.
- **Slice 136 set the link-group precedent** at the same level of UI
  surface investment. A dropdown would be more space-efficient but
  adds a stateful interaction (open/close) that the slice 145 server
  middleware can't help with — the three buttons are stateless and
  match the operator's "click and a file downloads" expectation.

## Spillovers filed

Two follow-on slices filed at the next available slots (verified via
`ls docs/issues/[0-9]*.md | sort | tail -1` — current ceiling is
slice 173, next available is **174 + 175**):

- **Slice 174** — UCF anchor catalog export. Ships the rejected
  Option B from D1: a `GET /v1/anchors/export` endpoint that exports
  the SCF anchor catalog (anchor + framework satisfactions + STRM
  edges) as a nested-JSON / two-sheet-XLSX export when there's
  operator demand. Provisional `not-ready`; gate is a customer
  request surfacing the need.
- **Slice 175** — control-bundle history export. Ships `superseded_by`
  / `superseded_at` columns + the multi-version row set across
  bundle lineage. Useful for "what did this control look like at
  audit-period freeze T?" but distinct from this slice (which is
  active-only). Provisional `not-ready`; gate is the slice-028
  audit-period attestation workflow shipping a sample-pull surface
  that needs lineage.

Both are speculative and will only flip to `ready` when a real
operator demand surfaces.

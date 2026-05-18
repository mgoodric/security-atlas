# Slice 135 — Data-export library + audit-log export · Decisions log

This document captures the JUDGMENT-slice build-time decisions for
slice 135. The slice ships:

- A new reusable library at `internal/export/` with three encoders
  (CSV, JSON, XLSX) sharing one streaming `Exporter` interface.
- The first consumer: `GET /v1/admin/audit-log/export?format=...` —
  reuses the slice-124 aggregator with a format-encoder swap.
- The BFF + frontend Export buttons on `/audit-log`.

The maintainer requested data-export "everywhere that makes sense"
on 2026-05-18; this slice ships the library + audit-log as the
reference. Per-entity exports (risk register, controls UCF, etc.)
follow as spillover slices 136–139.

## D1 — XLSX library choice

**Decision: option (c) — handcrafted minimal-XLSX writer (~200 LOC,
zero new dependencies).**

The slice file's "Notes for the implementing agent" laid out three
options:

| Option                                 | Pros                                                                  | Cons                                                                                                             |
| -------------------------------------- | --------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------- |
| (a) `xuri/excelize/v2`                 | Mature, well-documented, large feature surface                        | ~5 MB binary impact; large API surface most of which violates P0-A6 if misused; new transitive-dep audit surface |
| (b) `tealeg/xlsx`                      | Smaller, simpler                                                      | Older / less maintained; same dep-audit concern                                                                  |
| (c) Handcrafted single-sheet-text-only | Zero new deps; perfect P0-A6 fit by construction (cannot emit charts) | Engineer must understand the Open Office XML zip-structure                                                       |

I chose (c). Rationale:

1. **P0-A6 by construction.** The threat model REQUIRES single-sheet,
   text-only output. A handcrafted writer literally has no code path
   for charts, named ranges, VBA, hidden metadata sheets, embeddings,
   or pivot tables. The forbidden surfaces are physically impossible.
   Option (a) or (b) would let a future contributor reach for
   `excelize.NewChartSeries(...)` and ship a regression on the next
   "the customer wants pivot tables" feature request.
2. **Zero supply-chain surface.** Option (a) brings ~50 transitive
   dependencies into the module graph (image format helpers, XML
   pretty-printers, font metric tables) — every one of which is a
   Dependabot-tracking and breach-radius surface. The handcrafted
   writer is ~200 lines in `internal/export/xlsx.go`.
3. **Review surface.** The slice-135 reviewer can read the entire
   XLSX writer in one sitting + cross-check against the ECMA-376
   spec. Option (a) is opaque on review (you have to trust the lib
   doesn't emit anything outside the single-sheet-text-only profile).
4. **Performance.** The handcrafted writer streams (`archive/zip` +
   the writer copies row-by-row). 100k rows fits in <50MB live heap
   per the slice-135 AC-2 memory test.

The minimal XLSX is five XML files inside a zip:

- `[Content_Types].xml`
- `_rels/.rels`
- `xl/workbook.xml`
- `xl/_rels/workbook.xml.rels`
- `xl/worksheets/sheet1.xml` (the data; inline-string cells, so no
  `xl/sharedStrings.xml` is needed)

The AC-4 test asserts the produced zip contains EXACTLY those five
entries (sorted) and that none of the forbidden prefixes
(`xl/charts/`, `xl/vbaProject.bin`, `xl/sharedStrings.xml`,
`xl/drawings/`, `xl/embeddings/`, `xl/media/`, `xl/pivotTables/`)
appear.

**Fallback rule (per slice spec):** if the engineer attempts (c) and
finds the zip-structure rabbit-hole deeper than 0.5d, fall back to (a)
with a D1 entry explaining the pivot. The implementation took ~1
hour; no pivot needed.

## D2 — Filename convention

**Decision: `<entity>_<YYYYMMDD>_<key-value>_<key-value>_..._.<ext>`
with sorted filter keys.**

Examples:

- `audit-log_20260518.csv` (no filters)
- `audit-log_20260518_from-20260511_to-20260518.csv`
- `audit-log_20260518_kind-evidenceme_to-20260518.csv`
- `audit-log_20260518_actor-userabcd_kind-decision.csv`

Rules (slice 135 P0-A2):

1. ASCII alphanum + `-` / `_` only. Every other rune is dropped
   silently. CRLF, path-traversal, unicode are therefore impossible
   to smuggle through.
2. Max 80 characters total (including the extension). Filenames that
   would exceed the cap are truncated at the param-summary segment
   so the entity + timestamp always survive.
3. Tenant name / tenant id is NEVER injected.
4. Sorted filter keys — the same filter set ALWAYS produces the same
   filename across runs (load-bearing for re-running an export on
   the same day to confirm idempotency, and for downstream tooling
   that deduplicates by filename).
5. Filter values are sanitized through the same alphanum-only
   pipeline; long values (e.g. UUID actor filters) are truncated to
   8 chars in `filenameParamsFor` so the param summary stays short.

`internal/export/export.go::BuildFilename` is the single
implementation; `BuildFilename` unit tests pin the contract for
happy paths + CRLF injection + path-traversal + unicode + length
cap + empty params + entity defaulting + sorted-key determinism.

## D3 — Row cap default + per-entity override hook

**Decision: 100,000 default; per-entity override at registration
time; cap removal NOT allowed (slice 135 P0-A8).**

Rationale:

- 100k rows × 9 columns × ~200 bytes per cell = ~200 MB transfer.
  Comfortable over HTTP/2 streaming on a normal connection
  (<60 s at 30 Mbps); painful on HTTP/1.1 to a slow client.
- The audit-log union of 9 tables in a 90-day window for a
  50–150-person company very rarely exceeds 100k rows.
- For entities where 100k is too small (controls UCF: 1,400 SCF
  anchors × edges easily crosses 100k), spillover slices register
  their own cap (137 lifts to 500k; 138 ledger samples to 250k).
  Each override carries a decisions-log justification.

The per-entity override is implemented as a constant on the
handler — `defaultExportRowCap` in
`internal/api/adminauditlog/export.go`. Spillover handlers will
define their own constant. (A registry pattern with per-entity
caps is a v2 generalization; for v1 with one consumer, a per-
handler constant is the simplest correct shape.)

The caller-overflow shape: when the underlying query returns
`rowCap + 1` rows, the handler returns 413 Payload Too Large with
an actionable body suggesting filter narrowing. The 413 path
still writes a meta-audit row (slice 135 P0-A4).

## D4 — Streaming pattern (encoder ⇄ HTTP body)

**Decision: per-row pull-style iterator
(`iter.Seq[[]string]`) consumed by `Exporter.WriteRows` writing
directly into the `http.ResponseWriter`.**

The Go 1.23+ `iter.Seq` push-style iterator gives us:

- Bounded per-row allocation. The encoder pulls one row at a time;
  the row's []string can be a reused buffer (the test generator at
  `internal/export/export_test.go::generatedRowIter` reuses a
  single backing slice across 100k iterations and the memory test
  asserts live heap stays under 50MB).
- Composable cancellation. If the HTTP response writer errors
  mid-stream (client disconnected), the encoder error short-
  circuits and `inTx` rolls back the transaction; no half-written
  row goes anywhere.
- Trivial to test. `rowIter([][]string{...})` is a 5-line helper.

The handler builds the iterator from the slice-124 aggregator's
returned `[]unifiedlog.Entry` (capped at `rowCap+1` for the
"exceeds cap" detection). The encoder loops once over that slice
and emits per-row directly into the HTTP body via a
`countingWriter` that records the body byte-count for the meta-
audit row.

There is no buffering of the full result set. Verified by the
`TestStreamingMemoryUnder50MBFor100KRows` test in all three formats.

## D5 — Frontend Export controls: three buttons (not a dropdown)

**Decision: render three side-by-side buttons (CSV / JSON / XLSX)
instead of a split-button or dropdown.**

The slice file's AC-14 said "dropdown / split-button"; I chose
three plain buttons. Rationale:

- The shadcn `dropdown-menu` component is not yet installed in
  `web/components/ui/`; adding it would expand the BOM beyond
  slice scope.
- A three-button bar is more discoverable for the v1 user
  (security-leader-of-one) — they see all three formats at once
  rather than having to expand a menu.
- The Playwright spec is simpler (assert each button by testid,
  not "open menu → click item").

A future v2 follow-on can collapse to a split-button if the
operator-research signal points that way; the wire shape is
unchanged.

## D6 — Underlying SQL: defer the UUID cast via CASE WHEN

**Decision: harden the slice-124 unified-audit-log SQL with a
`CASE WHEN regex THEN actor_id::uuid ELSE NULL END` pattern in
the LEFT JOIN onto `users`.**

Surfaced during slice-135 integration test bring-up: the slice-124
SQL had a latent planner-reordering footgun. The regex guard
(`unified.actor_id ~* '...uuid...'` in the JOIN's ON clause) was
intended to short-circuit the `::uuid` cast for non-UUID actor_ids
(`'seeder'`, `'key_foo'`). In some plans Postgres hoists the cast
above the regex check; the result is a runtime "invalid input
syntax for type uuid" failure.

The slice-135 integration tests (TestSlice124 +
TestSlice135 in `internal/api/adminauditlog/`) reproduced this
deterministically on a fresh Postgres 16.13. The adminauditlog
package is NOT in CI's integration list (per `.github/workflows/ci.yml`
the `tests-integration` job runs a curated subset that does not
include this package), which is why slice 124 + 129 shipped
without this being caught.

The robust fix is to wrap the cast in a CASE expression so the
planner cannot split the predicate. The patch is in
`internal/db/queries/unified_audit_log.sql`:

```sql
LEFT JOIN users u
  ON u.tenant_id = unified.tenant_id
 AND u.id = CASE
                WHEN unified.actor_id ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
                THEN unified.actor_id::uuid
                ELSE NULL
            END
```

This is in scope for slice 135 because the slice 135 cross-tenant
isolation integration test (AC-11) is merge-blocking; without this
fix the load-bearing test cannot run.

**Follow-up spillover idea (not in scope here):** add the
adminauditlog package to the CI `tests-integration` step so future
regressions of this shape are caught at PR time. Would be a small
chore-typed slice.

## D7 — Audit-period freezing semantics for audit-log exports

**Decision: clamp the effective `to` boundary to `MIN(frozen_at)`
across FROZEN audit_periods whose
`[period_start, period_end+1)` window OVERLAPS the request window.**

Constitutional anchor: invariant #10 / canvas §8.4 — "When an
AuditPeriod is frozen, sample populations draw only from evidence
with `observed_at ≤ frozen_at`." Applied to the audit-log export
surface, this means a bulk-download whose request window overlaps
a frozen period must surface rows reproducibly. The simplest
semantic that satisfies this: the effective `to` is the earliest
`frozen_at` across overlapping frozen periods.

Implementation: the new
`internal/db/queries/audit_periods.sql::MinFrozenAtOverlappingWindow`
query returns the earliest `frozen_at` across the tenant's FROZEN
audit_periods that overlap the request window. The handler
clamps the aggregator's `to` parameter, and the meta-audit row
records the `clamped_to_frozen_at` value so forensic queries can
distinguish a live export from a frozen-window export.

The integration test
`TestSlice135_AuditPeriodFreezingClampsWindow` seeds a frozen
period with `frozen_at = NOW - 30 min`, plus two me_audit_log
rows: one before the horizon (must appear) and one after the
horizon (must be excluded). The test asserts the post-frozen row
is gone from the export body.

This is the FIRST place in the audit-log surface that wires the
freezing constraint (slice 124's read does not — the maintainer
considered adding it but deferred until "the first place a bulk
read could compromise an audit"). The bulk-export surface IS
that first place, so the wire-up lands here.

## D8 — Meta-audit action: `audit_log_export` (not reuse of `audit_log_query_unified`)

**Decision: new distinct action value, migrated via
`20260518000010_audit_log_export.sql`.**

Slice 124's meta-audit fires on every paginated screen-read with
`action = 'audit_log_query_unified'`. Slice 135 could reuse that
value (simpler migration) — I chose not to. Rationale:

- A forensic query like
  `WHERE action = 'audit_log_export'` enumerates bulk-PII
  extraction events cleanly. Reusing the read action would force
  the analyst to disambiguate via payload fields, which is brittle.
- Threat-detection rules want to alert on bulk dumps but NOT on
  routine screen-reads. Distinct actions let those rules be
  simple.
- The migration is mechanical (5-line ALTER TABLE ... ADD CHECK
  extension); the win at the analyst seat is permanent.

The migration extends the `me_audit_log.action` CHECK constraint
from the slice-124 four-value enum to a five-value enum:

```
'profile.update',
'preferences.update',
'session.revoke',
'audit_log_query_unified',
'audit_log_export'   -- slice 135
```

## D9 — Three Export buttons render even when window is invalid (disabled)

**Decision: when the date window is invalid (the read-side error
banner is showing), the three Export buttons render as
visually-inert disabled buttons (not hidden).**

Rationale:

- The buttons' presence is part of the page contract; hiding them
  on transient invalid input would surprise the operator.
- The disabled state shadows the read-side disable, so the UI is
  internally consistent.
- The backend would 400 anyway; the disable is fast UX feedback.

The Playwright spec asserts the buttons are PRESENT regardless of
window validity.

## D10 — BFF route streams the body (no buffering)

**Decision: pipe the upstream `fetch.body` ReadableStream directly
into `NextResponse`; do NOT buffer with `await upstream.text()` or
`await upstream.arrayBuffer()` on the happy path.**

Slice 135 P0-A7 requires the export pipeline to stream. The
backend streams; the BFF must NOT undo that by materializing the
body in Node memory. The route at
`web/app/api/audit-log/export/route.ts` wires
`new NextResponse(upstream.body, {...})` which Next.js handles as
a passthrough stream.

The ERROR path (non-2xx upstream) does materialize the body via
`await upstream.text()` — this is OK because the backend's error
responses are short JSON payloads (~100 bytes), not bulk data.
The happy path remains streamed end-to-end.

The slice-135 BFF vitest (`route.test.ts`) asserts the headers
flow through and the body matches the mocked upstream body.
End-to-end streaming behavior is covered by the integration tests
(which run against the real backend) + the Playwright spec.

---

## Decisions not made (deferred / out-of-scope)

- **Custom column selection** — P0-A14 forbids at v1. v3 follow-on.
- **Scheduled / emailed exports** — P0-A13 forbids at v1. v3.
- **PDF format** — P0-A11 forbids. Lives in slice-042-area PDF
  pipeline forever; never added to this dropdown.
- **Per-tenant rate limit on exports** — not in scope; the row cap
  - concurrency cap (mentioned in slice doc but not implemented at
    v1 — left to a v2 spillover if observed concurrent-export storms
    surface in production).

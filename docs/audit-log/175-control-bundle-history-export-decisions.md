# Slice 175 — Control bundle history export decisions

Slice 175 (`docs/issues/175-control-bundle-history-export.md`) is the
spillover from slice 137 D2: ships `GET /v1/controls/history/export`
as a sibling endpoint to slice 137's active-only
`GET /v1/controls/export`, exposing the supersession chain
(`superseded_by`, `superseded_at`) for auditor period-freeze
reconstruction.

The slice is typed **AFK** (no JUDGMENT — column shape is dictated by
the slice 175 P0-A-175-1 anti-criterion: 17 columns = slice 137's 15
in the same positions + 2 new at the end). Decisions below are the
mechanical wire-up calls plus a CI-delta scan record.

## D1 — UI surface: separate Export-History button cluster vs dropdown

**Decision:** Three NEW Export-History buttons (`Export History CSV`
/ `Export History JSON` / `Export History XLSX`) added to the
controls-page action bar, alongside the slice 137 three-button cluster.
NOT a dropdown.

**Why:**

- **The slice 137 D8 precedent is "link-group, not dropdown."** The
  rationale at slice 137 D8 was that a dropdown adds a stateful
  interaction (open/close) without buying anything operators need.
  The same argument applies here — extending the same link-group
  shape keeps the action bar shape consistent and matches operator
  muscle memory ("click and a file downloads").
- **Six buttons is still tractable.** The controls action bar at
  slice 137 carries three Export buttons + the disabled "New control"
  button = 4 elements. Adding three more = 7 elements. Acceptable
  density; if it gets unwieldy at a future slice (e.g., a JSON-Lines
  format ships, doubling the button count), a dropdown migration
  becomes the right answer THEN, not pre-emptively.
- **The label "Export History …" carries the semantic distinction.**
  Operators reading the action bar see at a glance that the two
  clusters correspond to two different export shapes. The "History"
  prefix is short enough not to wrap on a typical viewport.
- **The data-testid scheme keeps Playwright assertions clean.**
  `controls-export-csv` (slice 137) and `controls-history-export-csv`
  (slice 175) are mutually unambiguous; the Playwright spec at
  slice 098 (currently quarantined behind slice 082) can target
  either without ambiguity.

**Rejected:**

- **Dropdown menu.** Same critique as slice 137 D8 — stateful
  interaction, no operator-visible benefit.
- **Replace slice 137 cluster with a unified "Export …" dropdown
  carrying both shapes.** Would break slice 137's data-testid
  contract + force operators to learn a new interaction model for a
  surface they already use. Reject.
- **Hide the history-export buttons behind a "More …" affordance.**
  Pure space optimisation at the cost of discoverability. The
  Export-History buttons are the primary surface for an auditor
  workflow that v1's primary user (the solo security leader) needs
  during audit prep. Hiding them is wrong.

## D2 — Query approach: duplicate query vs extend slice 137 query

**Decision:** **Duplicate query.** A new `ListControlsHistoryForExport`
sqlc query at `internal/db/queries/controls.sql` returns the 17-column
projection without filtering on `superseded_by IS NULL`. The slice
137 `ListActiveControlsForExport` is left untouched.

**Why:**

- **Slice 137 D2 explicitly rejected including `superseded_by` /
  `superseded_at` in the active-only export** because those columns
  would always be NULL for the active row set. Extending the slice
  137 query to add those columns would re-introduce the same
  always-NULL noise against its existing consumers (compliance gap
  analysis, auditor handoff index sheets).
- **The two exports are different downstream consumers.** The
  active-only export is the day-to-day catalog dump; the history
  export is the auditor period-freeze reconstruction tool. Different
  consumers, different SQL, different wire shape. Keeping the queries
  separate keeps both projections clean.
- **Wire-shape stability.** Active-only consumers MUST keep seeing
  the slice 137 shape unchanged. Reshaping that query for the new
  consumer would force downstream tools to migrate — buying nothing.
- **Cost is minimal.** sqlc generates two functions sharing 95% of
  the column projection logic; the Go store adapter has the
  duplication too but it's narrow (40 lines of row marshalling). The
  cost is one regenerated `dbx/controls.sql.go` chunk per slice — a
  trivial overhead.

**Rejected:**

- **Single query with an `include_superseded` boolean param.** Adds
  branchy SQL (`WHERE tenant_id = $1 AND (CASE WHEN $2 THEN TRUE
ELSE superseded_by IS NULL END)`) — Postgres can't push the
  predicate as cleanly, and the row-type sqlc generates has to carry
  the supersession columns even on the active-only call path.
  Spreads the always-NULL noise across BOTH consumers. Reject.
- **Add the columns to slice 137's query, leave slice 137 handler
  filtering them out.** Worse than the prior — the row type changes
  but the column projection diverges between the two consumers,
  which is a recipe for sql.Row.Scan mismatches on a future schema
  evolution.

## D3 — `superseded_at` synthesis

**Decision:** `superseded_at` is **synthesised at projection time**
from the row's `updated_at` whenever `superseded_by IS NOT NULL`. NOT
a stored column. For active rows (no successor) it renders as an
empty cell.

**Why:**

- **The supersession transaction sets both columns in the same
  UPDATE.** `MarkControlSuperseded` (slice 009) bumps `updated_at =
now()` whenever it sets `superseded_by`, so for any superseded row
  `updated_at` IS the timestamp of the supersession event. The
  synthesis is correct by construction.
- **Zero schema cost.** Adding a stored `superseded_at TIMESTAMPTZ`
  column to the controls table is a separate slice's worth of work
  (migration + sqlc regen + supersession-transaction update +
  backfill for existing superseded rows). Slice 175 is 1d AFK — that
  surface is out of scope. The synthesis at projection time gets the
  AC-2 column without touching the schema.
- **Empty-cell on active rows is the right semantic.** A literal
  zero-value timestamp ("0001-01-01T00:00:00Z") would be both
  visually misleading and a downstream-tool footgun (CSV parsers
  might coerce it to epoch zero). Empty cell is unambiguous.
- **The synthesis is documented in three places.** The sqlc query
  doc-comment (`internal/db/queries/controls.sql`), the handler
  field doc (`controlHistoryExportRow.SupersededAt`), and this
  decisions log. A future contributor who wants to "promote"
  `superseded_at` to a stored column has clear breadcrumbs.

**Rejected:**

- **Add a stored `superseded_at TIMESTAMPTZ` column on controls.**
  Out of scope for a 1d slice. A follow-on slice can ship this when
  there's operator demand (e.g., the supersession-event timestamp
  needs to be queried/indexed independently of `updated_at`).
- **Return NULL `superseded_at` from the SQL query (alias `updated_at`
  conditionally).** Postgres allows a `CASE WHEN superseded_by IS
NULL THEN NULL ELSE updated_at END` projection but adds
  branch-in-SQL for no real benefit. The Go handler does the same
  branch with cleaner doc-strings.

## D4 — Streaming-memory test shape

**Decision:** Synthetic 50,000-row generator (in-process `iter.Seq`)
driven through all three encoders against `discardWriter`; assert
`runtime.MemStats.HeapAlloc` delta ≤ 200 MB across each encoder. Half
the synthetic rows simulate active controls (empty supersession
cells); half simulate superseded controls (populated supersession
cells). Mirrors slice 137's `TestSlice137_StreamingMemoryUnder200MBFor500KRows`
test scaled to 50K rows.

**Why:**

- **50K rows is the right scale.** The history export's 17-column
  projection is ~13% larger per row than slice 137's 15-column
  projection. A 50K-row test gives O(GB) of total streamed bytes
  through the encoder writer pipeline — well above the 200 MB live-
  heap budget — at O(2–3s) wall-clock. The slice 137 500K test runs
  for O(10s); scaling slice 175 down to 50K keeps the
  integration-suite friction lower while still being load-bearing
  against the streaming invariant.
- **The 50/50 active/superseded mix is the right shape.** Production
  history exports at a multi-product org will skew toward more active
  rows than superseded (most bundles are at version=1 or
  version=2); a 50/50 mix is a conservative-worst-case for per-row
  encoded size (every supersession row carries a full UUID +
  RFC3339 timestamp in the last two cells).
- **`HeapAlloc` delta is the right signal.** Streaming encoders
  should not retain rows; live heap should be roughly flat across
  the call. `HeapAlloc` is the live-heap-at-sample-time figure;
  comparing before/after with `runtime.GC()` brackets isolates the
  encoder's working-set growth from cumulative allocation.
- **200 MB matches slice 137's P0-A-UCF-3.** Same budget; same
  streaming invariant; same test mechanic. A future contributor
  cross-referencing both tests sees a consistent pattern.

**Rejected:**

- **500K rows (matching slice 137).** Test runs ~3× longer than
  needed to establish the invariant. Already-tested encoder
  primitives don't need re-testing at higher scale — the streaming
  contract is encoder-level, not row-count-level.
- **Real-DB seeding of 50K rows.** Would balloon the integration-DB
  for the slice's lifetime and make the test 100× slower. The
  encoder pipeline is the unit under test; the synthetic generator
  is the right granularity.

## D5 — Cross-tenant isolation test shape

**Decision:** Two tenants, each seeded with a control PAIR (v1
superseded + v2 active) via `seedControlPairForHistory`. Tenant A's
history export must include BOTH of A's ids; MUST NOT include either
of B's ids OR B's distinctive title. Run across all three formats
(CSV / JSON / XLSX). Mirrors slice 137's `TestSlice137_CrossTenantIsolationAllThreeFormats`.

**Why:**

- **The supersession chain is per-tenant.** RLS on the controls
  table is the load-bearing protection; the slice 175 query's
  `WHERE tenant_id = $1` clause is belt-and-suspenders alongside the
  GUC-driven RLS policy. The cross-tenant test asserts the
  application of BOTH guards.
- **Testing the FK chain is important.** The seed inserts v1 with
  `superseded_by = v2.id` — both rows MUST land in the same tenant
  for the FK to resolve. If a future schema regression broke the
  per-tenant FK constraint, this test would catch it via the seed
  failing.
- **Three-format coverage is cheap.** Each format's encoder has its
  own escape/quote/zip-write path; verifying the cross-tenant
  invariant in all three formats establishes that the RLS guard
  takes effect at the data-fetch layer, not at the encoder layer.

**Rejected:** No alternative seriously considered — slice 137's
shape is the precedent.

## D6 — Meta-audit action name (`controls_history_export`)

**Decision:** Meta-audit `action` value is `controls_history_export`.
Distinct from slice 137's `controls_export`.

**Why:**

- **Forensic distinguishability is the load-bearing property.** A
  query like `WHERE action = 'controls_history_export'` cleanly
  enumerates lineage-export events SEPARATELY from active-only
  catalog dumps. The two have different downstream consumers (gap
  analysis vs auditor period-freeze) and different sensitivity
  profiles (the lineage view exposes more state than the active-only
  view); keeping them distinguishable in the meta-audit log lets a
  security operator triage them independently.
- **Naming convention parity.** Slice 137 (`controls_export`), slice
  138 (`evidence_export`), slice 139 (`audit_periods_export` +
  `vendors_export`), slice 174 (`anchors_export`) all use plural
  entity + singular `_export` suffix. `controls_history_export` extends
  the convention with the qualifier `history` between the entity and
  the action — a small departure for clarity.
- **Down-migration parity.** Same defensive DELETE shape as slice
  137's `controls_export.down.sql` — drop the new value from the
  CHECK, restore the prior set, with a leading DELETE of any
  `controls_history_export` rows that might exist in the table at
  rollback time. Cheap insurance against future CI test-list churn.

## D7 — Route placement: `/v1/controls/history/export` literal

**Decision:** Endpoint is `GET /v1/controls/history/export`,
registered as a chi literal route alongside `/v1/controls/export`
(slice 137) and `/v1/controls/drift` (slice 016) — BEFORE any
`/v1/controls/{id}/...` patterns.

**Why:**

- **chi resolves static segments before wildcards.** The route
  `/v1/controls/history/export` is a 3-segment static path; chi's
  trie matches it before resolving `{id}` at segment 2. No shadowing
  of the existing `/v1/controls/{id}/history` route (slice 064).
- **`/v1/controls/drift` is the precedent.** Slice 016 registered
  `/v1/controls/drift` as a literal sibling of `/v1/controls/{id}/...`
  and chi resolves it correctly. The slice 175 placement follows the
  same shape.
- **Registration order in `httpserver.go` matches the precedent.**
  The slice 137 export handler is registered alongside the slice
  098 list handler at the top of the `/v1/controls/...` block;
  slice 175's handler is appended immediately after slice 137's.
  Future readers see the two as a unit.

**Rejected:**

- **`/v1/admin/controls/history/export`** (under the admin prefix).
  No precedent — slice 137 is at `/v1/controls/export`, not
  `/v1/admin/...`. Keep parity.
- **`/v1/controls/export?include_superseded=true`** (a query-param
  flag on the slice 137 endpoint). Conflicts with D2 (single query
  approach). The handler-level branching for the wire shape would
  bloat the slice 137 surface, defeating the "active-only export
  shape stays clean" discipline.

## D8 — CI-delta scan (slice 143 D8 / slice 202 D2 lineage)

**Decision:** Explicit verification of every CI gate, mirroring slice
202's D2 pattern (specific verification claims, not blanket "scan
clean"):

| Gate                                                    | Status                                                                                                                                                                                             | Verification                                                                                                                                                      |
| ------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `Go · build + test`                                     | PASS                                                                                                                                                                                               | `go test ./...` clean (all packages green); `internal/api/controls/` coverage 26.3% (floor 26%)                                                                   |
| `Go · lint`                                             | PASS                                                                                                                                                                                               | `just lint-go` → `0 issues`. Verified after adding 4 new test functions + 1 new test helper.                                                                      |
| `Go · sqlc generate diff`                               | PASS                                                                                                                                                                                               | Ran `just sqlc-generate`; `git diff internal/db/dbx/` shows ONLY the new `ListControlsHistoryForExport` function. No spurious diffs.                              |
| `Go · integration (Postgres RLS)`                       | NOT IN CI for `internal/api/controls/` package                                                                                                                                                     | The slice's integration test compiles with `-tags=integration`; will run on the integration suite when this package is added to the list. Migration-roundtrip OK. |
| `Frontend · vitest`                                     | PASS                                                                                                                                                                                               | 712 tests across 73 files; new tests at `controls-history-export.test.ts` (5) + `app/api/controls/history/export/route.test.ts` (6).                              |
| `Frontend · Playwright e2e`                             | N/A — controls-list spec is quarantined behind slice 082 seed harness                                                                                                                              | New `controls-history-export-*` data-testids land alongside slice 137's `controls-export-*`; quarantined spec is not blocking.                                    |
| `openapi-drift-check`                                   | PASS                                                                                                                                                                                               | Ran `just openapi-generate`; `scripts/check-openapi-drift.sh` → "no drift (207 routes documented)". New route entry inserted in sort order.                       |
| pre-commit hooks (gofmt / ruff / prettier / actionlint) | PASS                                                                                                                                                                                               | `pre-commit run --all-files` clean.                                                                                                                               |
| `coverage-gate`                                         | PASS                                                                                                                                                                                               | `cmd/scripts/coverage-gate -profile=cov.out` → "ALL CHECKS PASS"; 75 packages checked.                                                                            |
| Path-filter sanity                                      | New files at `web/app/api/controls/history/export/`, `web/lib/api/controls-history-export.{ts,test.ts}` trigger frontend jobs; `migrations/sql/`, `internal/api/`, `internal/db/` trigger Go jobs. | All filter paths land cleanly.                                                                                                                                    |

Specifically learned from slice 143's D8 regression: the floor at
`internal/api/controls: 26` is a tight gate. Adding the new handler
without compensating unit tests would have dropped coverage below
the floor. The 19 unit tests in `history_export_test.go` keep
coverage at 26.3% — a 0.3% margin. If a future PR shrinks coverage
further (e.g., by removing some of slice 137's tests), the gate
would fire; this is the correct ratchet behaviour per slice 069 D2.

## Spillovers filed

None. Slice 175 ships within its 1d AFK envelope without surfacing
out-of-scope work. Possible future follow-ons (NOT filed; only
mentioned for discoverability):

- **Promote `superseded_at` to a stored column.** Becomes worth doing
  if/when a query needs to index on it independently of `updated_at`
  — e.g., a "supersession events in the last 30 days" dashboard.
- **`include_superseded` query param on `/v1/controls/export`.**
  Becomes worth doing if/when operator workflows want to opt into the
  history shape without hitting a separate endpoint. NOT now —
  slice 137 D2's discipline is intact.

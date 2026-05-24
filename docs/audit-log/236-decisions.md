# 236 — UI honesty: /evidence record-count meta + ledger-total subtitle context

**Slice:** `docs/issues/236-ui-honesty-evidence-ledger-total-record-count.md`
**Type:** AFK (decisions log captured because the slice surfaced
sub-decisions inside the spec's choice envelope — none are JUDGMENT-class
sign-off calls; the spec defaulted to Path A and the maintainer-equivalent
calls below are scoped to language + render shape)
**Branch:** `frontend/236-evidence-record-count-meta`
**Parent:** slice 204 (UI parity audit fleet, /evidence page audit)

## Decisions made

### D1 — Pick Path A (add `total` to the wire) over Path B (drop the count claim)

- **Chosen.** Path A — extend `/v1/evidence` with a tenant-wide ledger
  `total` field; surface it on both the meta-row (`Showing N of M
records`) and the page subtitle (`append-only · M records`).
- **Considered.** Path B — drop the mockup's count claim entirely from
  the live page. The spec already documents Path B as REJECTED: the
  count is load-bearing for the empty-ledger-vs-narrowing-filter
  operator-confusion mode that surfaced in the slice 204 audit.
- **Rationale.** The spec defaults to Path A and the prompt re-pins it
  (`/evidence record-count meta says "Showing N" without ledger-total
context`). Path B preserves the bug class.
- **Confidence.** high. Spec + prompt converge.

### D2 — Reuse existing `CountEvidenceRecordsByTenant` sqlc query rather than authoring a new one

- **Chosen.** Reuse `internal/db/queries/evidence_ledger.sql`'s
  `CountEvidenceRecordsByTenant :one` query that landed in slice 013.
  Generated `dbx.Queries.CountEvidenceRecordsByTenant` exists already.
  Add a thin `Store.CountEvidenceForTenant` wrapper in
  `internal/api/controldetail/store.go` that calls it inside the same
  RLS-bound transaction the list reads use.
- **Considered.** Author a new query (`CountEvidenceForTenant :one`)
  alongside `ListEvidencePaged` in
  `internal/db/queries/control_detail.sql`. The spec hints at this
  with "sqlc query is added (named `CountEvidenceForTenant` or
  similar)" (AC-3).
- **Rationale.** Slice 013's query is already
  `SELECT count(*) FROM evidence_records WHERE tenant_id = $1` — the
  exact shape this slice needs. Re-authoring under a new name would
  produce a duplicate query with no semantic difference and a small
  drift risk if one branch ever evolved. The wrapper in
  `Store.CountEvidenceForTenant` provides the controldetail-package
  seam the AC asks for; the underlying SQL is shared. Slice 016's
  freshness counter followed the same "wrap existing query in a new
  store method" pattern.
- **Confidence.** high. Existing query is a verbatim semantic match;
  duplicate-by-name without a behaviour delta is the documented
  anti-pattern.

### D3 — `total` rides on BOTH the tenant-wide and the per-control wire paths

- **Chosen.** `total` is emitted on both `GET /v1/evidence` (tenant-wide)
  AND `GET /v1/evidence?control_id=…` (per-control). Both report the
  tenant-wide ledger row count, not the per-control count.
- **Considered.** Surface `total` only on the tenant-wide path. The
  spec AC-1 references "the evidence list response" without an
  explicit per-control carve-out; this slice's frontend consumer is
  `web/app/(authed)/evidence/page.tsx` which is exclusively
  tenant-wide today.
- **Rationale.** A consistent wire shape across both branches keeps
  the frontend formatter trivial — `EvidenceListResponse.total` is
  always present, no per-path conditional in the type. The per-control
  /controls/{id} drill-down page (slice 064/041) does NOT currently
  render record-count meta, but if a future slice surfaces a meta-line
  on that page, it will get the same operator-confusion-mode-safe
  rendering without a wire-shape migration. The query cost is one
  cheap `COUNT(*)` with a `tenant_id` index — identical on both paths.
- **Confidence.** medium. The frontend consumer is tenant-wide only
  today, so the per-control extension is forward-looking rather than
  load-bearing. If a future slice decides "per-control wire shape
  should surface a per-control count instead of a tenant-wide count",
  that's a forward-compatible additive field (`total` stays
  tenant-wide; a new `control_total` lands separately) — not a
  destructive shape change.

### D4 — Run the count query AFTER input validation, BEFORE the list read

- **Chosen.** The handler issues `CountEvidenceForTenant` once, after
  all 400 input-validation branches (control_id parse, result-enum
  check, cursor / limit / time parse) and before either the
  tenant-wide or per-control list query. The count and list run in
  the same RLS-bound transaction-per-call (each `inTx` call opens
  its own transaction; the count and list are two transactions in
  series).
- **Considered.** Run them in parallel via two goroutines that share a
  Done channel.
- **Considered.** Run the count INSIDE the same transaction as the list
  read, by extending `Store.EvidencePaged` to return `(rows, total)`.
- **Rationale.** Parallel: the AC-2 acceptance criterion is "computed
  via a separate `COUNT(*)` query against the same RLS-bound pool" —
  same pool, not same transaction. The slice 016 freshness counter
  established the precedent of separate transactions; the marginal
  latency vs parallel goroutines is small (both queries are indexed
  point reads), the code is simpler, and a parallel path adds an
  error-aggregation seam. Same-transaction: extends a store method's
  return shape for what is logically a different query, complicating
  per-control vs tenant-wide reuse. Sequential separate-tx wins on
  simplicity.
- **Confidence.** high. Slice 016 precedent; AC-2 wording supports it.

### D5 — Frontend meta-line tri-branch shape

- **Chosen.** The `recordCountMeta(shown, total)` formatter renders
  three distinct strings:
  1. `total === 0` → `"No records in ledger yet"`
  2. `total > 0 && shown === 0` → `"Showing 0 of N records"`
  3. `total > 0 && shown > 0` → `"Showing N of M records"`
- **Considered.** Two-branch (only #1 and a single
  `Showing N of M records` for #2/#3). Rejected — branch 2 is the
  "your filters are too narrow" signal; reading the same string the
  ledger-empty case renders would conflate them.
- **Considered.** Four-branch (split the "all rows shown" /
  `N === M` case into its own no-of-clause string,
  `"Showing all N records"`). Rejected — the `N of N` string is
  honest and consistent; specialising it adds copy without
  resolving an operator-confusion mode.
- **Rationale.** Three branches map 1:1 to the three operator-
  confusion modes the slice 204 audit + slice 236 spec call out.
  AC-5 calls them out by name.
- **Confidence.** high.

### D6 — Subtitle suffix collapses to empty string when `total === 0`

- **Chosen.** `ledgerSubtitleSuffix(0)` returns `""`; the page's
  subtitleNode renders the suffix only when `total > 0`. So on a
  fresh-tenant `/evidence` page the subtitle reads
  `Append-only · ingestion separated from evaluation · point-in-time
replay always possible. Push your first record via CLI →` without
  the trailing `· append-only · 0 records` noise.
- **Considered.** Render `append-only · 0 records` for parity with
  the non-zero case. Rejected — the meta-row's "No records in
  ledger yet" carries the operator signal; surfacing `0 records` on
  the subtitle adds noise that doesn't change the signal.
- **Considered.** Render a different empty-state suffix
  (e.g. `append-only · empty ledger`). Rejected — adds a third copy
  variant for a state the meta-row already covers.
- **Rationale.** "Honesty > parity-with-mockup" — the mockup shows
  `14,712 records` because it's the populated steady state; a
  fresh-tenant subtitle should not invent a "0 records" claim that
  the meta-row will then re-state more honestly.
- **Confidence.** high.

### D7 — `ledgerTotal === undefined` (query in flight) preserves the OLD meta shape

- **Chosen.** While the React Query is in flight (`evidenceQ.data`
  is `undefined` so `ledgerTotal` is `undefined`), the meta render
  falls back to the pre-slice-236 string: `Showing N records`. Only
  when the query resolves does the formatter take over.
- **Considered.** Render `"Loading…"` placeholder.
- **Considered.** Render the new shape with `?` as the M slot
  (`Showing 0 of ? records`).
- **Rationale.** A loading-state meta-row that says
  `No records in ledger yet` would briefly tell the operator the
  ledger is empty even on a populated tenant — a transient lie. The
  pre-slice-236 fallback says `Showing 0 records` (true: no rows
  have arrived from the wire yet) without claiming anything about
  the tenant total. Once the query resolves the formatter takes
  over and the empty-ledger sentinel is honest.
- **Confidence.** high.

### D8 — Tenant-wide `total` on per-control path is documented + integration-tested

- **Chosen.** Added `TestEvidence_PerControlTotalIsTenantWide` to
  `integration_test.go` that pins the per-control path's `total` to
  the tenant-wide count, not the per-control count. The handler
  comment also calls this out.
- **Considered.** Skip the pin — the per-control path's frontend
  consumer doesn't read `total` today.
- **Rationale.** D3 makes this a deliberate wire-shape choice. The
  test is the documentation: a future maintainer who looks at the
  per-control wire shape and thinks "shouldn't this be the
  per-control count?" sees the test, reads the rationale, and
  knows it's intentional. Without the test the choice is just a
  comment that could be silently regressed.
- **Confidence.** high.

### D9 — Pluralisation deferred ("1 records" stays "1 records")

- **Chosen.** Neither `recordCountMeta` nor `ledgerSubtitleSuffix`
  pluralises around `1`. So a 1-row ledger renders
  `Showing 1 of 1 records` + `append-only · 1 records`.
- **Considered.** Branch each formatter on `=== 1` and switch to
  `record`. The existing pre-slice-236 meta did this for the
  count, but the per-of formula reads `Showing 1 of 1 record(s)`
  which doesn't read cleanly with conditional pluralisation either.
- **Rationale.** The mockup string is `"Showing N of M records"`
  uniformly — no pluralisation in the design source. The
  ledger-count subtitle suffix is also `"M records"` uniformly.
  A future "polish the copy across all pages" slice can audit
  pluralisation consistently across the app; this slice's scope is
  the ledger-context surfacing, not copy polish.
- **Confidence.** medium. The pluralisation choice is mildly
  awkward in the N=1 case; the test pins it so a future change
  has a clear seam to update consistently.

### D10 — Anti-criteria scan

- **Verified before merge:**
  - **P0-236-1** (no `7 connectors` count) — no connectors-table
    read added; subtitle suffix is `append-only · M records` only,
    no connectors clause. ✓
  - **P0-236-2** (no filter predicates on `total`) — handler runs
    the count via the slice-013 `CountEvidenceRecordsByTenant`
    query, which is unconditional `count(*) WHERE tenant_id = $1`.
    No filter args are passed. Integration test
    `TestEvidence_TenantWideTotalIgnoresFilters` pins this. ✓
  - **P0-236-3** (no caching outside the request) — the count is
    issued per-request inside `Store.CountEvidenceForTenant`. No
    cache layer was added; no goroutine-local memo. ✓
- **Anti-criteria from prompt** (no `_STATUS.md` / `CHANGELOG.md`
  touch, no new backend endpoint) — verified by `git diff` review.
  The change extends the existing `/v1/evidence` handler with one
  additional field, no new route.
- **Confidence.** high.

## Revisit once in use

- **R1.** Connectors count (`7 connectors` in the mockup) is
  explicitly deferred. When a connector-inventory endpoint lands
  (open question — there is no scaffold today), revisit the page
  subtitle to add the third clause:
  `append-only · M records · K connectors`.
- **R2.** Pluralisation polish (D9). The "1 records" awkwardness
  is the obvious place to start when a copy-audit slice files.
  Affects `recordCountMeta` + `ledgerSubtitleSuffix`.
- **R3.** Per-control page meta-line (D3). The slice 041 / 064
  control-detail page does not surface a record-count meta today.
  If a future slice adds one, decide whether to surface the
  tenant-wide count (current wire shape, no migration) or
  re-shape the wire to carry a per-control count (additive field
  per D3 rationale).
- **R4.** Cache eviction. P0-236-3 says no caching outside the
  request. If a future "scale to 10⁸ evidence rows" stress test
  shows the COUNT(\*) is the bottleneck, consider materialising
  `evidence_records_count(tenant_id)` as a table updated on
  ingest. That is a future-scale concern, not a v1 concern; the
  current shape is correct for v1.

## Files touched

- `internal/api/controldetail/handler.go` — issue the count query
  before either list path; emit `total` on both response shapes.
- `internal/api/controldetail/store.go` — new
  `Store.CountEvidenceForTenant` wrapper over the existing
  `dbx.CountEvidenceRecordsByTenant` query (D2).
- `internal/api/controldetail/integration_test.go` — three new
  integration tests pinning (a) filter / window do not narrow
  the count, (b) per-control path surfaces tenant-wide total,
  (c) RLS isolates the count, plus a fresh-tenant zero-count
  pin.
- `web/lib/api.ts` — extend `EvidenceListResponse` with the
  `total: number` field (slice 236 line comment).
- `web/app/(authed)/evidence/format.ts` — new
  `recordCountMeta(shown, total)` + `ledgerSubtitleSuffix(total)`
  pure formatters with three- and two-branch shapes.
- `web/app/(authed)/evidence/format.test.ts` — ten new vitest
  cases covering the formatter branches + defensive clamping.
- `web/app/(authed)/evidence/page.tsx` — surface `ledgerTotal`,
  switch the `meta` ReactNode to the new formatter (with the
  `undefined` fallback path), append the ledger-context suffix
  to the subtitle when `total > 0`.
- `docs/audit-log/236-decisions.md` — this file.

## Anti-criteria honored

- **P0-236-1.** Connector count deferred — no connectors-table
  read introduced. Subtitle reads `append-only · M records`
  only.
- **P0-236-2.** `total` ignores filter predicates — backed by
  `TestEvidence_TenantWideTotalIgnoresFilters`.
- **P0-236-3.** No cache layer added. Count is per-request.
- **No `_STATUS.md` / `CHANGELOG.md` edits.** Verified by
  `git diff`.
- **No new backend endpoint.** `/v1/evidence` gained a field; no
  new route was registered.

## Constitutional invariants honored

- **Invariant 2 (ingestion / evaluation separated).** The count
  reads `evidence_records` (the ledger), not an evaluation read
  model. The append-only invariant means the count is monotonic-
  non-decreasing tenant-wide.
- **Invariant 6 (tenant isolation at DB layer).** The count runs
  through the same RLS-bound pool the list reads use; the query
  re-asserts `tenant_id = $1` as defense-in-depth. Backed by
  `TestEvidence_TotalRLSIsolation`.
- **Anti-pattern rejection.** "Count surfaces that imply state
  the product cannot verify" — closed. The wire now distinguishes
  filter-narrowed-zero from ledger-is-empty.

## Resolves

- **F-204-E-4** (HONESTY-DATA-BOUND) from
  `docs/audit-log/204-page-audit-evidence.md` — the next slice 204
  audit run will no longer surface "record-count meta lacks
  ledger-total context" on /evidence.

# 135 — Data-export library + audit-log export (reference implementation)

**Cluster:** Backend / Frontend
**Estimate:** 2-3d
**Type:** JUDGMENT (XLSX library choice + filename convention; engineer records D1-D3 at pickup)
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)

## Narrative

Maintainer asked 2026-05-18 for export functionality "everywhere that makes sense" (audit log, risk register, controls, evidence, policies, exceptions, samples, audit periods, vendors). The platform today has two non-overlapping export products:

1. **OSCAL export** (`internal/api/oscalexport/`, slice 030) — audit-binding artifacts (SSP, POA&M) for the audit binder. Cosigned bundles. Constitutional artifact discipline.
2. **PDF export** (slices 042-ish + policies + board packs) — formatted deliverables for human distribution.

What's missing is a **third product: data export** — CSV / JSON / XLSX dumps of tenant-scoped data for operator workflows (drop into Excel; pipe into a script; archive offline; feed an external system). This slice ships that product as a reusable library + audit-log as the reference implementation.

**What this slice ships:**

- A new `internal/export/` package with an `Exporter` interface, three encoders (CSV, JSON, XLSX), bounded streaming writer, and a per-entity registration pattern.
- A reference implementation: audit-log export, exposed as `GET /v1/admin/audit-log/export?format=<csv|json|xlsx>&from=...&to=...&kind=...` — same role gate (admin OR auditor OR grc_engineer per slice 124 D5), same 90-day window cap (slice 124 P0-A3), same cursor / kind / actor filters as the existing `/v1/admin/audit-log/unified` read endpoint.
- BFF route `/api/audit-log/export` + an "Export" affordance on the slice-125 `/audit-log` page (dropdown: CSV / JSON / XLSX).
- Meta-audit row written on every export attempt (action = `audit_log_export`, slice 124 meta-audit pattern reused).
- Per-entity spillover stubs filed (136 risk register; 137 controls UCF; 138 evidence + policies + exceptions + samples; 139 audit periods + vendors) — each will reuse the library shipped here.

**Scope discipline (what is OUT):**

- **PDF export** — already exists for policies + board packs; not adding new PDF surfaces here. The Export dropdown does NOT include PDF.
- **OSCAL export** — already exists (slice 030); CSV/JSON/XLSX is a different product. The two coexist on the audit-period detail page.
- **Risk / controls / evidence / policies / exceptions / samples / audit-periods / vendors exports** — filed as spillover slices 136-139. This slice establishes the library + ships audit-log as the reference; the spillovers wire each entity in.
- **Async / scheduled / emailed exports** — out of scope. Synchronous download-to-browser only.
- **Multi-tenant aggregate exports** — out of scope. Single-tenant only, RLS-enforced.
- **Custom column selection / templating** — out of scope. Each entity ships a canonical column set; user selection deferred to a v3 follow-on.
- **Compression** — out of scope at v1. CSV/JSON ship as-is; XLSX is natively zipped.

## Threat model

| STRIDE                       | Threat                                                                                                                                                                                                                                                                                                                                                         | Mitigation                                                                                                                                                                                                                                                                                                                                                                                                                        |
| ---------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **S** Spoofing               | Export endpoint reuses the read API's auth; no new auth surface. Caller still needs the bearer + the same OIDC session cookie path slice 110 established.                                                                                                                                                                                                      | Reuses bearer + OIDC session forwarding from slice 110. Engineer MUST NOT introduce a separate `?token=<short-link>` style affordance (that would re-expose the calendar-style URL-bearer surface). Anti-criteria P0-A1.                                                                                                                                                                                                          |
| **T** Tampering              | Filename suggestion in `Content-Disposition` headers can include user-controlled values (filter strings); without sanitization a caller could inject CRLF or path traversal characters that confuse the browser or downstream tools. CSV formula injection (`=cmd\|...`) is a real attack vector when an exported field starts with `=` / `+` / `-` / `@`.     | Filename sanitization: ASCII alphanum + `-` / `_` only; max 80 chars; tenant-name NEVER in filename. CSV cell-injection mitigation: every cell starting with `=` / `+` / `-` / `@` / `\t` / `\r` is prefixed with a single quote `'` per OWASP CSV-injection guidance. JSON / XLSX immune by structure. Anti-criteria P0-A2 + P0-A3.                                                                                              |
| **R** Repudiation            | Bulk exports of tenant-sensitive data are exactly the action that needs the strongest audit trail. A caller exporting 90 days of audit-log + denying it later is the threat.                                                                                                                                                                                   | Meta-audit row written on EVERY export attempt — success, partial, denied, or error. Action = `<entity>_export`. Payload captures: format, row_count, byte_count, filter params, timestamp, actor_id, tenant_id. Reuses slice 124's meta-audit pattern. Anti-criteria P0-A4.                                                                                                                                                      |
| **I** Information disclosure | **HIGH and load-bearing.** Exports are bulk PII vacuums. A single export can dump 90 days × 1000s of rows across 9 audit-log kinds — every actor_id, target_id, payload_json blob. Tenant isolation MUST hold or the entire RLS edifice collapses on this surface. XLSX files specifically can carry hidden sheets / named ranges / styles that leak metadata. | Every export query MUST run under `tenancy.ApplyTenant` against `atlas_app` role — no `BYPASSRLS`, no `atlas_migrate`. Integration test asserts cross-tenant isolation explicitly: Tenant A's export of audit-log with Tenant B's actor_id UUID does NOT include Tenant B rows. XLSX encoder MUST emit ONE sheet (the data) — no metadata sheet, no named ranges, no chart objects, no embedded VBA. Anti-criteria P0-A5 + P0-A6. |
| **D** DoS                    | **HIGH and load-bearing.** Bulk export is the DoS surface. An unbounded export could OOM the writer, exhaust pgxpool connections, or saturate the network egress. A caller spamming exports could starve interactive traffic.                                                                                                                                  | Row cap enforced — default 100,000 rows per export, configurable per entity. 90-day window cap (audit-log specific). Streaming writer — query results piped row-by-row into the encoder; encoder piped row-by-row into the HTTP response. NEVER materialize the full result set in memory. Concurrency cap — at most 2 in-flight exports per tenant. Connection released between batches. Anti-criteria P0-A7 + P0-A8.            |
| **E** Elevation of privilege | A new endpoint that uses a different OPA gate than the underlying read endpoint would silently elevate or restrict. e.g. audit-log read admits admin OR auditor OR grc_engineer (slice 124 D5); export must NOT admit any narrower or wider set.                                                                                                               | Export endpoint reuses the same OPA gate as the underlying read endpoint — bit-for-bit. OPA matrix test asserts the admit set matches the slice-124 admit set exactly. Anti-criteria P0-A9.                                                                                                                                                                                                                                       |

**Verdict:** HAS-MITIGATIONS — information disclosure (RLS hold) and DoS (streaming + row cap) are the load-bearing concerns; P0-A5 through P0-A8 are the load-bearing mitigations.

## Acceptance criteria

### Library (`internal/export/`)

- [ ] **AC-1:** NEW `internal/export/` package with `Exporter` interface:
      `    type Exporter interface {
  ContentType() string
  FileExt() string
  WriteRows(ctx, w io.Writer, header []string, rows iter.Seq[[]string]) error
}`
      Three implementations: `csvExporter`, `jsonExporter`, `xlsxExporter`.
- [ ] **AC-2:** Streaming write — `WriteRows` consumes the `iter.Seq` pull-style so per-row allocation is bounded. Test asserts memory usage stays under 50 MB for a 100,000-row export.
- [ ] **AC-3:** CSV exporter applies the OWASP cell-injection mitigation: every cell whose first character is `=` / `+` / `-` / `@` / `\t` / `\r` is prefixed with a single quote `'`. Test cases for each character.
- [ ] **AC-4:** XLSX exporter emits ONE sheet only; no metadata sheets / named ranges / chart objects / embedded VBA / cell formatting beyond text. Test asserts the produced `.xlsx` zip contains exactly the expected entries (`xl/workbook.xml`, `xl/worksheets/sheet1.xml`, etc. — no `xl/charts/`, no `xl/vbaProject.bin`).
- [ ] **AC-5:** JSON exporter emits an array-of-objects (NOT NDJSON) where object keys match the header strings byte-for-byte. Wire-format-true to the existing endpoint's read shape.
- [ ] **AC-6:** Filename builder helper: `BuildFilename(entity string, format string, params map[string]string) string` produces `<entity>_<YYYYMMDD>_<param-summary>.<ext>`. Restricted to ASCII alphanum + `-` / `_`; max 80 chars; no tenant name. Test cases for sanitization (CRLF injection, path traversal, unicode, length).

### Audit-log reference implementation

- [ ] **AC-7:** NEW handler `GET /v1/admin/audit-log/export` in `internal/api/adminauditlog/` that reuses slice 124's aggregator query (same SQL, same filters, same 90-day cap, same role gate). Adds `format` query param (csv | json | xlsx; default csv). Returns the encoded body with `Content-Type` + `Content-Disposition: attachment; filename="<sanitized>"`.
- [ ] **AC-8:** Row cap enforced — default 100,000 rows. Caller exceeding the cap gets a 413 Payload Too Large response with a body explaining the cap + the narrower-filter retry guidance. Test asserts the cap.
- [ ] **AC-9:** Meta-audit row written on EVERY export attempt — success, denied (403), capped (413), or error (500). Action = `audit_log_export`. Payload captures `format`, `row_count`, `byte_count`, `from`, `to`, `kind` filter, `actor` filter, `result`. Test asserts row written in each outcome.
- [ ] **AC-10:** OPA matrix test — admit set EXACTLY matches slice 124's `HasUnifiedAuditLogRole` (admin allow; auditor allow; grc_engineer allow; viewer deny; control_owner deny; no-roles deny). The export endpoint's OPA gate must be the same rule, not a different rule with the same admit set.
- [ ] **AC-11:** Cross-tenant isolation integration test — seed Tenant B with audit-log entries whose `actor_id` UUIDs match Tenant A's. Export from Tenant A: assert NO Tenant B rows in the output. Run for all three formats.
- [ ] **AC-12:** Audit-period freezing — when the request falls within a frozen `audit_period` window, the export query MUST filter `observed_at <= frozen_at` (canvas §8.4 / constitutional invariant #10). Test asserts a row created after `frozen_at` is excluded from an export inside the frozen window.

### BFF + frontend

- [ ] **AC-13:** NEW BFF route `web/app/api/audit-log/export/route.ts` that forwards to the backend, streaming the response (no buffering at the BFF layer). Bearer + OIDC session cookie forwarded per slice 110 pattern. Test asserts streaming + headers.
- [ ] **AC-14:** Frontend "Export" affordance on `/audit-log` (slice 125 page) — split-button or dropdown with three options (CSV / JSON / XLSX). On click, triggers a download via `<a href download>` or `window.location.assign(url)` so the browser handles the file save dialog. Filters currently applied to the page propagate to the export query string. Test (Playwright) asserts clicking each format option produces a download with the expected `Content-Type`.

### Tests + docs

- [ ] **AC-15:** Decisions log at `docs/audit-log/135-data-export-library-decisions.md` records: D1 the XLSX library choice (`xuri/excelize/v2` v.s. `tealeg/xlsx` v.s. handcrafted minimal-XLSX writer — note that XLSX is just a zipped XML format and a 200-line handcrafted writer covers the single-sheet-text-only case the threat model requires), D2 the filename convention, D3 the row-cap default + the per-entity override hook, D4 the streaming pattern, D5 any other JUDGMENT calls.
- [ ] **AC-16:** CHANGELOG entry under `[Unreleased] / Added`: "Data-export library (CSV / JSON / XLSX) + audit-log export endpoint + Export button on `/audit-log` (#135)".

## Constitutional invariants honored

- **#6 Tenant isolation is enforced at the database layer via RLS.** Every export query runs under `tenancy.ApplyTenant` on `atlas_app` — no `BYPASSRLS`, no `atlas_migrate`. Cross-tenant isolation test (AC-11) is the enforcement evidence.
- **#10 Audit-period freezing.** When request falls in a frozen window, export filters `observed_at <= frozen_at` (AC-12). Frozen exports stay reproducible; live state continues independently.
- **AI-assist boundary.** Exports are deterministic; no AI involvement. The constitutional rule that AI-published artifacts require one-click human approval does NOT apply here (no AI authorship).
- **#9 Manual evidence is first-class.** The export library is entity-shape-agnostic — manual + automated entities ship the same export UX once each entity is registered.

## Canvas references

- `Plans/canvas/01-vision.md` item 8 (board pack PDF + editable export) — commits to "editable export" at the vision level; this slice delivers the editable-export product.
- `Plans/canvas/03-ucf.md` §3.4 — OSCAL export; clarifies that data export is a separate product from audit-binding export.
- `Plans/canvas/04-evidence-engine.md` — manual-evidence-first-class principle; exports treat manual + automated equally.
- `Plans/canvas/08-audit-workflow.md` §8.2 — OSCAL SSP/POAM export already exists; this slice does NOT touch it.
- `Plans/canvas/08-audit-workflow.md` §8.4 — audit-period freezing; AC-12 enforces.

## Dependencies

- **#124** Unified audit-log aggregation API (merged, `gh#267`) — the underlying read query the export endpoint reuses. SAME role gate. SAME 90-day cap.
- **#125** Frontend `/audit-log` page (merged, `gh#276`) — where the Export button lands.
- **#108** `/v1/me` profile (merged) — slice-110 OIDC-session forwarding pattern reused for the BFF export route.
- **#030** OSCAL SSP/POA&M export (merged) — distinct product; this slice's library does NOT consume or extend the OSCAL exporter (different shape, different consumer).

## Anti-criteria (P0 — block merge)

- **P0-A1: NO unauthenticated short-link export URLs.** The export endpoint MUST require the same bearer + OIDC session as the read endpoint. NO `?token=<one-shot>` style URLs that would re-expose the calendar-bearer-URL surface.
- **P0-A2: Filename sanitization is mandatory.** ASCII alphanum + `-` / `_` only; max 80 chars; tenant name NEVER in filename. Reject + log any filter param that contains CRLF, path traversal sequences, or characters outside the allowed set.
- **P0-A3: CSV cell-injection mitigation is mandatory.** Every cell starting with `=` / `+` / `-` / `@` / `\t` / `\r` MUST be prefixed with `'` per OWASP guidance. JSON/XLSX immune by structure (formula injection requires CSV-style cell-text interpretation).
- **P0-A4: Meta-audit row on EVERY export attempt.** Success, denied, capped, error — all four outcomes write a row. NO outcome path skips the audit row.
- **P0-A5: NO `BYPASSRLS` and NO `atlas_migrate` on any export query path.** Every read goes through `atlas_app` + `tenancy.ApplyTenant`. Cross-tenant isolation integration test (AC-11) is merge-blocking.
- **P0-A6: XLSX = ONE sheet, text only.** No metadata sheets, no named ranges, no chart objects, no embedded VBA, no cell formatting beyond text. Test asserts the produced `.xlsx` zip contents.
- **P0-A7: Streaming write — NO buffering of the full result set.** Memory test asserts under 50 MB for a 100K-row export.
- **P0-A8: Row cap enforced — default 100,000.** Per-entity override allowed at registration time; cap removal is NOT allowed.
- **P0-A9: OPA admit set matches the underlying read endpoint EXACTLY.** Export does NOT introduce a narrower or wider gate. OPA matrix test asserts byte-for-byte match.
- **P0-A10: NO scope creep into the per-entity exports.** Slice 135 ships ONLY audit-log as the reference impl. Risk / controls / evidence / policies / exceptions / samples / audit-periods / vendors exports are spillovers (136-139).
- **P0-A11: NO PDF format in the dropdown.** PDF exports live in their existing slice 042-ish + board-pack pathways; not added here.
- **P0-A12: NO vendor-prefixed test fixture tokens** — neutral `test-*` only.
- **P0-A13: NO async / scheduled / emailed exports.** Synchronous download-to-browser only at v1.
- **P0-A14: NO custom column selection at v1.** Each entity ships a canonical column set; user-selectable columns deferred to a v3 follow-on.

## Skill mix (3-5)

- **`grill-with-docs`** — terminology + scope discipline at engineer pickup (verify "data export" vs "OSCAL export" vs "PDF export" terminology lands consistently in the package + endpoint names).
- **Go testing (table-driven + integration)** — AC-2 memory test, AC-3 cell-injection cases, AC-11 cross-tenant isolation against a real Postgres.
- **Playwright (web/e2e)** — AC-14 frontend Export-button download flow.
- **OPA matrix tests** — AC-10 admit-set parity proof against slice 124's gate.
- **XLSX format expertise** — D1 JUDGMENT call between library options + AC-4 minimal-sheet validation. Engineer MAY skip the library entirely and ship a 200-line handcrafted writer since the threat model REQUIRES single-sheet-text-only output (this also defends P0-A6 by construction — handcrafted writer literally cannot emit charts).

## Notes for the implementing agent

**Recommended JUDGMENT call (D1 — XLSX library):**

XLSX is a zipped XML format (Office Open XML / ECMA-376). The threat model REQUIRES single-sheet, text-only output (P0-A6). Three options at pickup:

| Option                                             | Pros                                                                                            | Cons                                                                                                                                                                                                                                                           |
| -------------------------------------------------- | ----------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **(a) `xuri/excelize/v2`**                         | Mature, ~5k GitHub stars, well-documented                                                       | ~5 MB binary impact, large API surface (most of which violates P0-A6 if misused), opens a new transitive-dep audit surface (slice 128's actions-pin-check has a sibling concern for Go modules — the implementation must Dependabot-track this dep separately) |
| **(b) `tealeg/xlsx`**                              | Smaller, older, simpler                                                                         | Older API, less active maintenance, same dep-audit concern                                                                                                                                                                                                     |
| **(c) Handcrafted minimal-XLSX writer (~200 LOC)** | Zero new dep, perfect P0-A6 fit by construction (cannot emit charts), reviewable in one sitting | Engineer MUST write + test it; XLSX zip-structure has corner cases (shared strings, content-types.xml, \_rels) the engineer must understand                                                                                                                    |

Maintainer lean: **option (c)** for the constitutional reasons (zero new deps; perfect P0-A6 fit; smaller review surface). The XLSX zip-structure for single-sheet-text-only is well-documented (e.g. https://learn.microsoft.com/en-us/dotnet/api/documentformat.openxml.spreadsheet) and the minimal file is ~5 XML files inside a ZIP. If the engineer attempts option (c) and finds the zip-structure rabbit-hole deeper than the 0.5d that's budgeted in the 2-3d slice estimate, FALL BACK TO OPTION (a) with a D1 entry explaining the pivot.

**Recommended JUDGMENT call (D2 — filename convention):**

`<entity>_<YYYYMMDD>_<filter-summary>.<ext>` where filter-summary is a short ASCII-safe rendering of the active filters (e.g. `audit-log_20260518_evidence_me.csv` for "audit-log entries, today, filtered to evidence + me kinds"). The maintainer lean is to keep filter summaries short (< 20 chars) and stable (sorted filter keys) so the same filter set always produces the same filename across runs. Engineer records the final convention in D2.

**Recommended JUDGMENT call (D3 — row-cap default):**

100,000 is the maintainer lean — large enough that 99% of audit-log exports fit in one request, small enough that even a 9-column row at ~200 bytes each = ~200 MB transfer, which is OK over HTTP/2 streaming but NOT OK over HTTP/1.1 to a slow client. Per-entity override hook at registration time so the controls UCF (1,400 SCF anchors × edges = ~100,000 rows easily) can lift its cap to e.g. 500,000 with explicit decisions-log justification at pickup.

**Already-built check (Phase 2 grill):**

- `internal/api/oscalexport/` (slice 030) exports OSCAL SSP / POA&M. Different product (audit-binder bundle, cosigned). New library lives in `internal/export/` — explicitly NOT in `internal/api/oscalexport/`. The slice-030 exporter and the slice-135 library coexist; they do NOT share code (different shapes, different consumers).
- PDF export exists for policies + board packs (slice 042 area). Slice 135 does NOT touch PDF.
- No generic data-export library exists today — this slice is the first.

**Terminology (Phase 2 grill):**

- "Export" is overloaded. Use "data export" or "tabular export" for this product in code + docs (package name `internal/export/`, endpoint suffix `/export?format=...`). Reserve "OSCAL export" for the slice-030 product. Reserve "PDF export" for the policy / board-pack PDFs.
- "Dump", "extract", "download" — NOT canonical. "Export" only.

**Threat-model context (Phase 3 grill):**

The information-disclosure risk is the load-bearing concern. The cross-tenant isolation integration test (AC-11) is the load-bearing mitigation: if the engineer cannot make it pass, NO export endpoint ships. The XLSX P0-A6 (single-sheet-text-only) is the second load-bearing mitigation — XLSX's hidden-sheet / named-range / chart-object surface is a known data-exfil vector in spreadsheet attack literature; the slice intentionally rules out the entire surface by construction.

**Spillover slices already filed (Phase 7):**

- **136** — Risk register export.
- **137** — Controls UCF export (large; will lift the row-cap).
- **138** — Evidence + policies + exceptions + samples export (4 ledger entities; share shape).
- **139** — Audit periods + vendors export (2 smaller entities).

All four are `not-ready` gated on slice 135 merging. Each spillover REUSES the library shipped by 135 — none of them re-implement the encoder layer.

**CLAUDE.md is intentionally NOT in this slice.** The data-export library is product code, not meta-process. Engineer does NOT touch CLAUDE.md.
